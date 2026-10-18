// assemble_test.go——serve 真实装配钉子(2026-10-18-web-ui-mvp Task 6):
// FakeHost × 真实 guard/cache/sessionx/control 栈(对齐 internal/e2e 惯例,包内
// 自实现——测试基建不跨包复用):happy 回合镜像先于 sink、resume 恢复门
// fail-closed、引擎漂移错误上抛(FR-013)、续链记账续号。
package serve

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/sessionx"
)

// testParams 组真实装配参数:默认分层配置(e2e 同款——Paths{} 空加载即纯默认值)
// + FakeHost 回放 happy-turn fixture(assembleParams.HostFactory 测试缝)。root =
// TempDir:镜像池 root/sessions、记账 root(与 assembleReal 的 UsageRoot 缺省
// 推导同构,显式传等价值保证确定性)。
func testParams(t *testing.T, root string, mutate func(*assembleParams)) assembleParams {
	t.Helper()
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	fixture, err := engine.FixtureScript("../../fixtures/engine/happy-turn.jsonl")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	p := assembleParams{
		Cfg:          cfg,
		SessionsRoot: filepath.Join(root, "sessions"),
		UsageRoot:    root,
		HostFactory: func(engine.BootConfig) (engine.Host, error) {
			return engine.NewFakeHost(fixture), nil
		},
	}
	if mutate != nil {
		mutate(&p)
	}
	return p
}

// waitTurnDone 从订阅流收集事件直至 TurnDone 到达(收口后 pump 不再发布本回合
// 事件——返回序列即本回合完整 sink 视图)。超时 5s。
func waitTurnDone(t *testing.T, ch <-chan any) []any {
	t.Helper()
	var got []any
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("sink 提前关闭(已收 %d 事件)", len(got))
			}
			got = append(got, ev)
			if _, isDone := ev.(event.TurnDoneEvent); isDone {
				return got
			}
		case <-deadline:
			t.Fatalf("5s 内未观察到 TurnDone(已收 %d 事件)", len(got))
		}
	}
}

// kindsOf 把事件序列映射为镜像 kind 序列(sessionx.EventKindOf——与镜像/总线
// 共用同一映射,不另造第二套)。
func kindsOf(t *testing.T, events []any) []string {
	t.Helper()
	kinds := make([]string, 0, len(events))
	for _, ev := range events {
		k, err := sessionx.EventKindOf(ev)
		if err != nil {
			t.Fatalf("event kind: %v", err)
		}
		kinds = append(kinds, string(k))
	}
	return kinds
}

// assertSinkMirrorsStoreTail 校验 sink 序列与镜像事件序尾部逐一相等(mirror-first
// 不变量的系统边界观测,对齐 e2e assertSinkMirrorsStore 形态:先持久化后发布由
// control.publish 结构保证——Submit 返回后两侧序列整体对账;sink 只含订阅后的
// 事件,镜像含全程,故对尾部)。
func assertSinkMirrorsStoreTail(t *testing.T, store *sessionx.Store, sink []any) {
	t.Helper()
	storeEvents, err := store.ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if len(sink) > len(storeEvents) {
		t.Fatalf("sink 序列(%d)长于镜像(%d)——镜像先行破坏", len(sink), len(storeEvents))
	}
	tail := storeEvents[len(storeEvents)-len(sink):]
	want, got := kindsOf(t, tail), kindsOf(t, sink)
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("序列[%d]: sink %s != 镜像 %s\nsink=%v\n镜像=%v", i, got[i], want[i], got, want)
		}
	}
}

