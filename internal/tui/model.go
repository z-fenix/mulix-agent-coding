package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"mulix/internal/cache"
	"mulix/internal/control"
	"mulix/internal/event"
	"mulix/internal/subagent"
)

// Options 是 TUI 的窄配置面（组合根注入）：只枚举模型实际消费点——
// /cache 汇总根、/doctor 预合成回调、事件汇；禁止搬整个 cli.Options
// （doctor 探针/检查清单为 cli 未导出，经 Doctor 闭包预合成注入）。
type Options struct {
	// UsageRoot 是 /cache 汇总读取根（跨进程权威口径 cache.ReadUsageReport）。
	UsageRoot string
	// Doctor 是 /doctor 的预合成回调（cli 注入；返回逐行输出——探针注入面与
	// CLI doctor 一致）。nil = 提示未注入（生产组合根必注入）。
	Doctor func() []string
	// Sink 是事件汇（组合根创建并已绑定初始会话；必须与 Run 的 events 参数
	// 同源）。换绑时模型以它供给 SessionSwapper.Open——新会话事件续流同一通道。
	Sink *Sink
}

// Run 生产 TUI 主循环（组合根入口：cli 装配初始会话、换绑器与事件汇后注入）。
// ctrl/selfID 为初始会话的控制面与 mulix 会话 id；swapper 供 /new /resume
// 换绑。退出后由组合根收口「当前」会话（模型不持句柄——换绑后句柄归 swapper）。
func Run(o Options, ctrl SessionController, selfID string, swapper SessionSwapper, events <-chan tuiEvent) error {
	m := newTuiModel(o, ctrl, events, selfID)
	m.swapper = swapper
	_, err := tea.NewProgram(m).Run()
	return err
}

// ---- 模型 ----

type approvalCard struct {
	toolUseID string
	tool      string
}

type tuiModel struct {
	opts     Options
	ctrl     SessionController
	events   <-chan tuiEvent // 只读消费侧
	sink     *Sink           // 事件汇本体（与 events 同一通道；换绑时供给 Open）
	swapper  SessionSwapper  // 会话换绑接缝（cli 实现；nil = 未接线，防御性拒绝换绑）
	selfID   string          // 状态栏展示的 mulix 会话 id
	width    int
	height   int
	input    string
	approval *approvalCard // 非 nil = 审批态（与渲染态互斥：composer 冻结）
	status   control.Snapshot
	running  bool // 回合进行中（异步 Submit——Update 循环保持响应）
	swapping bool // 换绑进行中（/new /resume 异步——与 running 同门禁语义）
	// /agents 面板（M12/T12.4）：列表态独占 composer；agentsSel 是选中行。
	agentsOpen bool
	agentsSel  int
	// 关注点子结构（D4 骨架）：transcriptState 已于 FR-001 落位（语义块 +
	// viewport）；其余字段随后续任务逐块从上方平铺字段迁入——迁移完成前
	// 平铺字段仍是权威（见各 struct 注释）。
	transcriptState transcriptState
	scrollState     scrollState
	composerState   composerState
	approvalState   approvalState
	statusState     statusState
	themeState      themeState
	// testMode：单测形态——Update 不回挂阻塞型重挂命令（waitForEvent/Tick，
	// 单测直接以消息驱动）；submitCmd 仍正常返回（由测试辅助同步执行）。
	testMode bool
}

var (
	styleStatus = lipgloss.NewStyle().Faint(true)
	styleCard   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// ---- 关注点子结构（D4 骨架）----
//
// 根模型按渲染关注点的拆分容器：transcriptState 已落位（transcript.go）、
// scrollState 已落位（scrollstate.go——尾随状态机 023 D5），其余仅空结构，
// 字段随后续任务从 tuiModel 平铺平移迁入（迁移完成前 tuiModel 平铺字段仍是
// 权威）。
type composerState struct{} // 输入行（input）
type approvalState struct{} // 审批互斥态（approval 卡）
type statusState struct{}   // 状态栏快照（status）与 busy 门禁
type themeState struct{}    // 样式与配色（styleStatus/styleCard）

func newTuiModel(o Options, ctrl SessionController, events <-chan tuiEvent, sessionID string) *tuiModel {
	return &tuiModel{
		opts:   o,
		ctrl:   ctrl,
		events: events,
		sink:   o.Sink,
		selfID: sessionID,
		width:  80, height: 24, // WindowSizeMsg 到达前的合理缺省
		transcriptState: newTranscriptState(),
	}
}

// maxEventDrain 封顶一次 Update 内合并消费的事件数（含首条；对照参考
// DeepSeek-Reasonix chat_tui.go:501 / turn_lifecycle.go:147——参考的 512
// 计首条后的缓冲量，本仓口径含首条，以 TestDrainCoalescing 钉死）：一次
// re-wrap 覆盖整批而不是每事件一条命令——洪峰下 Update 次数以常数封顶。
const maxEventDrain = 512

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), statusTickCmd())
}

func statusTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return statusTick{} })
}

type statusTick struct{}

// typedInput 是文本注入消息（测试用它模拟整串输入；Update 按逐字符处理）。
type typedInput string

// appendLine 追加系统行（notice/turn/审批留痕等）——一行一块包装为 plainBlock
// （块模型迁移：语义行进块流，渲染由 transcriptState 按宽收口）。
func (m *tuiModel) appendLine(s string) {
	m.transcriptState.append(plainBlock{text: s})
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
		m.drainEvents(msg)
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
		// 异步换绑回执（评审 Important-B/Critical-A）：换绑 ctrl + selfID。
		m.running = false
		m.swapping = false
		if msg.err != nil {
			m.appendLine("[" + msg.kind + "失败] " + msg.err.Error() + "（旧会话已收口；可重试 " + msg.kind + "）")
			return m, nil
		}
		m.ctrl = msg.ctrl // Critical-A：换绑后控制面必须指向新控制器
		m.selfID = msg.id
		m.status = control.Snapshot{}
		m.approval = nil // 旧会话的审批卡作废（对旧桥的裁决路径已不存在）
		// 会话切换单帧强制置底（设计 §7.2：回底恢复跟随；对照参考
		// resetFreshContextView chat_tui.go:4171 的 forceGotoBottom 武装）。
		m.scrollState.forceGotoBottom = true
		m.appendLine("[会话] 已切换 → " + msg.id)
		return m, nil
	case doctorDoneMsg:
		for _, l := range msg.lines {
			m.appendLine(l)
		}
		return m, nil
	case agentsCancelDoneMsg:
		// /agents 面板取消回执（异步）：失败留痕；成功状态由列表刷新呈现
		//（mgr.List() 以 cancelled 终态渲染）。
		if msg.err != nil {
			m.appendLine("[agents] 取消失败: " + msg.err.Error())
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseWheelMsg:
		// 滚轮手势（023 D5）：模态独占态不泄漏，渲染态滚转写并归位跟随态
		//（scrollstate.go handleWheel——MouseMode 经 View 声明启用上报）。
		m.handleWheel(msg)
		return m, nil
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

// drainEvents 事件合并消费（D4 洪峰合并，对照参考 turn_lifecycle.go:147）：
// 处理首条后，非阻塞排水通道中已缓冲的事件（首条计入预算——一次 Update
// 合并消费至多 maxEventDrain 条），随后统一 finalize() 收口——一次 re-wrap
// 覆盖整批而不是每事件一条命令。超出上限的余量留通道，由后续 Update 继续
// 消费（不丢）。
func (m *tuiModel) drainEvents(first tuiEvent) {
	m.handleEvent(first.ev)
	for i := 1; i < maxEventDrain; i++ {
		select {
		case ev := <-m.events:
			m.handleEvent(ev.ev)
		default:
			m.finalize()
			return
		}
	}
	m.finalize()
}

// finalize 一批合并消费的统一收口：换行缓存 sync（增量——只重排后缀块，
// 宽度变更内部自动全量重建），行列表有变才克隆喂给 viewport；钉底由尾随
// 状态机裁决（023 D5）——仅跟随态（或单帧 forceGotoBottom，经
// shouldFollowTail）GotoBottom，上翻锁定下视口不动——一次 re-wrap 覆盖整批
// 而不是每事件一次（对照参考 chat_tui.go:989-1012 的 sync+feed+钉底收口）。
func (m *tuiModel) finalize() {
	if m.transcriptState.syncWrappedLines(transcriptContentWidth(m.width), false) {
		m.transcriptState.feedViewportContent()
		if m.shouldFollowTail() {
			m.transcriptState.vp.GotoBottom()
		}
	}
}

// handleEvent 事件路由：文本/通知入 transcript；审批请求置卡（互斥态）；
// 裁决回执清卡/留痕；回合收口与引擎退出留痕。
func (m *tuiModel) handleEvent(ev any) {
	switch e := ev.(type) {
	case event.AssistantTextEvent:
		// 助手文本一事件一块（Task 4 纯文本渲染；Task 7 换 markdown——届时
		// 流式改写走 rewriteLast）。
		m.transcriptState.append(assistantBlock{text: e.Text})
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
	if m.agentsOpen { // /agents 列表态独占 composer（与审批态同级互斥）
		return m.handleAgentsKey(msg)
	}
	// 转写滚动手势（023 D5）：审批/agents 独占态在上方分支拦截——治理独占期
	// 按键不泄漏（D10/D11；对照参考 chat_tui.go:1316-1333 在任意状态可滚动，
	// 本仓治理独占语义优先）。PgUp/PgDn/Ctrl+Home/Ctrl+End 均非文本键。
	switch {
	case msg.Code == tea.KeyPgUp:
		m.transcriptState.vp.PageUp()
		m.syncScrollModeAfterGesture()
		return m, nil
	case msg.Code == tea.KeyPgDown:
		m.transcriptState.vp.PageDown()
		m.syncScrollModeAfterGesture()
		return m, nil
	case msg.Code == tea.KeyHome && msg.Mod&tea.ModCtrl != 0: // 顶格——显式滚离
		m.transcriptState.vp.GotoTop()
		m.markUserScrolled()
		return m, nil
	case msg.Code == tea.KeyEnd && msg.Mod&tea.ModCtrl != 0: // 回底——恢复跟随
		m.transcriptState.vp.GotoBottom()
		m.markFollowTail()
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
// running/swapping 期间 Enter 被门禁。
func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	if m.running || m.swapping { // 门禁在清空 input 之前——忙期按键不吞文本
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

// swapDoneMsg 是异步会话换绑回执（ctrl/id 为新会话；err 非 nil 时为空）。
type swapDoneMsg struct {
	ctrl SessionController
	id   string
	err  error
	kind string // "new" | "resume"
}

func submitCmd(ctrl SessionController, text string) tea.Cmd {
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
		// T29 接线：与 CLI doctor 共用同一检查清单（预合成回调经 Options.Doctor
		// 注入——探针为 cli 未导出）。异步（T29 评审 I-1）：claude --version 是
		// 引擎时长级子进程——同步在 Update 内会冻结事件循环（与 T28 Critical-1
		// 同类）。
		return m.doctorCmd()
	case "/rewind":
		m.appendLine("[rewind] 引擎 checkpoint 透传随 T32 真实引擎联调落地")
	case "/agents":
		return m.openAgentsPanel()
	case "/new":
		return m.swapSession("")
	case "/resume":
		if len(fields) < 2 {
			m.appendLine("[resume] 用法：/resume <引擎 session id>（无参形态随会话选择器落地）")
			return nil
		}
		return m.swapSession(fields[1])
	default:
		m.appendLine("[命令] 未知命令 " + fields[0] + "（可用：/new /resume /cache /doctor /agents /rewind /quit）")
	}
	return nil
}

// ---- /agents 面板（M12/T12.4：子代理列表视图 + c 取消） ----

// openAgentsPanel 打开 /agents 面板：子会话 id/状态/任务/深度 列表视图。
// 编排器未接线（ctrl.Subagent() == nil）→ 命令提示未启用，不进入面板态。
func (m *tuiModel) openAgentsPanel() tea.Cmd {
	if m.ctrl == nil {
		m.appendLine("[agents] 子代理未启用")
		return nil
	}
	if m.ctrl.Subagent() == nil {
		m.appendLine("[agents] 子代理未启用（会话未接线子代理工厂）")
		return nil
	}
	m.agentsOpen = true
	m.agentsSel = 0
	return nil
}

// handleAgentsKey /agents 面板键位（列表态独占 composer）：↑/↓ 选择、c 取消
// 选中运行中子代理（异步——T28 Critical-1 同款，Update 内不阻塞）、esc/q
// 关闭。审批卡仍优先（handleKey 前置分支）。
func (m *tuiModel) handleAgentsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Code == tea.KeyEscape || msg.Code == 'q':
		m.agentsOpen = false
		return m, nil
	case msg.Code == tea.KeyUp:
		if m.agentsSel > 0 {
			m.agentsSel--
		}
	case msg.Code == tea.KeyDown:
		m.agentsSel++
	case msg.Code == 'c':
		mgr := m.ctrl.Subagent()
		if mgr == nil { // 面板打开期间编排器不会消失；防御性收口
			m.agentsOpen = false
			return m, nil
		}
		infos := mgr.List()
		if len(infos) == 0 {
			return m, nil
		}
		if m.agentsSel >= len(infos) {
			m.agentsSel = len(infos) - 1
		}
		sel := infos[m.agentsSel]
		if sel.Status != subagent.StatusRunning {
			m.appendLine("[agents] 仅运行中的子代理可取消（当前 " + string(sel.Status) + "）")
			return m, nil
		}
		return m, agentsCancelCmd(mgr, sel.SubSessionID)
	}
	return m, nil
}

// agentsCancelDoneMsg 是 /agents 面板的取消回执（异步；成功态由列表刷新呈现）。
type agentsCancelDoneMsg struct{ err error }

// agentsCancelCmd 异步取消一个子代理（用户经面板触发）。
func agentsCancelCmd(mgr *subagent.Manager, subID string) tea.Cmd {
	return func() tea.Msg {
		return agentsCancelDoneMsg{err: mgr.Cancel(context.Background(), subID, "user:/agents")}
	}
}

// agentsPanelView 渲染 /agents 面板（列表：id | 状态 | 任务 | 深度；选中行
// 标记；无子代理给空态）。数据源 = mgr.List() 实时快照。
func (m *tuiModel) agentsPanelView() string {
	var b strings.Builder
	mgr := m.ctrl.Subagent()
	if mgr == nil {
		return "子代理未启用\n[esc] 关闭\n"
	}
	infos := mgr.List()
	if len(infos) == 0 {
		return "暂无子代理\n[↑/↓] 选择  [c] 取消  [esc] 关闭\n"
	}
	if m.agentsSel >= len(infos) {
		m.agentsSel = len(infos) - 1
	}
	if m.agentsSel < 0 {
		m.agentsSel = 0
	}
	b.WriteString("子代理（ID | 状态 | 任务 | 深度）\n")
	for i, in := range infos {
		marker := "  "
		if i == m.agentsSel {
			marker = "▸ "
		}
		b.WriteString(fmt.Sprintf("%s%-14s | %-9s | %s | 深度 %d\n",
			marker, shortID(in.SubSessionID), string(in.Status), in.Task, in.Depth))
	}
	b.WriteString("[↑/↓] 选择  [c] 取消选中  [esc] 关闭\n")
	return b.String()
}

// doctorCmd 异步执行 doctor 检查（T29 评审 I-1）——回执 doctorDoneMsg。
// 检查清单经 Options.Doctor 预合成回调注入（探针为 cli 未导出）。
func (m *tuiModel) doctorCmd() tea.Cmd {
	return func() tea.Msg {
		if m.opts.Doctor == nil {
			return doctorDoneMsg{lines: []string{"[doctor] 检查回调未注入"}}
		}
		return doctorDoneMsg{lines: m.opts.Doctor()}
	}
}

type doctorDoneMsg struct{ lines []string }

// swapSession 异步换绑（/new //resume 复用换绑接缝 SessionSwapper）。评审
// 两轮结论的合成：T28 首轮 Critical-1（Update 内同步等待冻结事件循环）+
// 复审 Important-B（old.close + 引擎启动是引擎时长级等待，同样不得阻塞
// Update，且阻塞期 Ctrl+C 无法触达）——故在 tea.Cmd goroutine 执行，
// swapDoneMsg 回执；期间 swapping 忙门禁。次序钉死：旧会话先收口
// （CloseCurrent 阻塞至泵退出——残响全部入通道），排空通道丢弃旧残响
// （通道归 tui 所有），再开新会话——新 transcript 不受旧事件污染；Open
// 失败时旧会话已不可恢复（swapper 侧 cur 置 nil，UI 提示可重试）。成功
// 回执换绑 ctrl（复审 Critical-A：不换绑则后续 Submit/审批打在已关的旧
// 控制器上——ErrNoSession / 审批 120s 超时）。
func (m *tuiModel) swapSession(resumeID string) tea.Cmd {
	kind := "new"
	if resumeID != "" {
		kind = "resume"
	}
	m.swapping = true // 换绑期间门禁（swapDoneMsg 清除）
	return func() tea.Msg {
		if m.swapper == nil { // 未接线换绑器——防御性失败回执（生产组合根必接线）
			return swapDoneMsg{err: errors.New("会话换绑未接线（SessionSwapper 缺席）"), kind: kind}
		}
		m.swapper.CloseCurrent() // 旧会话收尾 publish 全部入通道
		for drained := true; drained; {
			select {
			case <-m.events: // 排空旧会话残响
			default:
				drained = false
			}
		}
		ctrl, id, err := m.swapper.Open(resumeID, m.sink) // 同 sink 续流
		return swapDoneMsg{ctrl: ctrl, id: id, err: err, kind: kind}
	}
}

// View 帧合成（FR-001 alt-screen 全接管）：frame() = 转写视口 + 底部固定区
// （模态面板/授权卡/编辑器 + 状态行）。AltScreen 以 View 字段声明——bubbletea
// v2 无 WithAltScreen 程序选项（v1 API），按 v2 升级指南等价替换（brief 形态
// 偏差 Ruling，见任务报告）。
func (m *tuiModel) View() tea.View {
	v := tea.NewView(m.frame())
	v.AltScreen = true
	// 滚轮/点击上报（023 D5 滚动键）；拖拽选区随 Task 21 升 MouseModeAllMotion。
	v.MouseMode = tea.MouseModeCellMotion
	return v
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
