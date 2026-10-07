package control

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/sessionx"
)

// ---- 测试基建 ----

// recordingSink 记录 Sink 收到的事件（并发安全）。
type recordingSink struct {
	mu     sync.Mutex
	events []any
}

func (s *recordingSink) Publish(ev any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *recordingSink) all() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]any(nil), s.events...)
}

// gateSink 在首个 AssistantTextEvent 时阻塞（制造「回合进行中」的确定性窗口）。
type gateSink struct {
	recordingSink
	release chan struct{}
	blocked chan struct{}
	fired   bool
}

func (s *gateSink) Publish(ev any) {
	if _, ok := ev.(event.AssistantTextEvent); ok && !s.fired {
		s.fired = true
		close(s.blocked)
		<-s.release
	}
	s.recordingSink.Publish(ev)
}

func newTestController(t *testing.T, host engine.Host, sink Sink) *Controller {
	t.Helper()
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{
		ID: "s-test", Workspace: "w", Model: "m", PinnedModel: "m",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root:      root,
		SessionID: "s-test",
		Pricing:   cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	t.Cleanup(func() { meter.Close() })
	policy, err := guard.NewPolicy(guard.PresetDefault, nil)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}
	c, err := NewController(Options{
		HostFactory: func(engine.BootConfig) (engine.Host, error) { return host, nil },
		Sink:        sink,
		Store:       store,
		Meter:       meter,
		Gate:        gate,
		Config:      mustDefaultConfig(),
		Workspace:   "w",
	})
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func mustDefaultConfig() config.Config {
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		panic(err)
	}
	return *cfg
}

// policyRuleIn 是测试里构造策略规则的轻量描述（guard.ParseRule + 效果）。
type policyRuleIn struct {
	raw    string
	effect guard.Decision
}