// TestAssembleNewSessionHappyTurn 钉:assembleReal 全栈装配 → Submit 回合收口;
// 镜像序与 sink 序逐一相等(1:1),Usage 先于 TurnDone(记账后收口),回合非错误。
func TestAssembleNewSessionHappyTurn(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	h, err := newSessionHost(ctx, "s-assemble-new", "w", false, assembleReal, withAssembly(testParams(t, root, nil)))
	if err != nil {
		t.Fatalf("newSessionHost: %v", err)
	}
	t.Cleanup(func() { _ = h.close(ctx) })
	if h.ctrl == nil || h.bridge == nil {
		t.Fatal("ctrl/bridge 未装配(attended 审批桥必须就位)")
	}
	ch, unsub := h.sink.Subscribe()
	defer unsub()
	if err := h.ctrl.Submit(ctx, "hi"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	sinkEvents := waitTurnDone(t, ch)
	assertSinkMirrorsStoreTail(t, h.ctrl.Mirror(), sinkEvents)
	usageIdx, doneIdx := -1, -1
	for i, ev := range sinkEvents {
		switch ev.(type) {
		case event.UsageEvent:
			usageIdx = i
		case event.TurnDoneEvent:
			doneIdx = i
		}
	}
	if usageIdx == -1 || doneIdx == -1 || usageIdx > doneIdx {
		t.Fatalf("Usage/TurnDone 顺序异常: usage=%d done=%d", usageIdx, doneIdx)
	}
	if d := sinkEvents[doneIdx].(event.TurnDoneEvent); d.IsError {
		t.Fatalf("happy 回合不得失败: subtype=%s", d.Subtype)
	}
}

// TestAssembleResumeRecoveryGate 钉:open-turn 镜像的 resume 走 serve 恢复门被拒
// (而非裸 ctrl.Resume——后者本就合成 resume_reconciled 不拒),错误明示未收口
// 回合;拒绝路径镜像零改写(无合成收口、事件数不变)。
func TestAssembleResumeRecoveryGate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := testParams(t, root, nil)
	const crashed = "s-assemble-crash"
	store, err := sessionx.Create(p.SessionsRoot, sessionx.Options{ID: crashed, Workspace: "w"})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	// 崩溃痕迹:TurnStarted 后进程死亡(无 TurnDone);引擎 id 已 init(e2e 场景 5 同款)。
	if err := store.Append(event.TurnStartedEvent{Seq: 1, SessionID: crashed}); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := store.SetEngineID("e-crash"); err != nil {
		t.Fatalf("seed engine id: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
	seeded, err := sessionx.OpenStore(sessionx.SessionDir(p.SessionsRoot, crashed))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	before, err := seeded.ReadAllEvents()
	if err != nil {
		t.Fatalf("seed read: %v", err)
	}
	if err := seeded.Close(); err != nil {
		t.Fatalf("seed close 2: %v", err)
	}

	_, err = newSessionHost(ctx, crashed, "w", true, assembleReal, withAssembly(p))
	if err == nil {
		t.Fatal("open-turn 镜像的 resume 必须被 serve 恢复门拒绝")
	}
	if !strings.Contains(err.Error(), "未收口") {
		t.Fatalf("错误须明示未收口回合: %v", err)
	}
	// 镜像未被静默改写:事件数不变、无 resume_reconciled 合成收口。
	reopened, err := sessionx.OpenStore(sessionx.SessionDir(p.SessionsRoot, crashed))
	if err != nil {
		t.Fatalf("verify reopen: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	after, err := reopened.ReadAllEvents()
	if err != nil {
		t.Fatalf("verify read: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("拒绝路径镜像被改写: before=%d after=%d", len(before), len(after))
	}
	for _, ev := range after {
		if d, ok := ev.(event.TurnDoneEvent); ok {
			t.Fatalf("恢复门拒绝路径不得合成收口: subtype=%s", d.Subtype)
		}
	}
}

// TestAssembleEngineDriftRefused 钉(FR-013):HostFactory 拒绝(引擎漂移形态)
// → 错误原样上抛供 UI 呈现,绝不静默降级。
func TestAssembleEngineDriftRefused(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := testParams(t, root, func(p *assembleParams) {
		p.HostFactory = func(engine.BootConfig) (engine.Host, error) {
			return nil, errors.New("engine.lock drift: claude 2.1.291 != pinned 2.1.100")
		}
	})
	_, err := newSessionHost(ctx, "s-assemble-drift", "w", false, assembleReal, withAssembly(p))
	if err == nil {
		t.Fatal("HostFactory 漂移错误必须上抛")
	}
	if !strings.Contains(err.Error(), "engine.lock drift") {
		t.Fatalf("错误须携带漂移原因: %v", err)
	}
}

// TestAssembleResumeHappyPath 钉:过门 resume 通路——镜像无 open turn →
// OpenStore + meta 引擎 id + ctrl.Resume;记账续号(cache.NextTurnNumber →
// Meter TurnOffset)使续链回合记账接续历史(不撞号——offset 缺失时两把 Meter
// 都写 Turn 1,Reconcile 合并后 Turns=1,本断言即红)。
func TestAssembleResumeHappyPath(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	p := testParams(t, root, nil)
	const id = "s-assemble-resume"
	store, err := sessionx.Create(p.SessionsRoot, sessionx.Options{ID: id, Workspace: "w"})
	if err != nil {
		t.Fatalf("seed create: %v", err)
	}
	// 已收口历史回合(成对)+ 引擎 id + 记账 Turn 1。
	if err := store.Append(event.TurnStartedEvent{Seq: 1, SessionID: id}); err != nil {
		t.Fatalf("seed started: %v", err)
	}
	if err := store.Append(event.TurnDoneEvent{Seq: 2, Turn: 1}); err != nil {
		t.Fatalf("seed done: %v", err)
	}
	if err := store.SetEngineID("e-resume"); err != nil {
		t.Fatalf("seed engine id: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
	seed, err := cache.NewMeter(cache.MeterOptions{
		Root: p.UsageRoot, SessionID: id,
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("seed meter: %v", err)
	}
	if _, err := seed.Observe(cache.TurnUsage{InputTokens: 10, OutputTokens: 5}); err != nil {
		t.Fatalf("seed observe: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed meter close: %v", err)
	}

	h, err := newSessionHost(ctx, id, "w", true, assembleReal, withAssembly(p))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	t.Cleanup(func() { _ = h.close(ctx) })
	ch, unsub := h.sink.Subscribe()
	defer unsub()
	if err := h.ctrl.Submit(ctx, "again"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	sinkEvents := waitTurnDone(t, ch)
	assertSinkMirrorsStoreTail(t, h.ctrl.Mirror(), sinkEvents)
	report, err := cache.ReadUsageReport(p.UsageRoot, id)
	if err != nil {
		t.Fatalf("usage report: %v", err)
	}
	if report.Turns != 2 {
		t.Fatalf("usage turns = %d, want 2(记账续号必须接续历史)", report.Turns)
	}
}
