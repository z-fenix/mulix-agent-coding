package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"mulix/internal/cache"
	"mulix/internal/control"
	"mulix/internal/event"
)

// channelSink 把事件通道包装回 control.Sink（换绑时复用通道）。
type channelSink struct{ ch chan<- tuiEvent }

func (s channelSink) Publish(ev any) { s.ch <- tuiEvent{ev: ev} }

// sessionController 是 TUI 消费的会话控制面（*control.Controller 的子集——
// 状态机单测可注入替身）。
type sessionController interface {
	Submit(ctx context.Context, text string) error
	Approve(toolUseID string, allow bool, resolvedBy string) error
	ApproveSession(toolUseID, resolvedBy string) error
	Snapshot() control.Snapshot
}

// ---- 事件适配（control Sink → tea.Msg） ----

// capTuiEventCh 是事件通道容量：满时 Publish 阻塞背压（前端可慢不可丢——
// 镜像先行的对面；阻塞的是 control 的 publish，天然节流引擎事件流）。
// 已知限制（T28 评审 Minor-C，P2 权衡）：退出/换绑时若积压 >容量，旧泵的
// 收尾 publish 会阻塞至新消费者排空——语义为「延迟」而非「丢失/死锁」。
const capTuiEventCh = 1024

type tuiEvent struct{ ev any }

// tuiSink 把 control 事件流转成 tea 消息（control.Sink 实现）。
type tuiSink struct {
	ch chan tuiEvent
}

func newTuiSink() *tuiSink {
	return &tuiSink{ch: make(chan tuiEvent, capTuiEventCh)}
}

func (s *tuiSink) Publish(ev any) {
	s.ch <- tuiEvent{ev: ev}
}

func (s *tuiSink) events() <-chan tuiEvent { return s.ch }

func waitForEvent(ch <-chan tuiEvent) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// ---- 模型 ----

type approvalCard struct {
	toolUseID string
	tool      string
}

type tuiModel struct {
	opts     Options
	ctrl     sessionController
	events   <-chan tuiEvent // 只读消费侧
	sendCh   chan tuiEvent   // 换绑时把新会话的 sink 接回同一通道
	handle   *sessionHandle  // 当前会话（/new /resume 换绑；nil = 仅单测形态）
	selfID   string          // 状态栏展示的 mulix 会话 id
	width    int
	height   int
	lines    []string // transcript 渲染行
	input    string
	approval *approvalCard // 非 nil = 审批态（与渲染态互斥：composer 冻结）
	status   control.Snapshot
	running  bool // 回合进行中（异步 Submit/换绑——Update 循环保持响应）
	// testMode：单测形态——Update 不回挂阻塞型重挂命令（waitForEvent/Tick，
	// 单测直接以消息驱动）；submitCmd 仍正常返回（由测试辅助同步执行）。
	testMode bool
}

var (
	styleStatus = lipgloss.NewStyle().Faint(true)
	styleCard   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

func newTuiModel(o Options, ctrl sessionController, events <-chan tuiEvent, sessionID string) *tuiModel {
	return &tuiModel{
		opts:   o,
		ctrl:   ctrl,
		events: events,
		selfID: sessionID,
		width:  80, height: 24, // WindowSizeMsg 到达前的合理缺省
	}
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), statusTickCmd())
}

func statusTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return statusTick{} })
}

type statusTick struct{}

// typedInput 是文本注入消息（测试用它模拟整串输入；Update 按逐字符处理）。
type typedInput string

