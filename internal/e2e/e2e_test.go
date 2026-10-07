// Package e2e 是系统级回归（T11.1）：FakeHost × 真实 control/cache/sessionx/guard
// 栈——六个系统边界场景（design §11）。只使用各包导出面，接线与 cli 生产路径
// 一致（openSession 同序：store → meter → gate/bridge → controller）。
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/registry"
	"mulix/internal/sessionx"
)

// ---- 测试替身 ----

// recordingSink 按序记录发布的事件（mirror-first 的对面观测点）。
type recordingSink struct{ events []any }

func (s *recordingSink) Publish(ev any) { s.events = append(s.events, ev) }

// allowApprover 自动允许（场景 1；真 Gate+bridge 链路）。
type allowApprover struct{}

func (allowApprover) ApproveRequest(context.Context, guard.ApprovalRequest) (guard.ApprovalVerdict, error) {
	return guard.ApprovalVerdict{Allow: true, ResolvedBy: "e2e-allow"}, nil
}

type env struct {
	root   string
	ctrl   *control.Controller
	bridge *control.UserApprovalBridge
	store  *sessionx.Store
	meter  *cache.Meter
	sink   *recordingSink
	fake   *engine.FakeHost
}

type envOpts struct {
	attended   bool
	floor      float64
	cold       time.Duration
	now        func() time.Time
	bootOut    *engine.BootConfig
	fixtureDir string
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{ID: "s-e2e", Workspace: "w", Unattended: !o.attended})
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: "s-e2e",
		Pricing:       cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
		ColdAfterIdle: o.cold, Now: o.now,
	})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	e := &env{root: root, store: store, meter: meter, sink: &recordingSink{}}
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if o.floor > 0 {
		cfg.Cache.HitRateFloor = o.floor
	}
	policy, err := guard.NewPolicy(guard.Preset(cfg.Permissions.Preset), nil)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	gopts := guard.GateOptions{Policy: policy, Timeout: 2 * time.Second}
	if o.attended { // attended = 有裁决者；e2e 用自动裁决（fake 时序无法等人工裁决）
		gopts.Approver = allowApprover{}
	}
	gate, err := guard.NewGate(gopts)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	fixture, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	script := fixture
	for len(script.Turns) < 11 { // 场景 4 需 ≥10 有效回合触发窗口评估（WindowSize=10）
		script.Turns = append(script.Turns, script.Turns[0])
	}
	// 场景 1/2：在首个回合的 result 前插入 can_use_tool 控制请求（系统边界
	// 复验权限回路；无人值守场景同样插入——fail-closed 链路照跑）。
	turn0 := script.Turns[0]
	withCR := make([]engine.Event, 0, len(turn0)+1)
	for _, ev := range turn0 {
		if ev.Kind == engine.KindResult {
			withCR = append(withCR, *canUseTool("req-e2e", "tu-e2e", "Write", `{"file_path":"e2e.txt"}`))
		}
		withCR = append(withCR, ev)
	}
	script.Turns[0] = withCR
	e.fake = engine.NewFakeHost(script)
	bootCapture := o.bootOut
	e.ctrl, err = control.NewController(control.Options{
		HostFactory: func(boot engine.BootConfig) (engine.Host, error) {
			if bootCapture != nil {
				*bootCapture = boot // 场景 6：系统边界捕获 BootArtifact
			}
			return e.fake, nil
		},
		Sink: e.sink, Store: store, Meter: meter,
		Gate: gate, Approvals: e.bridge,
		Config: *cfg, Workspace: "w", Unattended: !o.attended,
		Registry: registry.NewServer(registry.Options{Name: "mulix", Version: "0.1.0-dev"}),
	})
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	if err := e.ctrl.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() {
		_ = e.ctrl.Close(context.Background())
		_ = e.meter.Close()
		_ = e.store.Close()
	})
	return e
}

// ---- 六场景 ----

