package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/subagent"
)

// ---- 测试替身 ----

// stubController 记录 TUI 对会话控制面的调用（状态机单测不跑 tea runtime）。
type stubController struct {
	submits       []string
	approves      [][3]string // id, allow, by
	sessionGrants [][2]string // id, by
	snap          control.Snapshot
	failSubmit    bool
	sub           *subagent.Manager // /agents 面板数据源（nil = 未启用）
}

func (s *stubController) Subagent() *subagent.Manager { return s.sub }

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

// newTestModel 构造带 stub 控制面的 TUI 模型。
func newTestModel(t *testing.T) (*tuiModel, *stubController, <-chan tuiEvent) {
	t.Helper()
	sink := newTuiSink()
	stub := &stubController{snap: control.Snapshot{
		SessionID: "s-x", Model: "fake-model", Turn: 3,
		TurnTokens:  cache.TurnUsage{InputTokens: 100, CacheReadTokens: 300, OutputTokens: 20},
		MeterReport: cache.Report{Turns: 4, SessionHit: 0.82, Cost: 1.25, ColdTurns: 1},
	}}
	m := newTuiModel(Options{UsageRoot: t.TempDir()}, stub, sink.events(), "s-x")
	m.testMode = true
	return m, stub, sink.events()
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

// TestApprovalExclusiveState pins（brief：审批态/渲染态互斥）：审批卡可见时
// composer 输入被忽略（按键不进 input）、Enter 不提交；a/s/d 走审批动作并
// 清卡回渲染态。
func TestApprovalExclusiveState(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{
		Seq: 1, ToolUseID: "toolu-1", Tool: "Write",
	}})
	if m.approval == nil {
		t.Fatal("approval card must show on request")
	}
	if !strings.Contains(m.View().Content, "本次允许") {
		t.Fatal("approval card must render actions")
	}
	// 互斥：审批态下输入不进 composer
	before := m.input
	m = update(t, m, keyPress('x'))
	if m.input != before {
		t.Fatalf("input changed in approval state: %q", m.input)
	}
	// 互斥：审批态下 Enter 不提交
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 0 {
		t.Fatalf("submit called in approval state: %v", stub.submits)
	}
	// a = 本次允许
	m = update(t, m, keyPress('a'))
	if len(stub.approves) != 1 || stub.approves[0] != [3]string{"toolu-1", "true", "user"} {
		t.Fatalf("approves = %v", stub.approves)
	}
	if m.approval != nil {
		t.Fatal("card must clear after resolve")
	}
	// 回渲染态：输入恢复
	m = update(t, m, keyPress('y'))
	if m.input != "y" {
		t.Fatalf("input = %q, want y", m.input)
	}
}

// TestApprovalDenyAndSession pins：d=拒绝、Esc=拒绝（逃生等价）、s=本会话允许
// （brief 审批卡三动作）；Ctrl+C 在审批态可退出（Minor-4）。
func TestApprovalDenyAndSession(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t2", Tool: "Bash"}})
	m = update(t, m, keyPress('d'))
	if len(stub.approves) != 1 || stub.approves[0][1] != "false" {
		t.Fatalf("deny not recorded: %v", stub.approves)
	}
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t3", Tool: "Edit"}})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if len(stub.approves) != 2 || stub.approves[1][1] != "false" {
		t.Fatalf("esc deny not recorded: %v", stub.approves)
	}
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t4", Tool: "Write"}})
	m = update(t, m, keyPress('s'))
	if len(stub.sessionGrants) != 1 || stub.sessionGrants[0][0] != "t4" {
		t.Fatalf("session grant not recorded: %v", stub.sessionGrants)
	}
	// Ctrl+C 审批态逃生
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t5", Tool: "Bash"}})
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("Ctrl+C in approval state must quit")
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

// TestStatusBar pins：状态栏含模型 | 会话命中率 | 本回合 tokens | 成本；
// expected_cold 有冷回合时标注。
func TestStatusBar(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, statusTick{}) // 状态栏数据由 tick 拉快照
	view := m.View().Content
	for _, want := range []string{"fake-model", "82.0%", "420 tok", "$1.25", "冷"} {
		if !strings.Contains(view, want) {
			t.Fatalf("status bar missing %q:\n%s", want, view)
		}
	}
	// 无冷回合 → 无标注
	m, _, _ = newTestModel(t)
	m = update(t, m, statusTick{})
	m.status.MeterReport.ColdTurns = 0
	if strings.Contains(m.View().Content, "冷") {
		t.Fatal("cold marker must be absent without cold turns")
	}
}

