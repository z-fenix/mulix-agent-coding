// host_test.go——sessionHost 状态机钉子(2026-10-18-web-ui-mvp Task 5):
// claim 交互权/release 宽限/ gone 一次性与重连取消/idle-close 扫描。
// 宽限与扫描节拍经包级 var 注入收缩,绝不真实睡眠 10s/10min。
package serve

import (
	"context"
	"testing"
	"time"

	"mulix/internal/agentloop"
	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/guard"
	"mulix/internal/kernel"
	"mulix/internal/sessionx"
)

// ---- 最小装配桩(e2e newEnv 同款形态,包内自实现——测试基建不跨包复用) ----

// kernelCtxWithLoop 返回挂载 loop 插件的最小根 ctx(021 FR-102 测试注入缝;
// 形态同 e2e/control 测试面同名 helper)。
func kernelCtxWithLoop() *kernel.Context {
	root := kernel.NewContext(nil)
	if err := root.Mount(agentloop.NewPlugin()); err != nil {
		panic(err)
	}
	return root
}

// testAssemble 是本任务的最小装配桩:FakeHost × 真实 control 栈(policy+Gate
// 桥接 / Meter / sessionx 镜像),Sink = host 的 webSink。resume 路径同闭包
// 驱动(Task 6 换真实装配)。
func testAssemble(t *testing.T) assembleFn {
	t.Helper()
	return func(ctx context.Context, h *sessionHost) (*control.Controller, *control.UserApprovalBridge, error) {
		root := t.TempDir()
		store, err := sessionx.Create(root, sessionx.Options{ID: h.ID, Workspace: h.Workspace})
		if err != nil {
			return nil, nil, err
		}
		t.Cleanup(func() { _ = store.Close() })
		meter, err := cache.NewMeter(cache.MeterOptions{
			Root: root, SessionID: h.ID,
			Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
		})
		if err != nil {
			return nil, nil, err
		}
		t.Cleanup(func() { _ = meter.Close() })
		cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
		if err != nil {
			return nil, nil, err
		}
		policy, err := guard.NewPolicy(guard.Preset(cfg.Permissions.Preset), nil)
		if err != nil {
			return nil, nil, err
		}
		bridge := control.NewUserApprovalBridge()
		gate, err := guard.NewGate(guard.GateOptions{Policy: policy, Approver: bridge, Timeout: 2 * time.Second})
		if err != nil {
			return nil, nil, err
		}
		ctrl, err := control.NewController(control.Options{
			HostFactory: func(engine.BootConfig) (engine.Host, error) { return engine.NewFakeHost(engine.FakeScript{}), nil },
			Sink:        h.sink, Store: store, Meter: meter,
			Gate: gate, Approvals: bridge,
			Config: *cfg, Workspace: h.Workspace,
			Kernel: kernelCtxWithLoop(),
		})
		if err != nil {
			return nil, nil, err
		}
		if h.resume {
			err = ctrl.Resume(ctx, "stub-engine-session")
		} else {
			err = ctrl.NewSession(ctx)
		}
		if err != nil {
			return nil, nil, err
		}
		return ctrl, bridge, nil
	}
}

// newTestHost 构造桩装配的 host;close 幂等,清理与 sweep 双重调用无碍。
func newTestHost(t *testing.T, id string) *sessionHost {
	t.Helper()
	h, err := newSessionHost(context.Background(), id, "w", false, testAssemble(t))
	if err != nil {
		t.Fatalf("newSessionHost(%s): %v", id, err)
	}
	t.Cleanup(func() { _ = h.close(context.Background()) })
	return h
}

// ---- 状态机 ----

// TestClaimInteractiveThenReadonly 钉:首个 claim → interactive;他人 claim
// → readonly;同 client 重复 claim 幂等仍 interactive。
func TestClaimInteractiveThenReadonly(t *testing.T) {
	h := newTestHost(t, "s-claim")
	role, err := h.claim("c1")
	if err != nil || role != roleInteractive {
		t.Fatalf("c1 claim = (%v, %v), want (interactive, nil)", role, err)
	}
	role, err = h.claim("c2")
	if err != nil || role != roleReadonly {
		t.Fatalf("c2 claim = (%v, %v), want (readonly, nil)", role, err)
	}
	if role, _ = h.claim("c1"); role != roleInteractive {
		t.Fatalf("c1 re-claim = %v, want interactive(幂等)", role)
	}
}

// TestReleaseThenReclaim 钉:非持有者 release 是 no-op(不进入宽限);持有者
// release 后他人 claim 即接管 interactive。
func TestReleaseThenReclaim(t *testing.T) {
	h := newTestHost(t, "s-release")
	if _, err := h.claim("c1"); err != nil {
		t.Fatalf("c1 claim: %v", err)
	}
	h.release("c2") // 非持有者:no-op
	role, err := h.claim("c2")
	if err != nil || role != roleReadonly {
		t.Fatalf("非持有者 release 后 c2 claim = (%v, %v), want (readonly, nil)", role, err)
	}
	h.release("c1") // 持有者:清持有 + 宽限开始
	role, err = h.claim("c2")
	if err != nil || role != roleInteractive {
		t.Fatalf("c1 release 后 c2 claim = (%v, %v), want (interactive, nil)", role, err)
	}
}

