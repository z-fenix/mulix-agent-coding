package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/agentloop"
	"mulix/internal/cache"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/kernel"
	"mulix/internal/registry"
	"mulix/internal/sessionx"
)

// ---- 生命周期：Resume 续链 / 漂移留痕 / 崩溃恢复 ----

// newTestControllerCapturingBoot 构造无人值守控制器并捕获传入 HostFactory 的
// BootConfig（Resume 参数传递的断言用；其余同 newTestController）。
func newTestControllerCapturingBoot(t *testing.T, host engine.Host, sink Sink, bootOut *engine.BootConfig, opts ...func(*Options)) *Controller {
	t.Helper()
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{
		ID: "s-lc", Workspace: "w", Model: "m", PinnedModel: "m",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: "s-lc",
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
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}
	cOpts := Options{
		HostFactory: func(boot engine.BootConfig) (engine.Host, error) {
			*bootOut = boot
			return host, nil
		},
		Sink: sink, Store: store, Meter: meter, Gate: gate,
		Config: mustDefaultConfig(), Workspace: "w",
		Kernel: kernelCtxWithLoop(), // 021 FR-102:loop 权威 ctx(测试注入缝)
	}
	for _, f := range opts {
		f(&cOpts)
	}
	c, err := NewController(cOpts)
	if err != nil {
		t.Fatalf("new controller: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func initEvent(sessionID string) engine.Event {
	return engine.Event{Kind: engine.KindInit, SessionID: sessionID, UUID: "init-1",
		Init: &engine.InitInfo{CLIVersion: "2.1.291", Model: "m", PermissionMode: "default"}}
}

// TestResumeChainEvents pins：Resume 把引擎会话 id 传入 Boot（engine.lock
// 校验在 Host 层——T17），成功续链后镜像落 resume 链事件（Notice info），
// EngineSessionID 以引擎 init 上报为准。
func TestResumeChainEvents(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{OnStart: []engine.Event{initEvent("e-resume")}})
	sink := &recordingSink{}
	var gotBoot engine.BootConfig
	c := newTestControllerCapturingBoot(t, fake, sink, &gotBoot)
	if err := c.Resume(context.Background(), "e-resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if gotBoot.ResumeEngineSessionID != "e-resume" {
		t.Fatalf("boot resume id = %q, want e-resume", gotBoot.ResumeEngineSessionID)
	}
	if c.EngineSessionID() != "e-resume" {
		t.Fatalf("engine session id = %q, want e-resume (init 上报为准)", c.EngineSessionID())
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	found := false
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && n.Level == "info" &&
			strings.Contains(n.Content, "e-resume") && strings.Contains(n.Content, "resume") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no resume chain notice in mirror (%d events)", len(mirror))
	}
}

// TestResumeEngineMismatchWarning pins：Resume 请求 id 与引擎上报不一致时，
// 以引擎为上下文权威（EngineSessionID=上报值），镜像保留告警（含两个 id）。
func TestResumeEngineMismatchWarning(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{OnStart: []engine.Event{initEvent("e-actual")}})
	sink := &recordingSink{}
	var gotBoot engine.BootConfig
	c := newTestControllerCapturingBoot(t, fake, sink, &gotBoot)
	if err := c.Resume(context.Background(), "e-requested"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if c.EngineSessionID() != "e-actual" {
		t.Fatalf("engine session id = %q, want e-actual（引擎权威）", c.EngineSessionID())
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	warned := false
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && n.Level == "warning" &&
			strings.Contains(n.Content, "e-requested") && strings.Contains(n.Content, "e-actual") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no mismatch warning mentioning both ids (%d events)", len(mirror))
	}
}

// TestResumeMirrorMismatchWarning pins（design §10）：镜像已记录的
// engine_session_id 与引擎上报不一致（如崩溃后错续别的会话）→ 告警 +
// 引擎权威（meta 更新为上报值；审计靠告警事件留痕）。
func TestResumeMirrorMismatchWarning(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{OnStart: []engine.Event{initEvent("e-actual")}})
	sink := &recordingSink{}
	var gotBoot engine.BootConfig
	c := newTestControllerCapturingBoot(t, fake, sink, &gotBoot)
	if err := c.opts.Store.SetEngineID("e-mirror"); err != nil {
		t.Fatalf("set engine id: %v", err)
	}
	// 用户按镜像记录续链（requested == 镜像值），引擎却上报另一个会话
	if err := c.Resume(context.Background(), "e-mirror"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	warned := false
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && n.Level == "warning" &&
			strings.Contains(n.Content, "镜像记录引擎会话 e-mirror") &&
			strings.Contains(n.Content, "e-actual") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no mirror-mismatch warning (%d events)", len(mirror))
	}
	// 引擎权威：meta 已由 onInit 回填为上报值（后续 --resume 不会拿错 id）
	meta, err := c.opts.Store.ReadMeta()
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta["engine_session_id"] != "e-actual" {
		t.Fatalf("meta engine id = %v, want e-actual（引擎权威）", meta["engine_session_id"])
	}
}