// appendLine 追加 transcript 行（渲染层裁剪）。
func (m *tuiModel) appendLine(s string) {
	for _, l := range strings.Split(s, "\n") {
		m.lines = append(m.lines, l)
	}
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case statusTick:
		if m.ctrl != nil {
			m.status = m.ctrl.Snapshot()
		}
		if m.testMode {
			return m, nil
		}
		return m, statusTickCmd()
	case tuiEvent:
		m.handleEvent(msg.ev)
		if m.testMode {
			return m, nil
		}
		return m, waitForEvent(m.events)
	case submitDoneMsg:
		// 异步 Submit 回执（T28 评审 Critical-1）：回合收口，解锁 composer。
		m.running = false
		if msg.err != nil {
			m.appendLine("[提交失败] " + msg.err.Error())
		}
		return m, nil
	case swapDoneMsg:
		// 异步换绑回执（评审 Important-B/Critical-A）：换绑 ctrl + handle。
		m.running = false
		if msg.err != nil {
			m.handle = nil // 旧会话已在 goroutine 中收口
			m.appendLine("[" + msg.kind + "失败] " + msg.err.Error() + "（旧会话已收口；可重试 " + msg.kind + "）")
			return m, nil
		}
		m.handle = msg.h
		m.ctrl = msg.h.ctrl // Critical-A：换绑后控制面必须指向新控制器
		m.selfID = msg.h.id
		m.status = control.Snapshot{}
		m.approval = nil // 旧会话的审批卡作废（对旧桥的裁决路径已不存在）
		m.appendLine("[会话] 已切换 → " + msg.h.id)
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case typedInput:
		// 测试注入：整串文本按逐字符输入处理（审批态被 handleKey 忽略——
		// 与真实键序一致）。
		for _, r := range string(msg) {
			var cmd tea.Cmd
			_, cmd = m.handleKey(tea.KeyPressMsg{Text: string(r), Code: r})
			_ = cmd
		}
		return m, nil
	case tea.QuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

// handleEvent 事件路由：文本/通知入 transcript；审批请求置卡（互斥态）；
// 裁决回执清卡/留痕；回合收口与引擎退出留痕。
func (m *tuiModel) handleEvent(ev any) {
	switch e := ev.(type) {
	case event.AssistantTextEvent:
		m.appendLine(e.Text)
	case event.NoticeEvent:
		m.appendLine("[notice] " + e.Content)
	case event.ApprovalRequestEvent:
		m.approval = &approvalCard{toolUseID: e.ToolUseID, tool: e.Tool}
		m.appendLine(fmt.Sprintf("[审批] 工具 %s（%s）需要授权", e.Tool, shortID(e.ToolUseID)))
	case event.ApprovalResolvedEvent:
		if m.approval != nil && m.approval.toolUseID == e.ToolUseID {
			m.approval = nil
		}
		m.appendLine(fmt.Sprintf("[裁决] %s → %s/%s", shortID(e.ToolUseID), e.Decision, e.Source))
	case event.TurnDoneEvent:
		if e.IsError {
			m.appendLine("[回合失败] " + e.Subtype)
		} else {
			m.appendLine("[回合完成 t" + fmt.Sprint(e.Turn) + "]")
		}
	case event.EngineExitedEvent:
		m.appendLine("[引擎已退出] " + e.Reason)
	case event.UsageEvent:
		// 状态栏走 Snapshot 轮询；usage 事件不进 transcript
	}
}

// handleKey 键盘路由。审批态只响应审批键（审批态/渲染态互斥——composer 冻结，
// 输入与提交都被忽略）+ Ctrl+C 逃生（评审 Minor-4：审批态不可困死用户）；
// 渲染态输入/提交/命令。
func (m *tuiModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == 'c' && msg.Mod&tea.ModCtrl != 0 { // Contains(0) 恒真——不能用作零 Mod 判定
		return m, tea.Quit
	}
	if m.approval != nil {
		switch {
		case msg.Code == 'a':
			m.resolve(m.approval.toolUseID, true, false)
		case msg.Code == 's':
			m.resolve(m.approval.toolUseID, true, true)
		case msg.Code == 'd' || msg.Code == tea.KeyEscape:
			m.resolve(m.approval.toolUseID, false, false)
		}
		return m, nil
	}
	switch {
	case msg.Code == tea.KeyEnter:
		return m.submit()
	case msg.Code == tea.KeyBackspace || msg.Code == tea.KeyDelete:
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	case msg.Text != "" && msg.Mod == 0:
		m.input += msg.Text
		return m, nil
	}
	return m, nil
}

// resolve 执行审批动作并立即清卡（回执事件随后到达时幂等跳过）。
func (m *tuiModel) resolve(toolUseID string, allow, session bool) {
	var err error
	if session {
		err = m.ctrl.ApproveSession(toolUseID, "user")
	} else {
		err = m.ctrl.Approve(toolUseID, allow, "user")
	}
	if err != nil {
		m.appendLine("[审批失败] " + err.Error())
	}
	m.approval = nil
}

// submit 渲染态 Enter：/命令分流（/cache /doctor /rewind /new /resume /quit）；
// 其余提交为回合输入。
// 异步提交（T28 评审 Critical-1）：Submit 同步等待回合收口，若在 Update 内
// 直调则事件循环冻结——审批卡不可达（gate 超时 120s 后自动拒绝）、transcript
// 不流式、事件通道可能填满死锁。改为 tea.Cmd goroutine，submitDoneMsg 回执；
// running 期间 Enter 被门禁。
func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	if m.running { // 门禁在清空 input 之前——忙期按键不吞文本
		m.appendLine("[忙] 回合进行中，等待收口…")
		return m, nil
	}
	text := strings.TrimSpace(m.input)
	m.input = ""
	if strings.HasPrefix(text, "/") {
		return m, m.slash(text)
	}
	if text == "" {
		return m, nil
	}
	m.running = true
	return m, submitCmd(m.ctrl, text)
}

