package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/agentloop"
	"mulix/internal/cache"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/kernel"
	"mulix/internal/registry"
	"mulix/internal/sessionx"
	"mulix/internal/subagent"
)

// ---- M12/T12.4 组装接线测试基建 ----

// kernelCtxWithLoop 返回挂载 loop 插件的最小根 ctx（021 FR-102 测试注入缝；
// 形态同 control 测试面同名 helper——cli 包无共享测试基建，各自最小实现）。
func kernelCtxWithLoop() *kernel.Context {
	root := kernel.NewContext(nil)
	if err := root.Mount(agentloop.NewPlugin()); err != nil {
		panic(err)
	}
	return root
}

// callTool 经全局注册面按名直调工具（serve 子进程的进程内替身）：工具
// Execute → POST MULIX_SUBAGENT_CALLBACK → 前台回调桥——与真实
// serve→前台协议同一路径。注册面由 buildComponents 的 RegisterConditional
// 接线产出（组合根职责，本测试同时钉该接线）。
func callTool(t *testing.T, name, args string) string {
	t.Helper()
	tool, ok := registry.Lookup(name)
	if !ok {
		t.Fatalf("tool %q not registered（RegisterConditional 组合根接线缺失？）", name)
	}
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s execute: %v", name, err)
	}
	return out
}

// opResult 解析工具应答 JSON（fail-closed 应答也是 JSON）。
func opResult(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("tool response not json: %v (%s)", err, raw)
	}
	return m
}

// bootCapture 按装配序捕获各控制器的 Boot 面（0=主会话，随后=子会话按
// spawn 序——工厂组装配合同步完成，索引确定）。
type bootCapture struct {
	mu    sync.Mutex
	boots []engine.BootConfig
}

func (b *bootCapture) add(boot engine.BootConfig) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.boots = append(b.boots, boot)
	return len(b.boots) - 1
}