// TestInteractiveGoneAfterGrace 钉:release 后宽限内 gone 未关;宽限到点
// 关闭(一次性——通道保持关闭,若实现二次 close 本测试进程即 panic)。
func TestInteractiveGoneAfterGrace(t *testing.T) {
	old := gracePeriod
	gracePeriod = 10 * time.Millisecond
	defer func() { gracePeriod = old }()
	h := newTestHost(t, "s-gone")
	if _, err := h.claim("c1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	h.release("c1")
	select {
	case <-h.interactiveGone():
		t.Fatal("宽限内 gone 已关闭(计时器未注入或未挂)")
	default:
	}
	select {
	case <-h.interactiveGone():
	case <-time.After(2 * time.Second):
		t.Fatal("宽限超时后 gone 未关闭")
	}
}

// TestGraceReconnectCancelsGone 钉:宽限内重连(同 client reclaim 或不同
// client 接管)取消离线计时,gone 永不关闭——重连即交互端就位,离线取消器
// 不得误伤其待审请求。
func TestGraceReconnectCancelsGone(t *testing.T) {
	old := gracePeriod
	gracePeriod = 10 * time.Millisecond
	defer func() { gracePeriod = old }()
	h := newTestHost(t, "s-reconnect")
	if _, err := h.claim("c1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	h.release("c1")
	if _, err := h.claim("c1"); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	select {
	case <-h.interactiveGone():
		t.Fatal("宽限内重连后 gone 仍被关闭(计时器未取消)")
	case <-time.After(100 * time.Millisecond): // 10x 宽限
	}
	// 不同 client 在宽限内 claim 同样取消计时器
	h.release("c1")
	if _, err := h.claim("c2"); err != nil {
		t.Fatalf("c2 claim: %v", err)
	}
	select {
	case <-h.interactiveGone():
		t.Fatal("c2 宽限内接管后 gone 仍被关闭")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestNewSessionHostResumeStub 钉:resume 路径经同一条 assemble 闭包装配
// 成功(Task 6 真实装配的形状前奏)。
func TestNewSessionHostResumeStub(t *testing.T) {
	h, err := newSessionHost(context.Background(), "s-resume", "w", true, testAssemble(t))
	if err != nil {
		t.Fatalf("newSessionHost(resume): %v", err)
	}
	t.Cleanup(func() { _ = h.close(context.Background()) })
	if h.ctrl == nil || h.sink == nil || h.bridge == nil {
		t.Fatal("ctrl/sink/bridge 未装配")
	}
	if h.interactiveGone() == nil {
		t.Fatal("gone 通道未就绪")
	}
}

// ---- hostManager ----

// TestIdleCloseClosesHost 钉:空闲(无持有者、回合未在飞)超 idle 的 host
// 被扫描 close 并摘除;有持有者的 host 不被误扫;release 后进入空闲再被扫。
func TestIdleCloseClosesHost(t *testing.T) {
	old := idleSweepInterval
	idleSweepInterval = 5 * time.Millisecond
	defer func() { idleSweepInterval = old }()

	m := newHostManager(30 * time.Millisecond)
	defer m.Close()

	held := newTestHost(t, "s-idle-held")
	if _, err := held.claim("c1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	m.add(held)
	idle := newTestHost(t, "s-idle-free")
	m.add(idle)

	waitRemoved := func(id string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := m.get(id); !ok {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("host %s 未被 idle-close 移除", id)
	}
	waitRemoved("s-idle-free")
	select {
	case <-idle.closedCh:
	default:
		t.Fatal("manager 摘除时 host 未实际 close")
	}
	if _, ok := m.get("s-idle-held"); !ok {
		t.Fatal("有持有者的 host 被误 idle-close")
	}
	held.release("c1")
	waitRemoved("s-idle-held")
}

// TestManagerListSummaries 钉:list 仅含本 manager 在册 host,条目带
// ID/Workspace/Open,status 自 Snapshot 派生( FakeHost init 到达时序内
// ready|not_ready 二选一)。
func TestManagerListSummaries(t *testing.T) {
	m := newHostManager(time.Hour)
	defer m.Close()
	h := newTestHost(t, "s-list")
	m.add(h)
	got := m.list()
	if len(got) != 1 {
		t.Fatalf("list len = %d, want 1(仅本 manager 在册)", len(got))
	}
	s := got[0]
	if s.ID != "s-list" || s.Workspace != "w" || !s.Open {
		t.Fatalf("summary = %+v, want ID/Workspace/Open 对位", s)
	}
	if s.Status != "ready" && s.Status != "not_ready" {
		t.Fatalf("status = %q, want ready|not_ready", s.Status)
	}
	if s.LastActivity.IsZero() {
		t.Fatal("LastActivity 未初始化")
	}
}