type submitDoneMsg struct{ err error }

// swapDoneMsg 是异步会话换绑回执（h 为新会话；err 非 nil 时 h 为 nil）。
type swapDoneMsg struct {
	h    *sessionHandle
	err  error
	kind string // "new" | "resume"
}

func submitCmd(ctrl sessionController, text string) tea.Cmd {
	return func() tea.Msg {
		return submitDoneMsg{err: ctrl.Submit(context.Background(), text)}
	}
}

// slash 是 `/` 命令分发（brief：/new /resume /cache /doctor /rewind）。
// 返回 tea.Quit（/quit）或 nil。
func (m *tuiModel) slash(line string) tea.Cmd {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/quit":
		return tea.Quit
	case "/cache":
		rep, err := cache.ReadUsageReport(m.opts.UsageRoot, m.selfID)
		if err != nil {
			m.appendLine("[cache] " + err.Error())
			return nil
		}
		m.appendLine(fmt.Sprintf("[cache] 回合 %d | 命中率 %.1f%% | 成本 $%.4f | 节省 $%.4f | 冷回合 %d",
			rep.Turns, rep.SessionHit*100, rep.Cost, rep.Saving, rep.ColdTurns))
	case "/doctor":
		m.appendLine("[doctor] 自检项在 Task 29 落地")
	case "/rewind":
		m.appendLine("[rewind] 引擎 checkpoint 透传随 T32 真实引擎联调落地")
	case "/new":
		return m.swapSession("")
	case "/resume":
		if len(fields) < 2 {
			m.appendLine("[resume] 用法：/resume <引擎 session id>（无参形态随会话选择器落地）")
			return nil
		}
		return m.swapSession(fields[1])
	default:
		m.appendLine("[命令] 未知命令 " + fields[0] + "（可用：/new /resume /cache /doctor /rewind /quit）")
	}
	return nil
}