func (b *bootCapture) env(i int, key string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, e := range b.boots[i].Env {
		if k, v, ok := strings.Cut(e, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// happyChildScript 子会话完成脚本：assistant 文本 + result（用量非零——
// DelegationCompleted 的 usage 摘要断言靶）。
func happyChildScript() engine.FakeScript {
	return engine.FakeScript{Turns: [][]engine.Event{{
		{Kind: engine.KindAssistant, SessionID: "e-sub", UUID: "a-1",
			Assistant: &engine.AssistantMsg{ID: "m-1", Model: "fake-model",
				Blocks: []engine.ContentBlock{{Type: "text", Text: "子代理结论：任务已完成"}}}},
		{Kind: engine.KindResult, SessionID: "e-sub", UUID: "r-1",
			Result: &engine.ResultInfo{SubType: "success", ResultText: "子代理结论：任务已完成",
				Usage: engine.Usage{InputTokens: 120, OutputTokens: 45, CacheReadTokens: 30,
					Details: engine.OutputTokensDetails{ThinkingTokens: 7}}}},
	}}}
}

// hangChildScript 挂起回合脚本：assistant 后无 result——取消收口只能靠
// 父侧级联（泵 engine_closed 合成收口）——孤儿回合钉的靶子。
func hangChildScript() engine.FakeScript {
	return engine.FakeScript{Turns: [][]engine.Event{{
		{Kind: engine.KindAssistant, SessionID: "e-hang", UUID: "h-1",
			Assistant: &engine.AssistantMsg{ID: "m-h", Model: "fake-model",
				Blocks: []engine.ContentBlock{{Type: "text", Text: "开始长任务…"}}}},
	}}}
}

// waitFor 轮询直到 cond 为真（子代理终态收口是异步泵路径；10s 上限）。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// mirrorEvents 读一个会话镜像的完整事件流（OpenStore 只读消费；写句柄由
// defer Close 释放——Windows TempDir 清理依赖句柄归还）。
func mirrorEvents(t *testing.T, dir string) []any {
	t.Helper()
	s, err := sessionx.OpenStore(dir)
	if err != nil {
		t.Fatalf("open mirror %s: %v", dir, err)
	}
	defer s.Close()
	evs, err := s.ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror %s: %v", dir, err)
	}
	return evs
}

// assertNoOrphanTurns 镜像对账（Review Focus 孤儿回合钉）：TurnStarted ↔
// TurnDone 严格成对（1:1 计数 + 无跨对交错），P1 恢复门必须放行；引擎收口
// 留痕（EngineExited）必须存在——取消/级联走的合成收口路径证据。
func assertNoOrphanTurns(t *testing.T, dir string) {
	t.Helper()
	evs := mirrorEvents(t, dir)
	started, done, exited := 0, 0, 0
	open := false
	for _, ev := range evs {
		switch ev.(type) {
		case event.TurnStartedEvent:
			started++
			if open {
				t.Fatalf("overlapping TurnStarted in %s (1:1 invariant broken)", dir)
			}
			open = true
		case event.TurnDoneEvent:
			done++
			if !open {
				t.Fatalf("TurnDone without open turn in %s", dir)
			}
			open = false
		case event.EngineExitedEvent:
			exited++
		}
	}
	if started != done || open {
		t.Fatalf("orphan turns in %s: started=%d done=%d open=%v", dir, started, done, open)
	}
	if started > 0 && exited == 0 {
		t.Fatalf("engine close-out missing in %s (cancelled child must log EngineExited)", dir)
	}
	// P1 恢复门同款判定：无未收口回合（工作区不被永久拒绝的直接证据）
	s, err := sessionx.OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen %s: %v", dir, err)
	}
	defer s.Close()
	need, openTurn, err := control.RecoveryRequired(s)
	if err != nil || need {
		t.Fatalf("recovery gate must be clear in %s: need=%v openTurn=%d err=%v", dir, need, openTurn, err)
	}
}

// delegationEvent 在镜像事件流中找指定子会话的委派事件。
func delegationEvent[T any](t *testing.T, dir, subID string, pick func(T) string) *T {
	t.Helper()
	for _, ev := range mirrorEvents(t, dir) {
		if e, ok := ev.(T); ok && pick(e) == subID {
			return &e
		}
	}
	return nil
}

// spawnViaTool 以当前进程 env 的回调地址 spawn（调用前 t.Setenv 指向目标桥）。
func spawnViaTool(t *testing.T, task string) map[string]any {
	t.Helper()
	return opResult(t, callTool(t, "mulix_spawn", `{"task":`+mustJSON(task)+`}`))
}

func mustJSON(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// ---- 用例 ----

// TestSubagentConfigDefaults pins（brief Step 1）：[subagent] 默认 3/4。
func TestSubagentConfigDefaults(t *testing.T) {
	cfg := testConfig()
	if cfg.Subagent.MaxDepth != 3 || cfg.Subagent.MaxConcurrent != 4 {
		t.Fatalf("defaults: %+v", cfg.Subagent)
	}
}

// TestSubagentWiringEndToEnd pins（brief Step 1 + Task 3 评审移交）：
// FakeHost 主引擎接线 → mulix_spawn 工具面 → 回调桥 → SubagentFactory 造
// 真实镜像子会话（sessionx.Create 到 sessionsRoot）→ mulix_poll 回收
// completed + summary。断言：
//
//	①主镜像含 DelegationStarted（depth 0）与 DelegationCompleted（usage 非零）
//	②子镜像存在且 meta.parent_id == 主会话 id；三层委派链 depth 0/1/2 递增
//	③【Review Focus 孤儿回合钉】mulix_cancel 后子镜像无孤儿回合（成对 + 1:1）
//	  且恢复门放行；父会话 Close → 级联取消同断言
//	④【Task 3 移交】第四层 spawn 被拒（错误含 depth）——MaxDepth 可达性。
func TestSubagentWiringEndToEnd(t *testing.T) {
	opts, _, errOut := testOptions(t)
	cfg := testConfig()
	opts.Config = cfg
	root := opts.SessionsRoot

	boots := &bootCapture{}
	opts.HostFactory = func(boot engine.BootConfig) (engine.Host, error) {
		idx := boots.add(boot)
		switch {
		case idx == 0: // 主引擎：仅握手（spawn 由测试经工具面驱动）
			return engine.NewFakeHost(engine.FakeScript{}), nil
		case idx == 1: // A：完成回合（DelegationCompleted usage 断言靶）
			return engine.NewFakeHost(happyChildScript()), nil
		default: // H1/H2/H3/D/E（spawn 序确定）：挂起回合——运行中其桥存活，
			// 取消收口只能靠父侧级联（泵 engine_closed 合成收口）
			return engine.NewFakeHost(hangChildScript()), nil
		}
	}

	h := mustOpenSession(t, opts, "", nil)
	defer h.close()
	mainID := h.id

	// 前置：工厂已接线 → Boot Env 注入回调地址（真实注入点捕获——serve 子
	// 进程继承同一值）。
	cbMain := boots.env(0, "MULIX_SUBAGENT_CALLBACK")
	if cbMain == "" {
		t.Fatal("boot env must carry MULIX_SUBAGENT_CALLBACK（工厂已接线时）")
	}
	t.Setenv("MULIX_SUBAGENT_CALLBACK", cbMain)

	// 第一层：主会话 spawn A（真实镜像子会话）
	spawnA := spawnViaTool(t, "整理依赖清单")
	if spawnA["ok"] != true {
		t.Fatalf("spawn A = %v (stderr: %s)", spawnA, errOut.String())
	}
	subA, _ := spawnA["sub_session_id"].(string)
	if !strings.HasPrefix(subA, mainID+"-sub-") {
		t.Fatalf("sub id = %q, want <parent>-sub-<n>", subA)
	}

	// mulix_poll → completed + summary（FR-102 最终消息回收 e2e 面）
	var pollA map[string]any
	waitFor(t, "A completed", func() bool {
		pollA = opResult(t, callTool(t, "mulix_poll", `{"sub_session_id":"`+subA+`"}`))
		return pollA["status"] == "completed"
	})
	if pollA["summary"] == "" {
		t.Fatalf("completed poll must carry summary: %v", pollA)
	}

	// ②子镜像存在且 meta.parent_id == 主会话 id
	childDir := sessionx.SessionDir(root, subA)
	if _, err := os.Stat(filepath.Join(childDir, "events.jsonl")); err != nil {
		t.Fatalf("child mirror missing: %v", err)
	}
	cs, err := sessionx.OpenStore(childDir)
	if err != nil {
		t.Fatalf("open child mirror: %v", err)
	}
	childMeta, err := cs.ReadMeta()
	_ = cs.Close()
	if err != nil {
		t.Fatalf("read child meta: %v", err)
	}
	if childMeta["parent_id"] != mainID {
		t.Fatalf("child meta parent_id = %v, want %s", childMeta["parent_id"], mainID)
	}

	// ①主镜像：DelegationStarted（depth 0）+ DelegationCompleted（usage 非零）
	stA := delegationEvent(t, h.store.Dir(), subA, func(e event.DelegationStartedEvent) string { return e.SubSessionID })
	if stA == nil {
		t.Fatalf("main mirror missing DelegationStarted for %s", subA)
	}
	if stA.Depth != 0 || stA.Task != "整理依赖清单" {
		t.Fatalf("DelegationStarted = %+v, want depth 0 + task", *stA)
	}
	cpA := delegationEvent(t, h.store.Dir(), subA, func(e event.DelegationCompletedEvent) string { return e.SubSessionID })
	if cpA == nil {
		t.Fatalf("main mirror missing DelegationCompleted for %s", subA)
	}
	if cpA.TotalTokens <= 0 || cpA.Summary == "" {
		t.Fatalf("DelegationCompleted usage summary = %+v, want nonzero tokens + summary", *cpA)
	}

	// 第二层：H1（挂起回合——子代理运行中其回调桥存活，孙代 spawn 与真实
	// 引擎回合中调工具同路径；已完成子代理的桥随 reap 收口关闭）
	t.Setenv("MULIX_SUBAGENT_CALLBACK", cbMain)
	spawnH1 := spawnViaTool(t, "挂起的父任务")
	if spawnH1["ok"] != true {
		t.Fatalf("spawn H1 = %v", spawnH1)
	}
	subH1, _ := spawnH1["sub_session_id"].(string)

	// H1 的桥 → spawn H2（depth 1）
	t.Setenv("MULIX_SUBAGENT_CALLBACK", boots.env(2, "MULIX_SUBAGENT_CALLBACK"))
	spawnH2 := spawnViaTool(t, "第二层任务")
	if spawnH2["ok"] != true {
		t.Fatalf("spawn H2 = %v", spawnH2)
	}
	subH2, _ := spawnH2["sub_session_id"].(string)
	stH2 := delegationEvent(t, sessionx.SessionDir(root, subH1), subH2, func(e event.DelegationStartedEvent) string { return e.SubSessionID })
	if stH2 == nil || stH2.Depth != 1 {
		t.Fatalf("H1 mirror DelegationStarted for H2 = %+v, want depth 1", stH2)
	}
	if m := readMetaDir(t, sessionx.SessionDir(root, subH2)); m["parent_id"] != subH1 {
		t.Fatalf("H2 meta parent_id = %v, want %s", m["parent_id"], subH1)
	}

	// H2 的桥 → spawn H3（depth 2）
	t.Setenv("MULIX_SUBAGENT_CALLBACK", boots.env(3, "MULIX_SUBAGENT_CALLBACK"))
	spawnH3 := spawnViaTool(t, "第三层任务")
	if spawnH3["ok"] != true {
		t.Fatalf("spawn H3 = %v", spawnH3)
	}
	subH3, _ := spawnH3["sub_session_id"].(string)
	stH3 := delegationEvent(t, sessionx.SessionDir(root, subH2), subH3, func(e event.DelegationStartedEvent) string { return e.SubSessionID })
	if stH3 == nil || stH3.Depth != 2 {
		t.Fatalf("H2 mirror DelegationStarted for H3 = %+v, want depth 2", stH3)
	}
	if m := readMetaDir(t, sessionx.SessionDir(root, subH3)); m["parent_id"] != subH2 {
		t.Fatalf("H3 meta parent_id = %v, want %s", m["parent_id"], subH2)
	}

	// ④【Task 3 移交】第四层 spawn（H3 的镜像链 3 深 → depth 3 == MaxDepth）
	// 被拒——错误含 depth（MaxDepth 限额经真实深度来源可达，非死代码）。
	t.Setenv("MULIX_SUBAGENT_CALLBACK", boots.env(4, "MULIX_SUBAGENT_CALLBACK"))
	reject := spawnViaTool(t, "第四层任务")
	if reject["ok"] != false {
		t.Fatalf("fourth-layer spawn must be rejected: %v", reject)
	}
	if !strings.Contains(fmt.Sprint(reject["error"]), "depth") {
		t.Fatalf("rejection error must mention depth: %v", reject)
	}

	// ③【Review Focus 孤儿回合钉】mulix_cancel H1 → 级联取消 H2/H3——
	// 三层镜像全部无孤儿回合（成对 + 1:1）且恢复门放行。
	t.Setenv("MULIX_SUBAGENT_CALLBACK", cbMain)
	cancelH1 := opResult(t, callTool(t, "mulix_cancel", `{"sub_session_id":"`+subH1+`"}`))
	if cancelH1["ok"] != true {
		t.Fatalf("cancel H1 = %v", cancelH1)
	}
	for _, d := range []string{subH1, subH2, subH3} {
		dir := sessionx.SessionDir(root, d)
		waitFor(t, d+" reconciled", func() bool { return mirrorReconciled(t, dir) })
		assertNoOrphanTurns(t, dir)
	}
	cnH1 := delegationEvent(t, h.store.Dir(), subH1, func(e event.DelegationCancelledEvent) string { return e.SubSessionID })
	if cnH1 == nil || cnH1.Reason != "requested via mulix_cancel" {
		t.Fatalf("main mirror DelegationCancelled for H1 = %+v", cnH1)
	}
	cnH2 := delegationEvent(t, sessionx.SessionDir(root, subH1), subH2, func(e event.DelegationCancelledEvent) string { return e.SubSessionID })
	if cnH2 == nil || cnH2.Reason != "manager_closed" {
		t.Fatalf("H1 mirror DelegationCancelled for H2 = %+v, want manager_closed cascade", cnH2)
	}
	cnH3 := delegationEvent(t, sessionx.SessionDir(root, subH2), subH3, func(e event.DelegationCancelledEvent) string { return e.SubSessionID })
	if cnH3 == nil || cnH3.Reason != "manager_closed" {
		t.Fatalf("H2 mirror DelegationCancelled for H3 = %+v, want manager_closed cascade", cnH3)
	}

	// ③mulix_cancel 单层：主会话 spawn D（挂起）→ 取消 → 无孤儿
	spawnD := spawnViaTool(t, "被取消的长任务")
	if spawnD["ok"] != true {
		t.Fatalf("spawn D = %v", spawnD)
	}
	subD, _ := spawnD["sub_session_id"].(string)
	cancelD := opResult(t, callTool(t, "mulix_cancel", `{"sub_session_id":"`+subD+`"}`))
	if cancelD["ok"] != true {
		t.Fatalf("cancel D = %v", cancelD)
	}
	dirD := sessionx.SessionDir(root, subD)
	waitFor(t, "D reconciled", func() bool { return mirrorReconciled(t, dirD) })
	assertNoOrphanTurns(t, dirD)
	cnD := delegationEvent(t, h.store.Dir(), subD, func(e event.DelegationCancelledEvent) string { return e.SubSessionID })
	if cnD == nil || cnD.Reason != "requested via mulix_cancel" {
		t.Fatalf("main mirror DelegationCancelled for D = %+v", cnD)
	}

	// ③父会话 Close → 级联取消，同断言（reason=manager_closed）
	spawnE := spawnViaTool(t, "级联取消的长任务")
	if spawnE["ok"] != true {
		t.Fatalf("spawn E = %v", spawnE)
	}
	subE, _ := spawnE["sub_session_id"].(string)
	h.close() // 父会话收口 → Manager.Close 级联取消运行中子代理
	dirE := sessionx.SessionDir(root, subE)
	waitFor(t, "E reconciled", func() bool { return mirrorReconciled(t, dirE) })
	assertNoOrphanTurns(t, dirE)
	cnE := delegationEvent(t, h.store.Dir(), subE, func(e event.DelegationCancelledEvent) string { return e.SubSessionID })
	if cnE == nil || cnE.Reason != "manager_closed" {
		t.Fatalf("main mirror DelegationCancelled for E = %+v", cnE)
	}
}

// mirrorReconciled 报告镜像是否已无未收口回合（恢复门同款判定）。
func mirrorReconciled(t *testing.T, dir string) bool {
	t.Helper()
	s, err := sessionx.OpenStore(dir)
	if err != nil {
		return false
	}
	defer s.Close()
	need, _, err := control.RecoveryRequired(s)
	return err == nil && !need
}

func readMetaDir(t *testing.T, dir string) map[string]any {
	t.Helper()
	s, err := sessionx.OpenStore(dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	defer s.Close()
	meta, err := s.ReadMeta()
	if err != nil {
		t.Fatalf("read meta %s: %v", dir, err)
	}
	return meta
}

// TestSubagentDisabledWhenFactoryNil pins（Task 3 裁定）：Options.SubagentFactory
// == nil → Boot Env 无 MULIX_SUBAGENT_CALLBACK，boot 面与 P1 逐字节一致
// （cache.Compose 独立重算为 P1 基线；注册表全局态跨测试共享，不做注册缺席
// 断言）。对照面：工厂非空时 Env 携带回调地址且 boot 面同样逐字节不变
// （子代理面只走 Boot Env 通道，绝不入 mcp-config——SC-003 字节稳定）。
func TestSubagentDisabledWhenFactoryNil(t *testing.T) {
	for _, tc := range []struct {
		name    string
		factory subagent.SessionFactory
		wantCB  bool
	}{
		{name: "factory nil = disabled", factory: nil, wantCB: false},
		{name: "factory set = enabled", factory: func(context.Context, string) (subagent.SessionHandle, error) {
			return nil, errors.New("stub factory")
		}, wantCB: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := sessionx.Create(root, sessionx.Options{ID: "s-dis", Workspace: "w"})
			if err != nil {
				t.Fatalf("create store: %v", err)
			}
			defer store.Close()
			meter, err := cache.NewMeter(cache.MeterOptions{Root: root, SessionID: "s-dis",
				Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}})
			if err != nil {
				t.Fatalf("meter: %v", err)
			}
			defer meter.Close()
			policy, err := guard.NewPolicy(guard.PresetDefault, nil)
			if err != nil {
				t.Fatalf("policy: %v", err)
			}
			gate, err := guard.NewGate(guard.GateOptions{Policy: policy})
			if err != nil {
				t.Fatalf("gate: %v", err)
			}
			reg := registry.NewServer(registry.Options{Name: "mulix", Version: "test",
				SelfCommand: []string{"mulix.exe", "mcp", "serve"}})
			var boot engine.BootConfig
			c, err := control.NewController(control.Options{
				HostFactory: func(b engine.BootConfig) (engine.Host, error) {
					boot = b
					return engine.NewFakeHost(engine.FakeScript{}), nil
				},
				Sink: discardSink{}, Store: store, Meter: meter, Gate: gate,
				Config: *testConfig(), Workspace: "w", Registry: reg,
				SubagentFactory: tc.factory,
				Kernel:          kernelCtxWithLoop(), // 021 FR-102:loop 权威 ctx(测试注入缝)
			})
			if err != nil {
				t.Fatalf("controller: %v", err)
			}
			defer func() { _ = c.Close(context.Background()) }()
			if err := c.NewSession(context.Background()); err != nil {
				t.Fatalf("new session: %v", err)
			}
			hasCB := false
			for _, e := range boot.Env {
				if strings.HasPrefix(e, "MULIX_SUBAGENT_CALLBACK=") {
					hasCB = true
				}
			}
			if hasCB != tc.wantCB {
				t.Fatalf("MULIX_SUBAGENT_CALLBACK in env = %v, want %v (env=%v)", hasCB, tc.wantCB, boot.Env)
			}
			// boot 面与 P1 逐字节一致：与 cache.Compose 独立重算（P1 组装器，
			// 不知道子代理存在）对表。
			art, err := cache.Compose(*testConfig(), "w", reg)
			if err != nil {
				t.Fatalf("compose baseline: %v", err)
			}
			if boot.AppendSystemPrompt != art.AppendSystemPrompt ||
				boot.MCPConfigJSON != art.MCPConfigJSON ||
				strings.Join(boot.AllowedTools, ",") != strings.Join(art.AllowedTools, ",") ||
				boot.PermissionPromptTool != "mcp__mulix__permission" {
				t.Fatalf("boot face diverges from P1 baseline:\nboot: %+v\nbase: %+v", boot, art)
			}
		})
	}
}