// TestResumeDriftTraceMirrored pins：漂移放行留痕在 Resume 链路同样生效
// （Host 层产出 engine_drift trace → control 映射 Notice；code 进镜像内容）。
func TestResumeDriftTraceMirrored(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{OnStart: []engine.Event{
		initEvent("e-drift"),
		engine.Event{Kind: engine.KindTrace, SessionID: "e-drift", UUID: "t1",
			Trace: &engine.TraceInfo{Code: "engine_drift",
				Message: "engine version drift allowed by explicit flag",
				Details: json.RawMessage(`{"lock_version":"2.1.291","actual_version":"9.9.9"}`)}},
	}})
	sink := &recordingSink{}
	var gotBoot engine.BootConfig
	c := newTestControllerCapturingBoot(t, fake, sink, &gotBoot)
	if err := c.Resume(context.Background(), "e-drift"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	mirror, _ := c.Mirror().ReadAllEvents()
	found := false
	for _, ev := range mirror {
		if n, ok := ev.(event.NoticeEvent); ok && n.IsMeta &&
			strings.Contains(n.Content, "engine_drift") &&
			strings.Contains(n.Content, "engine version drift allowed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("engine_drift trace not mirrored with code (%d events)", len(mirror))
	}
}

// TestRecoveryRequiredDetection pins：崩溃恢复判据——镜像存在未闭合回合
// （TurnStarted 无配对 TurnDone）→ required；各收口形态（success/send_failed/
// engine_exited/engine_closed）都算闭合。
func TestRecoveryRequiredDetection(t *testing.T) {
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{
		ID: "s-rec", Workspace: "w", Model: "m", PinnedModel: "m",
	})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	check := func(want bool, wantTurn int64) {
		t.Helper()
		got, turn, err := RecoveryRequired(store)
		if err != nil {
			t.Fatalf("recovery: %v", err)
		}
		if got != want || turn != wantTurn {
			t.Fatalf("RecoveryRequired = (%v,%d), want (%v,%d)", got, turn, want, wantTurn)
		}
	}
	check(false, 0) // 空镜像
	// 判据说明：TurnStarted 无回合号（Submit 时发布），TurnStarted:TurnDone
	// 恰 1:1 是 T22 钉死的不变量——未闭合回合 = 计数差，最近未闭合 = started 计数。
	mkTurnStarted := func(seq int64) event.TurnStartedEvent {
		return event.TurnStartedEvent{Seq: seq, SessionID: "s-rec"}
	}
	mkTurnDone := func(turn int64, sub string) event.TurnDoneEvent {
		return event.TurnDoneEvent{Seq: turn*10 + 1, Turn: turn, Subtype: sub, IsError: sub != "success"}
	}
	if err := store.Append(mkTurnStarted(10)); err != nil {
		t.Fatalf("append: %v", err)
	}
	check(true, 1) // 崩溃：turn1 未闭合（started=1 > done=0）
	if err := store.Append(mkTurnDone(1, "success")); err != nil {
		t.Fatalf("append: %v", err)
	}
	check(false, 0)
	if err := store.Append(mkTurnStarted(20)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Append(mkTurnDone(2, "engine_exited")); err != nil {
		t.Fatalf("append: %v", err)
	}
	check(false, 0) // 崩溃收口形态也算闭合
	if err := store.Append(mkTurnStarted(30)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Append(mkTurnStarted(40)); err != nil {
		t.Fatalf("append: %v", err)
	}
	check(true, 4) // 多个未闭合 → 报最近未闭合回合（started 计数）
}

// TestPublishStressMirrorIntact pins（评审 carry 加固）：Submit 与泵侧事件
// 并发发布时镜像不损坏（ReadAllEvents 全量可解析、seq 均为正）。
func TestPublishStressMirrorIntact(t *testing.T) {
	fake := engine.NewFakeHost(engine.FakeScript{Turns: [][]engine.Event{
		{engine.Event{Kind: engine.KindResult, SessionID: "s", Result: &engine.ResultInfo{SubType: "success"}}},
	}})
	sink := &recordingSink{}
	c := newTestControllerWithPolicyRules(t, fake, sink,
		policyRuleIn{raw: "Bash(go:*)", effect: guard.DecisionAllow})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := c.Submit(ctx, "hi"); err != nil {
			t.Errorf("submit: %v", err)
		}
	}()
	for i := range 8 {
		cr := canUseTool("req-s"+string(rune('a'+i)), "tu-s"+string(rune('a'+i)),
			"Bash", `{"command":"go version"}`)
		if err := fake.InjectControlRequest(*cr.Control); err != nil {
			t.Fatalf("inject: %v", err)
		}
	}
	wg.Wait()
	waitResponses(t, fake, 8)
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("mirror unreadable (corruption): %v", err)
	}
	if len(mirror) == 0 {
		t.Fatal("mirror empty")
	}
	for _, ev := range mirror {
		switch e := ev.(type) {
		case event.TurnStartedEvent:
			if e.Seq <= 0 {
				t.Fatalf("bad seq in %+v", e)
			}
		case event.ApprovalResolvedEvent:
			if e.Seq <= 0 {
				t.Fatalf("bad seq in %+v", e)
			}
		}
	}
}