// swapSession 异步换绑（/new //resume 复用装配路径 openSession）。评审两轮
// 结论的合成：T28 首轮 Critical-1（Update 内同步等待冻结事件循环）+ 复审
// Important-B（old.close + 引擎启动是引擎时长级等待，同样不得阻塞 Update，
// 且阻塞期 Ctrl+C 无法触达）——故在 tea.Cmd goroutine 执行，swapDoneMsg 回执；
// 期间复用 running 忙门禁。次序：旧会话先收口（Close 阻塞至泵退出——残响
// 全部入通道），排空通道丢弃旧残响，再开新会话——新 transcript 不受旧事件
// 污染；openSession 失败时旧会话已不可恢复（handle 置 nil，UI 提示可重试）。
// 成功回执换绑 ctrl（复审 Critical-A：不换绑则后续 Submit/审批打在已关的
// 旧控制器上——ErrNoSession / 审批 120s 超时）。
func (m *tuiModel) swapSession(resumeID string) tea.Cmd {
	kind := "new"
	if resumeID != "" {
		kind = "resume"
	}
	old := m.handle
	m.running = true // 换绑期间门禁（复用忙语义；swapDoneMsg 清除）
	return func() tea.Msg {
		if old != nil {
			old.close()
		}
		for drained := true; drained; {
			select {
			case <-m.sendCh: // 丢弃旧会话残响
			default:
				drained = false
			}
		}
		h, _, err := openSession(m.opts, resumeID, true, channelSink{ch: m.sendCh})
		return swapDoneMsg{h: h, err: err, kind: kind}
	}
}

// View 组装：transcript（尾部裁剪）+ 审批卡或 composer + 状态栏。
func (m *tuiModel) View() tea.View {
	var b strings.Builder
	maxLines := m.height - 3 // 状态栏 + composer + 间隔
	if m.approval != nil {
		maxLines -= 4 // 审批卡高度
	}
	if maxLines < 1 {
		maxLines = 1
	}
	lines := m.lines
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for _, l := range lines {
		b.WriteString(l)
		b.WriteString("\n")
	}
	if m.approval != nil {
		card := fmt.Sprintf("工具 %s（%s）等待授权\n[a] 本次允许  [s] 本会话允许  [d] 拒绝",
			m.approval.tool, shortID(m.approval.toolUseID))
		b.WriteString(styleCard.Render(card))
		b.WriteString("\n")
	} else {
		b.WriteString("> " + m.input)
		b.WriteString("\n")
	}
	b.WriteString(styleStatus.Render(m.statusLine()))
	return tea.NewView(b.String())
}

// statusLine 状态栏（brief：模型 | 会话命中率 | 本回合 tokens | 成本；
// expected_cold 标注）。
func (m *tuiModel) statusLine() string {
	rep := m.status.MeterReport
	cold := ""
	if rep.ColdTurns > 0 {
		cold = fmt.Sprintf(" | ❄冷回合 %d", rep.ColdTurns)
	}
	turn := m.status.TurnTokens
	tokens := fmt.Sprintf("回合 t%d %d tok", m.status.Turn, turn.InputTokens+turn.CacheWriteTokens+turn.CacheReadTokens+turn.OutputTokens)
	running := "空闲"
	if m.status.Running {
		running = "运行中"
	}
	return fmt.Sprintf("%s | 会话 %s | 命中率 %.1f%% | %s | 成本 $%.4f%s | %s",
		m.status.Model, shortID(m.selfID), rep.SessionHit*100, tokens, rep.Cost, cold, running)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}

// RunTUI 生产 TUI 入口（root 默认命令；attended 会话）。
func RunTUI(ctx context.Context, o Options, resumeID string) error {
	o = withDefaults(o)
	sink := newTuiSink()
	h, _, err := openSession(o, resumeID, true, sink)
	if err != nil {
		return err
	}
	m := newTuiModel(o, h.ctrl, sink.events(), h.id)
	m.handle = h
	m.sendCh = sink.ch
	_, err = tea.NewProgram(m).Run()
	// 收口「当前」会话而非启动时的 h：/new //resume 换绑后 m.handle 已指向
	// 新控制器——不收口则宿主不 Close（mcp-config 临时文件泄漏）。
	m.handle.close()
	return err
}