// TestSlashCache pins：/cache 把跨进程权威汇总写进 transcript。
func TestSlashCache(t *testing.T) {
	root := t.TempDir()
	h := seedUsageSession(t, root)
	h.close() // meter 落盘并释放 usage 文件
	sink := newTuiSink()
	stub := &stubController{}
	m := newTuiModel(Options{UsageRoot: root}, stub, sink.events(), h.id)
	m = update(t, m, typeStr(m, "/cache"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View().Content
	if !strings.Contains(view, h.id[:8]) || !strings.Contains(view, "回合") {
		t.Fatalf("view missing cache report:\n%s", view)
	}
}

// TestSlashNew pins：/new 关闭当前会话并新建（handle 换绑 + ctrl 换绑——复审
// Critical-A：不换绑则后续 Submit 打在已关的旧控制器上）。
func TestSlashNew(t *testing.T) {
	opts := Options{Workspace: t.TempDir(), SessionsRoot: t.TempDir(), UsageRoot: t.TempDir(),
		Config: testConfig(), HostFactory: fakeHostFactory(t)}
	h := mustOpenSession(t, opts, "", nil)
	oldID := h.id
	sink := newTuiSink()
	stub := &stubController{}
	m := newTuiModel(opts, stub, sink.events(), h.id)
	m.handle = h
	m.sendCh = sink.ch
	m = update(t, m, typeStr(m, "/new"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.handle == nil || m.handle.id == oldID {
		t.Fatalf("handle not swapped: %+v", m.handle)
	}
	if m.handle.id == "" {
		t.Fatal("new session id empty")
	}
	// Critical-A 钉：ctrl 必须换绑到新控制器（不再是 stub）——且可提交
	if _, isStub := m.ctrl.(*stubController); isStub {
		t.Fatal("ctrl must be rebound to the new controller")
	}
	m = update(t, m, keyPress('h'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(m.View().Content, "提交失败") {
		t.Fatal("submit after /new must reach the new controller")
	}
	m.handle.close()
}

// TestSlashResume pins：/resume <engine-id> 续链对应镜像（handle + ctrl 换绑；
// 孤儿回合收口不变量由 control 的 TestResumeReconcilesOrphanTurn 钉）。
func TestSlashResume(t *testing.T) {
	opts := Options{Workspace: "w", SessionsRoot: t.TempDir(), UsageRoot: t.TempDir(),
		Config: testConfig(), HostFactory: fakeHostFactory(t)}
	seed := seedSession(t, opts.SessionsRoot, "s-res", "engine-res")
	defer seed.Close()
	h := mustOpenSession(t, opts, "", nil)
	sink := newTuiSink()
	stub := &stubController{}
	m := newTuiModel(opts, stub, sink.events(), h.id)
	m.handle = h
	m.sendCh = sink.ch
	m = update(t, m, typeStr(m, "/resume engine-res"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.handle == nil || !strings.HasSuffix(m.handle.id, "s-res") {
		t.Fatalf("handle not resumed: %+v", m.handle)
	}
	if _, isStub := m.ctrl.(*stubController); isStub {
		t.Fatal("ctrl must be rebound to the resumed controller")
	}
	m.handle.close()
}

// TestEventChannelBackpressure pins：sink 通道满时 Publish 阻塞背压（不丢事件
// ——镜像先行的对面是「前端可慢不可丢」）。
func TestEventChannelBackpressure(t *testing.T) {
	sink := newTuiSink()
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
		<-sink.events()
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publisher must finish after drain")
	}
}

// ---- 真实装配辅助 ----

func testConfig() *config.Config {
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		panic(err)
	}
	return cfg
}

// fakeHostFactory 注入 FakeHost（happy-turn——握手/回合/收口全绿的默认脚本）。
func fakeHostFactory(t *testing.T) func(engine.BootConfig) (engine.Host, error) {
	t.Helper()
	script, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return func(engine.BootConfig) (engine.Host, error) { return engine.NewFakeHost(script), nil }
}

func mustOpenSession(t *testing.T, o Options, resumeID string, sink control.Sink) *sessionHandle {
	t.Helper()
	if o.HostFactory == nil {
		o.HostFactory = fakeHostFactory(t)
	}
	if sink == nil {
		sink = discardSink{}
	}
	h, code, err := openSession(o, resumeID, true, sink)
	if err != nil || code != 0 {
		t.Fatalf("open session: code=%d err=%v", code, err)
	}
	return h
}

func seedUsageSession(t *testing.T, usageRoot string) *sessionHandle {
	t.Helper()
	o := Options{Workspace: t.TempDir(), SessionsRoot: t.TempDir(), UsageRoot: usageRoot, Config: testConfig()}
	h := mustOpenSession(t, o, "", nil)
	if _, err := h.meter.Observe(cache.TurnUsage{InputTokens: 100, CacheReadTokens: 300, OutputTokens: 20}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	return h
}

// discardSink 丢弃事件（装配辅助）。
type discardSink struct{}

func (discardSink) Publish(any) {}