// TestComposeBootRegistryAndPromptTool pins（T27 接线）：注入 registry 时
// mcp-config 含 mulix stdio 条目且 --permission-prompt-tool 钉为
// mcp__mulix__permission（权限回路最后一环）；未注入（nil）保持空面——既有
// 测试/无人值守测试兼容。
func TestComposeBootRegistryAndPromptTool(t *testing.T) {
	var boot engine.BootConfig
	c := newTestControllerCapturingBoot(t, engine.NewFakeHost(fixture(t, "happy-turn.jsonl")), &recordingSink{}, &boot,
		func(o *Options) {
			o.Registry = registry.NewServer(registry.Options{Name: "mulix", Version: "test", SelfCommand: []string{"mulix.exe", "mcp", "serve"}})
		})
	t.Cleanup(func() { c.Meter().Close() })
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if boot.PermissionPromptTool != "mcp__mulix__permission" {
		t.Fatalf("prompt tool = %q, want mcp__mulix__permission", boot.PermissionPromptTool)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(boot.MCPConfigJSON), &cfg); err != nil {
		t.Fatalf("mcp config: %v", err)
	}
	if len(cfg.MCPServers) != 1 {
		t.Fatalf("mcp servers = %+v, want only mulix", cfg.MCPServers)
	}
	if got := cfg.MCPServers["mulix"].Command; got != "mulix.exe" {
		t.Fatalf("mulix command = %q", got)
	}
	if args := cfg.MCPServers["mulix"].Args; len(args) != 2 || args[0] != "mcp" || args[1] != "serve" {
		t.Fatalf("mulix args = %v", args)
	}
}

// TestComposeBootEmptyRegistryKeepsPromptToolUnset pins：nil registry → 不设
// prompt tool（fail-closed：mcp-config 无 mulix 服务器时指向不存在的工具会
// 让引擎拒绝启动）。
func TestComposeBootEmptyRegistryKeepsPromptToolUnset(t *testing.T) {
	var boot engine.BootConfig
	c := newTestControllerCapturingBoot(t, engine.NewFakeHost(fixture(t, "happy-turn.jsonl")), &recordingSink{}, &boot)
	t.Cleanup(func() { c.Meter().Close() })
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if boot.PermissionPromptTool != "" {
		t.Fatalf("prompt tool = %q, want empty without registry", boot.PermissionPromptTool)
	}
	if strings.Contains(boot.MCPConfigJSON, "mulix") {
		t.Fatalf("mcp config = %q, want no mulix entry without registry", boot.MCPConfigJSON)
	}
}

// TestComposeBootAllowDriftPassthrough pins：Options.AllowEngineDrift →
// Boot.AllowEngineDrift（cli --allow-engine-drift 的通道；Host 层消费并留痕）。
func TestComposeBootAllowDriftPassthrough(t *testing.T) {
	var boot engine.BootConfig
	c := newTestControllerCapturingBoot(t, engine.NewFakeHost(fixture(t, "happy-turn.jsonl")), &recordingSink{}, &boot,
		func(o *Options) { o.AllowEngineDrift = true })
	t.Cleanup(func() { c.Meter().Close() })
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	if !boot.AllowEngineDrift {
		t.Fatal("AllowEngineDrift must pass through to Boot")
	}
}

