package control

import (
	"context"
	"testing"
	"time"

	"mulix/internal/engine"
)

// ---- 027 引擎就绪态（FR-002/004/005）——whitebox 状态机测试 ----

func readinessTestController(t *testing.T) *Controller {
	t.Helper()
	c := newTestController(t, engine.NewFakeHost(engine.FakeScript{}), &recordingSink{})
	if err := c.NewSession(context.Background()); err != nil {
		t.Fatalf("new session: %v", err)
	}
	// 等 FakeHost 合成 init 被泵消费（READY 定态）——后续用例按需白盒覆盖。
	waitReady(t, c)
	return c
}

func waitReady(t *testing.T, c *Controller) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		st := c.readyState
		c.mu.Unlock()
		if st == readyOK {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("engine never reached READY")
}

// TestReadyStateMachine pins（027 FR-002/004）：NOT_READY → READY（init 到达，
// 对账由引擎 route 完成——control 见 init 即 READY）/ FAILED（对账失败=引擎
// refused exit、pre-init 秒退、兜底超时）。
func TestReadyStateMachine(t *testing.T) {
	c := readinessTestController(t)
	c.mu.Lock()
	c.readyState = readyNotReady // 白盒：回拨到 NOT_READY（真实 CLI 的 init 迟到语义）
	c.mu.Unlock()
	if c.readyState != readyNotReady {
		t.Fatalf("initial readyState = %q, want NOT_READY", c.readyState)
	}

	// init 到达（对账在引擎 route 完成——control 见 init 即 READY）。
	c.onInit(engine.NewFakeHost(engine.FakeScript{}), initEvent("e-1"))
	if c.readyState != readyOK {
		t.Fatalf("after init readyState = %q, want READY", c.readyState)
	}
	if s := c.Snapshot(); s.Ready != "READY" {
		t.Fatalf("Snapshot.Ready = %q, want READY", s.Ready)
	}
}

// TestRefusedExitFailed pins：pre-READY 的 refused exit（引擎对账拒绝自终止）
// → FAILED（快照可见）——从「黑屏 30s 后拒绝」变为「秒级 FAILED」。
func TestRefusedExitFailed(t *testing.T) {
	c := readinessTestController(t)
	c.mu.Lock()
	c.readyState = readyNotReady // 白盒：pre-READY 场景
	c.mu.Unlock()
	c.onExit(engine.Event{Kind: engine.KindExit,
		Exit: &engine.ExitInfo{Code: -1, Reason: "refused: engine: version drift: lock 2.1.301, actual 9.9.9"}})
	if c.readyState != readyFailed {
		t.Fatalf("refused exit pre-READY must FAILED, got %q", c.readyState)
	}
	if s := c.Snapshot(); s.Ready != "FAILED" {
		t.Fatalf("Snapshot.Ready = %q, want FAILED", s.Ready)
	}
}

// TestFallbackBudgetFromFirstSubmit pins（Ruling-3 修订）：兜底计时器从首次
// Submit 起算（非 spawn）、注入通道触发、NOT_READY 才置 FAILED、READY 不受影响。
func TestFallbackBudgetFromFirstSubmit(t *testing.T) {
	fired := make(chan time.Time, 1)
	c := readinessTestController(t)
	c.mu.Lock()
	c.readyState = readyNotReady // 白盒：NOT_READY 场景（FakeHost 合成 init 已 READY）
	c.mu.Unlock()
	c.fallbackAfter = func(time.Duration) <-chan time.Time { return fired }

	// 首次 Submit 武装兜底（fake 无 Turns——Send 报错可接受，武装先于 Send）。
	err := c.Submit(context.Background(), "你好")
	if err != nil {
		t.Logf("DBG submit err: %v", err)
	}
	if !c.fallbackArmed {
		t.Fatalf("first submit must arm the fallback timer (submit err=%v, readyState=%q)", err, c.readyState)
	}

	// READY 到达后兜底触发不再置 FAILED（init 已解除）。
	c.onInit(engine.NewFakeHost(engine.FakeScript{}), initEvent("e-1"))
	fired <- time.Now()
	time.Sleep(50 * time.Millisecond) // AfterFunc goroutine 落定
	if c.readyState != readyOK {
		t.Fatalf("READY must be sticky against late fallback fire, got %q", c.readyState)
	}

	// 全新 controller：白盒覆盖 NOT_READY 后兜底触发 → FAILED。
	c2 := readinessTestController(t)
	c2.mu.Lock()
	c2.readyState = readyNotReady
	c2.mu.Unlock()
	c2.fallbackAfter = func(time.Duration) <-chan time.Time { return fired }
	_ = c2.Submit(context.Background(), "你好")
	fired <- time.Now()
	time.Sleep(50 * time.Millisecond)
	if c2.readyState != readyFailed {
		t.Fatalf("NOT_READY fallback fire must FAILED, got %q", c2.readyState)
	}
}

// TestFallbackBudgetValue pins：预算 = 60s（对齐 SDK initialize 预算——
// query.py:302-304「MCP 服务器可能需要时间启动」）。
func TestFallbackBudgetValue(t *testing.T) {
	if c := readinessTestController(t); c.fallbackBudget != 60*time.Second {
		t.Fatalf("fallbackBudget = %s, want 60s（SDK initialize 预算对齐）", c.fallbackBudget)
	}
}