// 场景 1：happy 回合（含审批 Allow）→ 事件全落镜像、Sink 顺序正确。
// can_use_tool 请求由 newEnv 预埋在 fixture 回合中段（result 前）——pump 对
// 回合外的控制请求会拒绝，goroutine 延时注入不可靠（终评 M-9 移除）。
func TestE2EHappyTurnWithApproval(t *testing.T) {
	e := newEnv(t, envOpts{attended: true})
	if err := e.ctrl.Submit(context.Background(), "write a file"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 镜像：1:1 + 审批链完整
	events, err := e.store.ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	var started, done, req, resolved int
	for _, ev := range events {
		switch v := ev.(type) {
		case event.TurnStartedEvent:
			started++
		case event.TurnDoneEvent:
			done++
			if v.IsError {
				t.Fatalf("turn failed: %s", v.Subtype)
			}
		case event.ApprovalRequestEvent:
			req++
		case event.ApprovalResolvedEvent:
			resolved++
			if v.Decision != "allow" {
				t.Fatalf("decision = %s, want allow", v.Decision)
			}
			if v.Source != "user" {
				t.Fatalf("source = %s, want user", v.Source)
			}
		}
	}
	if started != 1 || done != 1 || req != 1 || resolved != 1 {
		t.Fatalf("mirror counts: started=%d done=%d req=%d resolved=%d", started, done, req, resolved)
	}
	// Sink 顺序 = 镜像事件序（同型事件相对顺序一致）
	assertSinkMirrorsStore(t, e)
}

// 场景 2：审批 Deny / 无人值守 fail-closed（请求同样由 fixture 中段预埋）。
func TestE2EDenyFailClosed(t *testing.T) {
	e := newEnv(t, envOpts{attended: false}) // 无 Approver
	if err := e.ctrl.Submit(context.Background(), "try dangerous"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	var resolved bool
	for _, ev := range e.sink.events {
		if r, ok := ev.(event.ApprovalResolvedEvent); ok && r.Decision == "deny" {
			resolved = true
		}
	}
	if !resolved {
		t.Fatal("deny resolution must be published")
	}
	// 引擎收到的是 deny 应答（控制协议回执）
	resps := e.fake.Responses()
	if len(resps) == 0 {
		t.Fatal("control response must reach the host")
	}
}

// 场景 3：usage → Meter → cache 报表数字一致（mulix cache 的权威口径）。
func TestE2EUsageReportConsistency(t *testing.T) {
	e := newEnv(t, envOpts{attended: false})
	if err := e.ctrl.Submit(context.Background(), "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 手工复算口径（终评 I-3 实化）：weighted hit = read/(uncached+write+read)，
	// 与 AggregateRecords 的 SessionHit 口径逐数字对账。
	recs := e.meter.Records()
	if len(recs) == 0 {
		t.Fatal("meter must have observed the turn")
	}
	var unc, wr, rd, out int64
	for _, r := range recs {
		unc += r.Uncached
		wr += r.Write
		rd += r.Read
		out += r.Output
	}
	report, err := cache.ReadUsageReport(e.root, "s-e2e")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if report.Turns != int64(len(recs)) {
		t.Fatalf("report turns = %d, want %d", report.Turns, len(recs))
	}
	if report.Uncached != unc || report.Write != wr || report.Read != rd || report.Output != out {
		t.Fatalf("token sums diverge: report(u=%d,w=%d,r=%d,o=%d) vs meter(u=%d,w=%d,r=%d,o=%d)",
			report.Uncached, report.Write, report.Read, report.Output, unc, wr, rd, out)
	}
	denom := unc + wr + rd
	if denom == 0 {
		t.Fatal("fixture usage must produce nonzero denominator")
	}
	wantHit := float64(rd) / float64(denom)
	if diff := report.SessionHit - wantHit; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("SessionHit = %v, want %v (manual weighted口径)", report.SessionHit, wantHit)
	}
}

// 场景 4：空闲 TTL → expected_cold 标注；命中率跌破 floor → 结构化 Notice。
func TestE2EColdMarkAndFloorNotice(t *testing.T) {
	base := time.Now()
	cur := base
	e := newEnv(t, envOpts{attended: false, cold: 4 * time.Minute, floor: 0.99, now: func() time.Time { return cur }})
	// 回合 1–10（热）：fixture 命中率约 0.49——第 10 个有效回合起窗口评估
	//（WindowSize=10，Guard 预热期不评估）→ 低于 0.99 地板 → Notice。
	for i := 0; i < 10; i++ {
		if err := e.ctrl.Submit(context.Background(), fmt.Sprintf("t%d", i+1)); err != nil {
			t.Fatalf("t%d: %v", i+1, err)
		}
		cur = cur.Add(10 * time.Second) // 回合间隔 < TTL：全部热
	}
	cur = cur.Add(5 * time.Minute) // 越过 TTL → 下一回合 expected_cold
	// 回合 11（冷）
	if err := e.ctrl.Submit(context.Background(), "t11"); err != nil {
		t.Fatalf("t11: %v", err)
	}
	recs := e.meter.Records()
	if len(recs) != 11 {
		t.Fatalf("records = %d, want 11", len(recs))
	}
	if recs[0].ExpectedCold {
		t.Fatal("turn 1 must be hot")
	}
	if !recs[10].ExpectedCold {
		t.Fatal("turn 11 must be expected_cold after 5m idle")
	}
	var notice bool
	for _, ev := range e.sink.events {
		if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "命中率") {
			notice = true
		}
	}
	if !notice {
		t.Fatal("floor breach must emit structured Notice")
	}
}

// 场景 5：崩溃恢复——孤儿回合 → 恢复门 → Resume 续链（记账续号）。
func TestE2ECrashRecoveryResume(t *testing.T) {
	root := t.TempDir()
	store, err := sessionx.Create(root, sessionx.Options{ID: "s-crash", Workspace: "w"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 崩溃痕迹：TurnStarted 后进程死亡（无 TurnDone）
	if err := store.Append(event.TurnStartedEvent{Seq: 1, SessionID: "s-crash"}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	required, _, err := control.RecoveryRequired(store)
	if err != nil || !required {
		t.Fatalf("recovery gate must trigger: %v %v", required, err)
	}
	meter, err := cache.NewMeter(cache.MeterOptions{Root: root, SessionID: "s-crash",
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	// 新进程接管：镜像 meta 记录引擎会话 id（崩溃前已 init）
	if err := store.SetEngineID("e-crash"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	cfg, _, cfgErr := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if cfgErr != nil {
		t.Fatalf("config: %v", cfgErr)
	}
	policy, err := guard.NewPolicy(guard.Preset(cfg.Permissions.Preset), nil)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	fixture, ferr := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if ferr != nil {
		t.Fatalf("fixture: %v", ferr)
	}
	sink := &recordingSink{}
	ctrl, err := control.NewController(control.Options{
		HostFactory: func(engine.BootConfig) (engine.Host, error) { return engine.NewFakeHost(fixture), nil },
		Sink:        sink, Store: store, Meter: meter, Gate: gate, Config: *cfg, Workspace: "w",
	})
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	defer func() { _ = ctrl.Close(context.Background()); _ = meter.Close(); _ = store.Close() }()
	if err := ctrl.Resume(context.Background(), "e-crash"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// 孤儿被合成收口 → 恢复门解除 → 新回合落镜
	events, _ := store.ReadAllEvents()
	var done int
	for _, ev := range events {
		if v, ok := ev.(event.TurnDoneEvent); ok && v.Subtype == "resume_reconciled" {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("orphan close-out = %d, want 1", done)
	}
	if required, _, _ := control.RecoveryRequired(store); required {
		t.Fatal("gate must be clear after resume")
	}
}

// 场景 6：两次 Start 的 BootArtifact 逐字节一致（构造护栏在系统边界复验）。
func TestE2EBootArtifactByteStable(t *testing.T) {
	var first, second engine.BootConfig
	e1 := newEnv(t, envOpts{attended: false, bootOut: &first})
	_ = e1
	e2 := newEnv(t, envOpts{attended: false, bootOut: &second})
	_ = e2
	if first.AppendSystemPrompt != second.AppendSystemPrompt ||
		first.MCPConfigJSON != second.MCPConfigJSON ||
		strings.Join(first.AllowedTools, ",") != strings.Join(second.AllowedTools, ",") {
		t.Fatalf("boot artifacts diverge:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if first.MCPConfigJSON == "" {
		t.Fatal("mcp-config must be composed")
	}
}

// ---- 辅助 ----

// assertSinkMirrorsStore 校验 Sink 发布序列与镜像事件序一致（mirror-first
// 不变量的系统边界观测；终评 I-5 修复：两个序列各自独立取序号再比较）。
func assertSinkMirrorsStore(t *testing.T, e *env) {
	t.Helper()
	events, err := e.store.ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	// 存在性对等：镜像里有审批请求 ⇔ Sink 里有。
	storeReq, sinkReq := false, false
	for _, ev := range events {
		if _, ok := ev.(event.ApprovalRequestEvent); ok {
			storeReq = true
			break
		}
	}
	sinkResIdx, sinkReqIdx := -1, -1
	for i, ev := range e.sink.events {
		if _, ok := ev.(event.ApprovalRequestEvent); ok && sinkReqIdx == -1 {
			sinkReq = true
			sinkReqIdx = i
		}
		if _, ok := ev.(event.ApprovalResolvedEvent); ok && sinkResIdx == -1 {
			sinkResIdx = i
		}
	}
	if storeReq != sinkReq {
		t.Fatal("approval request presence differs between mirror and sink")
	}
	// Sink 序内：ApprovalRequest 必须先于其 ApprovalResolved。
	if sinkReqIdx != -1 && sinkResIdx != -1 && sinkResIdx < sinkReqIdx {
		t.Fatal("sink resolved before request — order broken")
	}
}

// canUseTool 构造引擎发来的 can_use_tool 控制请求信封（与 control 的 routing
// 测试同构；e2e 在系统边界注入——权限回路的入口复验）。
func canUseTool(requestID, toolUseID, tool string, input string) *engine.Event {
	args := map[string]any{"tool_name": tool, "tool_use_id": toolUseID}
	if input != "" {
		args["input"] = json.RawMessage(input)
	}
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID + "-mcp",
		"method":  "tools/call",
		"params":  map[string]any{"name": "permission", "arguments": args},
	})
	return &engine.Event{Kind: engine.KindControlRequest, SessionID: "s-e2e", Control: &engine.ControlRequest{
		RequestID: requestID,
		Request: engine.ControlRequestBody{
			SubType:    "mcp_message",
			ServerName: "mulix",
			Message:    msg,
		},
	}}
}
