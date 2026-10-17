package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"

	"mulix/internal/cache"
	"mulix/internal/control"
	"mulix/internal/event"
	"mulix/internal/subagent"
)

// ---- 测试替身 ----

// stubController 记录 TUI 对会话控制面的调用（状态机单测不跑 tea runtime）。
type stubController struct {
	submits       []string
	cancels       int         // Task 10 D10：Cancel 调用次数（Esc 取消阶梯「恰一次」断言）
	approves      [][3]string // id, allow, by
	sessionGrants [][2]string // id, by
	snap          control.Snapshot
	failSubmit    bool
	sub           *subagent.Manager // /agents 面板数据源（nil = 未启用）
}

func (s *stubController) Subagent() *subagent.Manager { return s.sub }

// Cancel 记录取消调用（SessionController 接口成员——Task 10 D10 扩面）。
func (s *stubController) Cancel(_ context.Context) error {
	s.cancels++
	return nil
}

func (s *stubController) Submit(_ context.Context, text string) error {
	if s.failSubmit {
		return errors.New("submit boom")
	}
	s.submits = append(s.submits, text)
	return nil
}
func (s *stubController) Approve(id string, allow bool, by string) error {
	s.approves = append(s.approves, [3]string{id, boolStr(allow), by})
	return nil
}
func (s *stubController) ApproveSession(id, by string) error {
	s.sessionGrants = append(s.sessionGrants, [2]string{id, by})
	return nil
}
func (s *stubController) Snapshot() control.Snapshot { return s.snap }

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// fakeSwapper 是换绑接缝替身：记录 CloseCurrent/Open 调用与入参；CloseCurrent
// 时向 sink 注入一条旧会话残响，Open 时清点通道残余——钉死 tui 换绑的
// close→drain→open 顺序与「同 sink 续流」。
type fakeSwapper struct {
	sink     *Sink // 与模型同一事件汇（注入残响/清点残余）
	closed   int
	opens    []string
	openSink control.Sink
	leftover int
	ctrl     SessionController
	id       string
	err      error
}

func (f *fakeSwapper) CloseCurrent() {
	f.closed++
	if f.sink != nil {
		f.sink.Publish(event.NoticeEvent{Content: "旧会话残响"}) // 模拟收尾 publish 入通道
	}
}

func (f *fakeSwapper) Open(resumeID string, sink control.Sink) (SessionController, string, error) {
	f.opens = append(f.opens, resumeID)
	f.openSink = sink
	if s, ok := sink.(*Sink); ok { // 清点残余：tui 必须已在 open 前排空
	loop:
		for {
			select {
			case <-s.Events():
				f.leftover++
			default:
				break loop
			}
		}
	}
	return f.ctrl, f.id, f.err
}

// newTestModel 构造带 stub 控制面的 TUI 模型。
func newTestModel(t *testing.T) (*tuiModel, *stubController, <-chan tuiEvent) {
	t.Helper()
	sink := NewSink()
	stub := &stubController{snap: control.Snapshot{
		SessionID: "s-x", Model: "fake-model", Turn: 3,
		TurnTokens:  cache.TurnUsage{InputTokens: 100, CacheReadTokens: 300, OutputTokens: 20},
		MeterReport: cache.Report{Turns: 4, SessionHit: 0.82, Cost: 1.25, ColdTurns: 1},
	}}
	m := newTuiModel(Options{UsageRoot: t.TempDir(), Sink: sink}, stub, sink.Events(), "s-x")
	m.testMode = true
	return m, stub, sink.Events()
}

func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: string(r), Code: r}
}

func update(t *testing.T, m *tuiModel, msg tea.Msg) *tuiModel {
	t.Helper()
	next, cmd := m.Update(msg)
	if cmd != nil {
		// 同步执行命令（testMode 下仅 submit/swap/agents-cancel cmd 会返回）
		//——回执续一拍。
		switch done := cmd().(type) {
		case submitDoneMsg:
			next, _ = next.Update(done)
		case swapDoneMsg:
			next, _ = next.Update(done)
		case agentsCancelDoneMsg:
			next, _ = next.Update(done)
		case cancelDoneMsg: // Task 10 D10：Esc 取消回执（同步执行——纪律同上）
			next, _ = next.Update(done)
		}
	}
	return next.(*tuiModel)
}

// ---- 用例 ----