// TestCloseAfterFailedStart pins（T27 e2e 暴露）：start 未走到挂泵（如
// HostFactory 失败）时 Close 必须立即返回，不得在 pumpDone 上无限 select。
func TestCloseAfterFailedStart(t *testing.T) {
	var out bytes.Buffer
	var boot engine.BootConfig
	c := newTestControllerCapturingBoot(t, nil, &recordingSink{}, &boot,
		func(o *Options) {
			o.HostFactory = func(engine.BootConfig) (engine.Host, error) {
				return nil, errors.New("host factory boom")
			}
		})
	_ = out
	if err := c.NewSession(context.Background()); err == nil {
		t.Fatal("start must fail")
	}
	done := make(chan struct{})
	go func() { _ = c.Close(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung after failed start")
	}
}

// TestResumeReconcilesOrphanTurn pins（T27 评审 Important-3）：resume 合成
// 收口孤儿回合（subtype=resume_reconciled）——RecoveryRequired 恢复 1:1，
// 恢复门不再永久拒绝该工作区的新会话。
func TestResumeReconcilesOrphanTurn(t *testing.T) {
	var boot engine.BootConfig
	c := newTestControllerCapturingBoot(t, engine.NewFakeHost(fixture(t, "happy-turn.jsonl")), &recordingSink{}, &boot)
	store := c.opts.Store
	if err := store.Append(event.TurnStartedEvent{Seq: 1, SessionID: "s-lc"}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if required, _, err := RecoveryRequired(store); err != nil || !required {
		t.Fatalf("pre-resume recovery = (%v, %v), want required", required, err)
	}
	if err := c.Resume(context.Background(), "e-resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if required, _, err := RecoveryRequired(store); err != nil || required {
		t.Fatalf("post-resume recovery = (%v, %v), want clear", required, err)
	}
}

// TestStartFailureClosesHost pins（T27 评审 Important-1）：host.Start 失败时
// control 必须调用 host.Close（真实宿主的 spawn 后失败路径不清理 mcp-config
// 临时文件——泄漏源）。
func TestStartFailureClosesHost(t *testing.T) {
	base := engine.NewFakeHost(engine.FakeScript{})
	host := &closingHostStub{Host: base}
	var boot engine.BootConfig
	c := newTestControllerCapturingBoot(t, host, &recordingSink{}, &boot,
		func(o *Options) {
			o.HostFactory = func(engine.BootConfig) (engine.Host, error) { return host, nil }
		})
	if err := c.NewSession(context.Background()); err == nil {
		t.Fatal("start must fail")
	}
	if !host.closed {
		t.Fatal("host.Close must be called after failed Start")
	}
}

// closingStartHost 是可令 Start 失败并记录 Close 的桩。
type closingHostStub struct {
	engine.Host
	closed bool
}

func (f *closingHostStub) Start(context.Context, engine.BootConfig) error {
	return errors.New("spawn boom")
}

func (f *closingHostStub) Close(context.Context) error {
	f.closed = true
	return nil
}

// kernelCtxWithLoop 返回挂载 loop 插件的最小根 ctx(021 测试注入缝)。
func kernelCtxWithLoop() *kernel.Context {
	root := kernel.NewContext(nil)
	if err := root.Mount(agentloop.NewPlugin()); err != nil {
		panic(err)
	}
	return root
}

// TestNewControllerRequiresKernelLoop 钉 021 FR-102:缺 Kernel/缺键/类型
// 不符三态 fail-closed(键即契约——缺 loop 的 controller 不该存在)。
func TestNewControllerRequiresKernelLoop(t *testing.T) {
	base := func() Options {
		root := t.TempDir()
		store, err := sessionx.Create(root, sessionx.Options{
			ID: "s-loop-test", Workspace: "w", Model: "m", PinnedModel: "m",
		})
		if err != nil {
			t.Fatalf("create store: %v", err)
		}
		t.Cleanup(func() { store.Close() })
		policy, err := guard.NewPolicy(guard.PresetDefault, nil)
		if err != nil {
			t.Fatalf("new policy: %v", err)
		}
		gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
		if err != nil {
			t.Fatalf("new gate: %v", err)
		}
		return Options{
			HostFactory: func(boot engine.BootConfig) (engine.Host, error) {
				return engine.NewFakeHost(engine.FakeScript{}), nil
			},
			Sink: &recordingSink{}, Store: store,
			Meter: &cache.Meter{},
			Gate:  gate,
		}
	}
	// ① Kernel nil。
	if _, err := NewController(base()); err == nil || !strings.Contains(err.Error(), "kernel context") {
		t.Fatalf("nil kernel err = %v", err)
	}
	// ② ctx 缺 agentloop 键。
	o := base()
	o.Kernel = kernel.NewContext(nil)
	if _, err := NewController(o); err == nil || !strings.Contains(err.Error(), "服务键缺失") {
		t.Fatalf("missing key err = %v", err)
	}
	// ③ 键值类型不符。
	o.Kernel = kernel.NewContext(nil)
	if err := o.Kernel.Provide("agentloop", "not-a-loop"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewController(o); err == nil || !strings.Contains(err.Error(), "类型不符") {
		t.Fatalf("wrong type err = %v", err)
	}
	// ④ 正确键 → 组装成功。
	o.Kernel = kernelCtxWithLoop()
	if _, err := NewController(o); err != nil {
		t.Fatalf("valid ctx err = %v", err)
	}
}