// newTestControllerWithPolicyRules 构造无人值守控制器（可带 allow/deny 规则；
// default 预设下未命中规则的工具 = Ask → fail-closed 拒绝）。
func newTestControllerWithPolicyRules(t *testing.T, host engine.Host, sink Sink, rules ...policyRuleIn) *Controller {
	t.Helper()
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{
		ID: "s-test", Workspace: "w", Model: "m", PinnedModel: "m",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: "s-test",
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	t.Cleanup(func() { meter.Close() })
	var grs []guard.Rule
	for _, r := range rules {
		gr, err := guard.ParseRule(r.raw)
		if err != nil {
			t.Fatalf("parse rule %q: %v", r.raw, err)
		}
		gr.Effect = r.effect
		grs = append(grs, gr)
	}
	policy, err := guard.NewPolicy(guard.PresetDefault, grs)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}
	c, err := NewController(Options{
		HostFactory: func(engine.BootConfig) (engine.Host, error) { return host, nil },
		Sink:        sink, Store: store, Meter: meter, Gate: gate,
		Config: mustDefaultConfig(), Workspace: "w",
	})
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// newTestControllerAttended 构造 attended 控制器：审批桥接 Approve()，
// Gate 窗口=timeout。
func newTestControllerAttended(t *testing.T, host engine.Host, sink Sink, timeout time.Duration) *Controller {
	t.Helper()
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{
		ID: "s-test", Workspace: "w", Model: "m", PinnedModel: "m",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: "s-test",
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	t.Cleanup(func() { meter.Close() })
	policy, err := guard.NewPolicy(guard.PresetDefault, nil)
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	bridge := NewUserApprovalBridge()
	gate, err := guard.NewGate(guard.GateOptions{
		Policy: policy, Approver: bridge, Timeout: timeout,
	})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}
	c, err := NewController(Options{
		HostFactory: func(engine.BootConfig) (engine.Host, error) { return host, nil },
		Sink:        sink, Store: store, Meter: meter, Gate: gate,
		Config: mustDefaultConfig(), Workspace: "w", Approvals: bridge,
	})
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func fixture(t *testing.T, name string) engine.FakeScript {
	t.Helper()
	script, err := engine.FixtureScript(filepath.Join("..", "..", "fixtures", "engine", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return script
}

// ---- 用例 ----

// TestSubmitHappyTurnEventOrder pins：happy 回合的事件顺序（镜像与 Sink 一致，
// 先持久化后发布）；init 幂等消费（不落镜像）；usage 记账。
func TestSubmitHappyTurnEventOrder(t *testing.T) {
	fake := engine.NewFakeHost(fixture(t, "happy-turn.jsonl"))
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	wantKinds := []event.Kind{
		event.TurnStarted, event.AssistantThinking, event.ToolUseRequested,
		event.ToolResult, event.AssistantText, event.Usage, event.TurnDone,
	}
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if len(mirror) != len(wantKinds) {
		t.Fatalf("mirror events = %d, want %d", len(mirror), len(wantKinds))
	}
	for i, ev := range mirror {
		if kindOf(ev) != wantKinds[i] {
			t.Fatalf("mirror[%d] = %s, want %s", i, kindOf(ev), wantKinds[i])
		}
	}
	got := sink.all()
	if len(got) != len(wantKinds) {
		t.Fatalf("sink events = %d, want %d", len(got), len(wantKinds))
	}
	for i, ev := range got {
		if kindOf(ev) != wantKinds[i] {
			t.Fatalf("sink[%d] = %s, want %s", i, kindOf(ev), wantKinds[i])
		}
	}
	// usage 事件反映记账（hit/cost 来自 Meter.Record）；文本块在工具回合后
	usage := got[5].(event.UsageEvent)
	if usage.Turn != 1 || usage.CostUSD <= 0 {
		t.Fatalf("usage event = %+v", usage)
	}
	// TurnDone 收口
	done := got[6].(event.TurnDoneEvent)
	if done.Turn != 1 || done.Subtype != "success" || done.IsError {
		t.Fatalf("turn done = %+v", done)
	}
}

// TestSubmitErrTurnRunning pins：回合进行中的第二次 Submit 返回 ErrTurnRunning
// （会话内回合互斥，design §6）。
func TestSubmitErrTurnRunning(t *testing.T) {
	fake := engine.NewFakeHost(fixture(t, "usage.jsonl")) // 两回合脚本
	sink := &gateSink{release: make(chan struct{}), blocked: make(chan struct{})}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- c.Submit(context.Background(), "first") }()
	select {
	case <-sink.recordingSinkBlocked():
	case <-time.After(2 * time.Second):
		t.Fatal("first submit never reached the gate")
	}
	if err := c.Submit(context.Background(), "second"); !errors.Is(err, ErrTurnRunning) {
		t.Fatalf("second submit = %v, want ErrTurnRunning", err)
	}
	close(sink.release)
	if err := <-errCh; err != nil {
		t.Fatalf("first submit: %v", err)
	}
}

// TestCacheProjectionWritten pins：cache.json 投影由 control 写入（Meter 汇总 +
// §8.3 记账标记），两回合后 turns=2、成本为正、hit_rate_floor 在场。
func TestCacheProjectionWritten(t *testing.T) {
	fake := engine.NewFakeHost(fixture(t, "usage.jsonl"))
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "t1"); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	if err := c.Submit(context.Background(), "t2"); err != nil {
		t.Fatalf("submit 2: %v", err)
	}
	proj, err := c.Mirror().ReadCache()
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if proj == nil {
		t.Fatal("cache.json projection missing")
	}
	if got := proj["turns"]; got != float64(2) {
		t.Fatalf("projection turns = %v, want 2", got)
	}
	cost, ok := proj["cost"].(float64)
	if !ok || cost <= 0 {
		t.Fatalf("projection cost = %v", proj["cost"])
	}
	if _, ok := proj["hit_rate_floor"]; !ok {
		t.Fatal("projection must carry hit_rate_floor (§8.3 记账标记)")
	}
	if _, ok := proj["hit_rate_breach"]; !ok {
		t.Fatal("projection must carry hit_rate_breach key (null = 无告警)")
	}
}

// TestInitReEmitIdempotent pins：每回合重发的 system/init 只消费一次
// （引擎 session id 以首条为准；镜像零 init 事件；每回合一个 TurnStarted）。
func TestInitReEmitIdempotent(t *testing.T) {
	fake := engine.NewFakeHost(fixture(t, "usage.jsonl"))
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "t1"); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	if err := c.Submit(context.Background(), "t2"); err != nil {
		t.Fatalf("submit 2: %v", err)
	}
	meta, err := c.Mirror().ReadMeta()
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	const wantSession = "f8dcb93b-dc7a-4263-b62d-770caf42308e" // usage.jsonl fixture
	if meta["engine_session_id"] != wantSession {
		t.Fatalf("engine id = %v, want %v", meta["engine_session_id"], wantSession)
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	started := 0
	for _, ev := range mirror {
		if kindOf(ev) == event.TurnStarted {
			started++
		}
		switch e := ev.(type) {
		case event.TurnStartedEvent:
			if e.EngineSessionID != wantSession {
				t.Fatalf("turn started engine id = %q", e.EngineSessionID)
			}
		}
	}
	if started != 2 {
		t.Fatalf("TurnStarted count = %d, want 2", started)
	}
}

// TestCancelInterrupts pins：Cancel → 引擎 interrupt 恰一次。
func TestCancelInterrupts(t *testing.T) {
	fake := engine.NewFakeHost(fixture(t, "happy-turn.jsonl"))
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := c.Cancel(context.Background()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if fake.Interrupts() != 1 {
		t.Fatalf("interrupts = %d, want 1", fake.Interrupts())
	}
}

// TestSnapshot pins：快照反映引擎身份与回合进度（TUI 状态栏数据源）。
func TestSnapshot(t *testing.T) {
	fake := engine.NewFakeHost(fixture(t, "happy-turn.jsonl"))
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	snap := c.Snapshot()
	if snap.EngineSessionID != "9ce73bff-164b-4488-a4e6-7d4e1fa4efc6" {
		t.Fatalf("snapshot engine id = %q", snap.EngineSessionID)
	}
	if snap.Turn != 1 || snap.Running {
		t.Fatalf("snapshot turn/running = %d/%v", snap.Turn, snap.Running)
	}
	if snap.MeterReport.Turns != 1 {
		t.Fatalf("snapshot meter turns = %d", snap.MeterReport.Turns)
	}
}

// TestTraceAndExitMapped pins：宿主 trace → Notice(meta)；引擎退出 →
// EngineExited 落镜像（TurnDone error 收口在先）。
func TestTraceAndExitMapped(t *testing.T) {
	turn := []engine.Event{
		{Kind: engine.KindResult, SessionID: "s", Result: &engine.ResultInfo{
			SubType: "success", ResultText: "done",
		}},
		{Kind: engine.KindExit, SessionID: "s", Exit: &engine.ExitInfo{Code: 1, Reason: "crashed"}},
	}
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{turn}})
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	kinds := make([]event.Kind, 0, len(mirror))
	var sawExit bool
	for _, ev := range mirror {
		kinds = append(kinds, kindOf(ev))
		if e, ok := ev.(event.EngineExitedEvent); ok {
			sawExit = true
			if e.Code != 1 || e.Reason != "crashed" {
				t.Fatalf("exited = %+v", e)
			}
		}
	}
	last := kinds[len(kinds)-1]
	if last != event.EngineExited {
		t.Fatalf("mirror tail = %v (kinds %v)", last, kinds)
	}
	if !sawExit {
		t.Fatal("EngineExited missing")
	}
}

// TestNewSessionHostFactoryError pins：HostFactory 失败向上传播（fail-closed）。
func TestNewSessionHostFactoryError(t *testing.T) {
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{ID: "s-hf", Workspace: "w"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	meter, err := cache.NewMeter(cache.MeterOptions{Root: root, SessionID: "s-hf"})
	if err != nil {
		t.Fatal(err)
	}
	defer meter.Close()
	policy, err := guard.NewPolicy(guard.PresetDefault, nil)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewController(Options{
		HostFactory: func(engine.BootConfig) (engine.Host, error) {
			return nil, errors.New("no cli")
		},
		Sink:   &recordingSink{},
		Store:  store,
		Meter:  meter,
		Gate:   gate,
		Config: mustDefaultConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.NewSession(context.Background()); err == nil {
		t.Fatal("host factory error must propagate")
	}
}

// TestLateResultIgnored pins：重复/迟到 result 不关闭已收口回合的通道
// （review Important-1：无守卫会 panic 泵）——落 Notice，泵存活，镜像无第二个
// usage/turn_done。
func TestLateResultIgnored(t *testing.T) {
	result := func(text string) engine.Event {
		return engine.Event{Kind: engine.KindResult, SessionID: "s", Result: &engine.ResultInfo{
			SubType: "success", ResultText: text,
		}}
	}
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{
		{result("first"), result("duplicate")},
	}})
	sink := &recordingSink{}
	c := newTestController(t, fake, sink)
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if err := c.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 先 Close（等待泵退出 = happens-before），再读镜像——消除迟到 Notice
	// 追加与镜像读取的确定性窗口（review fix-round Minor-1）。
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close (pump must be alive): %v", err)
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	usageCount, doneCount, noticeCount := 0, 0, 0
	for _, ev := range mirror {
		switch e := ev.(type) {
		case event.UsageEvent:
			usageCount++
		case event.TurnDoneEvent:
			doneCount++
			if e.ResultText != "first" {
				t.Fatalf("turn done result text = %q", e.ResultText)
			}
		case event.NoticeEvent:
			noticeCount++
			if !strings.Contains(e.Content, "重复 result") {
				t.Fatalf("notice = %+v", e)
			}
		}
	}
	if usageCount != 1 || doneCount != 1 || noticeCount != 1 {
		t.Fatalf("usage=%d done=%d notice=%d, want 1/1/1", usageCount, doneCount, noticeCount)
	}
}

// ---- 助手 ----

func kindOf(ev any) event.Kind {
	switch ev.(type) {
	case event.TurnStartedEvent:
		return event.TurnStarted
	case event.AssistantTextEvent:
		return event.AssistantText
	case event.AssistantThinkingEvent:
		return event.AssistantThinking
	case event.ToolUseRequestedEvent:
		return event.ToolUseRequested
	case event.ToolResultEvent:
		return event.ToolResult
	case event.ApprovalRequestEvent:
		return event.ApprovalRequest
	case event.ApprovalResolvedEvent:
		return event.ApprovalResolved
	case event.UsageEvent:
		return event.Usage
	case event.NoticeEvent:
		return event.Notice
	case event.TurnDoneEvent:
		return event.TurnDone
	case event.EngineExitedEvent:
		return event.EngineExited
	}
	return ""
}

// recordingSinkBlocked 暴露 gateSink 的阻塞信号（测试同步用）。
func (s *gateSink) recordingSinkBlocked() <-chan struct{} {
	return s.blocked
}