// TestTranscriptStreaming pins：助手文本/通知事件流式追加进 transcript。
func TestTranscriptStreaming(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.AssistantTextEvent{Text: "hello there"}})
	m = update(t, m, tuiEvent{ev: event.NoticeEvent{Level: "warning", Content: "命中率告警"}})
	view := m.View().Content
	if !strings.Contains(view, "hello there") || !strings.Contains(view, "命中率告警") {
		t.Fatalf("view missing transcript lines:\n%s", view)
	}
}

// TestSubmitFlow pins：渲染态 Enter → 异步 Submit（测试辅助同步执行命令并
// 派发 submitDoneMsg——生产中该回执来自 goroutine）；/命令分流不走 Submit。
func TestSubmitFlow(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, keyPress('h'))
	m = update(t, m, keyPress('i'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 || stub.submits[0] != "hi" {
		t.Fatalf("submits = %v", stub.submits)
	}
	if m.running {
		t.Fatal("submit cycle completed synchronously in helper — running must be cleared")
	}
	// /命令不进 Submit
	m = update(t, m, typeStr(m, "/rewind"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 {
		t.Fatalf("slash command leaked to Submit: %v", stub.submits)
	}
	if !strings.Contains(m.View().Content, "checkpoint") {
		t.Fatal("/rewind must append notice line")
	}
}

// TestBusyGate pins（T28 评审 Critical-1 配套）：回合进行中（running=true，
// 生产中由异步 Submit 置位）Enter 被门禁（不重复提交）+ [忙] 提示；
// submitDoneMsg 解锁后恢复提交。
func TestBusyGate(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, keyPress('a'))
	m.running = true // 模拟异步提交在途（生产：submitCmd 置位，收口回执清除）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 0 {
		t.Fatalf("busy submit leaked: %v", stub.submits)
	}
	if !strings.Contains(m.View().Content, "[忙]") {
		t.Fatal("busy gate must append notice")
	}
	m = update(t, m, submitDoneMsg{})
	if m.running {
		t.Fatal("done must unlock")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 || stub.submits[0] != "a" {
		t.Fatalf("post-busy submit missing: %v", stub.submits)
	}
}

func typeStr(_ *tuiModel, s string) tea.Msg { return typedInput(s) }

// TestSlashCache pins：/cache 把跨进程权威汇总写进 transcript。种子直连
// cache.NewMeter 播种（不需要会话——tui 不得 import cli 的 openSession
// 装配路径，循环依赖）。
func TestSlashCache(t *testing.T) {
	root := t.TempDir()
	id := "tui-cache-01"
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: id,
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	if _, err := meter.Observe(cache.TurnUsage{InputTokens: 100, CacheReadTokens: 300, OutputTokens: 20}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := meter.Close(); err != nil { // 落盘并释放 usage 文件（Windows 句柄）
		t.Fatalf("meter close: %v", err)
	}
	sink := NewSink()
	stub := &stubController{}
	m := newTuiModel(Options{UsageRoot: root, Sink: sink}, stub, sink.Events(), id)
	m.testMode = true
	m = update(t, m, typeStr(m, "/cache"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View().Content
	// 025 FR-006：会话 id8 退出状态栏——/cache 报告以回合/命中率字段断言。
	if !strings.Contains(view, "回合") || !strings.Contains(view, "命中率") {
		t.Fatalf("view missing cache report:\n%s", view)
	}
}

// TestSlashNew pins（tui 半——接缝顺序）：/new 触发换绑接缝——CloseCurrent
// （关当前会话）→ 排空残响 → Open（同 sink 开新会话），且 ctrl/selfID 重绑
// （复审 Critical-A：不换绑则后续 Submit 打在已关的旧控制器上）。真实装配
// 语义由 cli 侧 TestCliSwapper 钉。
func TestSlashNew(t *testing.T) {
	m, _, _ := newTestModel(t)
	newCtrl := &stubController{}
	sw := &fakeSwapper{sink: m.sink, ctrl: newCtrl, id: "s-new-id-0001"}
	m.swapper = sw
	m = update(t, m, typeStr(m, "/new"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if sw.closed != 1 {
		t.Fatalf("CloseCurrent calls = %d, want 1", sw.closed)
	}
	if len(sw.opens) != 1 || sw.opens[0] != "" {
		t.Fatalf("opens = %v, want [\"\"]", sw.opens)
	}
	if sw.leftover != 0 {
		t.Fatalf("old-session residue not drained before open: %d", sw.leftover)
	}
	if sw.openSink != control.Sink(m.sink) {
		t.Fatal("open must receive the model's own sink（同一通道续流）")
	}
	if m.ctrl != SessionController(newCtrl) {
		t.Fatal("ctrl must be rebound to the new controller")
	}
	if m.selfID != "s-new-id-0001" {
		t.Fatalf("selfID = %q, want s-new-id-0001", m.selfID)
	}
	if m.swapping {
		t.Fatal("swap receipt must clear the busy latch")
	}
	// Critical-A 钉：提交必须打在新控制器上
	m = update(t, m, keyPress('h'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(newCtrl.submits) != 1 || newCtrl.submits[0] != "h" {
		t.Fatalf("submit after /new must reach the new controller: %v", newCtrl.submits)
	}
}

// TestSlashResume pins（tui 半）：/resume <engine-id> 携引擎 id 走同一换绑
// 接缝并重绑 ctrl/selfID（真实镜像续链装配由 cli 侧 TestCliSwapper 钉；孤儿
// 回合收口不变量由 control 的 TestResumeReconcilesOrphanTurn 钉）。
func TestSlashResume(t *testing.T) {
	m, _, _ := newTestModel(t)
	newCtrl := &stubController{}
	sw := &fakeSwapper{sink: m.sink, ctrl: newCtrl, id: "s-res"}
	m.swapper = sw
	m = update(t, m, typeStr(m, "/resume engine-res"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if sw.closed != 1 {
		t.Fatalf("CloseCurrent calls = %d, want 1", sw.closed)
	}
	if len(sw.opens) != 1 || sw.opens[0] != "engine-res" {
		t.Fatalf("opens = %v, want [engine-res]", sw.opens)
	}
	if sw.leftover != 0 {
		t.Fatalf("old-session residue not drained before open: %d", sw.leftover)
	}
	if m.ctrl != SessionController(newCtrl) {
		t.Fatal("ctrl must be rebound to the resumed controller")
	}
	if m.selfID != "s-res" {
		t.Fatalf("selfID = %q, want s-res", m.selfID)
	}
	if m.swapping {
		t.Fatal("swap receipt must clear the busy latch")
	}
}

// TestEventChannelBackpressure pins：sink 通道满时 Publish 阻塞背压（不丢事件
// ——镜像先行的对面是「前端可慢不可丢」）。
func TestEventChannelBackpressure(t *testing.T) {
	sink := NewSink()
	done := make(chan struct{})
	go func() {
		for i := 0; i < capTuiEventCh+1; i++ {
			sink.Publish(event.NoticeEvent{Seq: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("publish must block when channel full")
	case <-time.After(50 * time.Millisecond):
	}
	for i := 0; i < capTuiEventCh+1; i++ {
		<-sink.Events()
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher must finish after drain")
	}
}

// TestSidebarEnterSwapsSession pins（Task 13——侧栏聚焦 Enter 走 /resume 同一
// 换绑接缝）：选中项 EngineID 为 swapSession 实参（sw.opens 记录）；触发即游
// 标复位 + 焦点回主列（换绑异步——回执 swapDoneMsg 重绑 ctrl/selfID 由既有
// 路径收口）。
func TestSidebarEnterSwapsSession(t *testing.T) {
	m, _, _ := newTestModel(t)
	newCtrl := &stubController{}
	sw := &fakeSwapper{sink: m.sink, ctrl: newCtrl, id: "s-res-2"}
	m.swapper = sw
	m.selfID = "sess-cur"
	m.opts.ListSessions = func() []SessionSummary { return sidebarSessionsStub() }
	m.refreshSidebarSessions()
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24}) // 宽档（F6 守卫放行）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // sel → 1（e-2）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(sw.opens) != 1 || sw.opens[0] != "e-2" {
		t.Fatalf("sidebar Enter must swap to the selected EngineID: %v", sw.opens)
	}
	if m.sidebar.sel != 0 {
		t.Fatalf("sel must reset after swap trigger: %d", m.sidebar.sel)
	}
	if m.focus != focusMain {
		t.Fatal("focus must return to main after swap trigger")
	}
	if m.selfID != "s-res-2" {
		t.Fatalf("swap receipt must rebind selfID: %q", m.selfID)
	}
}
