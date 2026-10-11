package control

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
)

// TestLeaderPanicWakeFollowers 钉 FR-101/102/105:单飞 leader 评估期 panic
// 时,等待方在有限时间内被唤醒且拿到一致的 fail-closed 裁决;在飞条目被
// 清理;异常路径镜像如实留痕(Notice);每次收口的评估 resolved 恰一次。
func TestLeaderPanicWakeFollowers(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		panic("gate exploded (test injection)")
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()

	const n = 20
	const toolUseID = "tu-panic-fanout"
	answers := make([]map[string]any, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			answers[i], errs[i] = postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, toolUseID)
		}(i)
	}
	close(start)
	wg.Wait()

	// 全员在超时内拿到 deny(现状缺陷:leader panic 后等待方悬挂到 ctx 超时)。
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: callback POST failed (panic must not hang waiters): %v", i, errs[i])
		}
		if answers[i]["behavior"] != "deny" {
			t.Fatalf("goroutine %d behavior = %v, want deny (fail-closed fallback)", i, answers[i]["behavior"])
		}
	}
	// 裁决一致性(决策层,FR-101):全员 deny;message 全部落在两类 fail-closed
	// 来源(leader 兜底 / 等待方防御)——瞬时 panic 下晚到者按 spec US1-2 成新
	// leader 重评(可能再 panic),故固定 1/n-1 分布不可确定;重评语义由
	// TestLeaderPanicThenFreshRequest 顺序钉住。
	leaderMsg, waiterMsg := 0, 0
	for i := range n {
		m, _ := answers[i]["message"].(string)
		switch {
		case strings.Contains(m, "singleflight: leader panic"):
			leaderMsg++
		case strings.Contains(m, "singleflight: ruling missing"):
			waiterMsg++
		default:
			t.Fatalf("goroutine %d unexpected message %q", i, m)
		}
	}
	if leaderMsg < 1 {
		t.Fatalf("leader fallback messages = %d, want >= 1 (panic must answer fail-closed)", leaderMsg)
	}
	// 每次收口的评估恰一次 resolved(可能 >1:晚到者重评各自成 leader);
	// N4 exactly-once 的正常路径由 TestPermissionCallbackSingleflightConcurrent 钉。
	_, resolved := countApprovalEvents(t, c, toolUseID)
	if resolved < 1 {
		t.Fatalf("approval_resolved count = %d, want >= 1 (each settled evaluation publishes once)", resolved)
	}
	// 在飞条目清理(FR-102)。
	c.mu.Lock()
	inflightLeft := len(c.inflight)
	c.mu.Unlock()
	if inflightLeft != 0 {
		t.Fatalf("inflight left = %d, want 0 (panic must clean up)", inflightLeft)
	}
	// 异常路径镜像留痕(FR-105):Notice 提及 leader panic。
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	sawNotice := false
	for _, ev := range mirror {
		if nEv, ok := ev.(event.NoticeEvent); ok && strings.Contains(nEv.Content, "leader panic") {
			sawNotice = true
		}
	}
	if !sawNotice {
		t.Fatal("mirror must record a Notice mentioning leader panic (FR-105)")
	}
}

// TestLeaderPanicThenFreshRequest 钉 FR-102/FR-103(Edge):panic 后同 ID 的
// 新请求不被悬挂状态阻塞——关闭钩子后重新评估并得到新裁决。
func TestLeaderPanicThenFreshRequest(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	probeOn := true
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		if probeOn {
			panic("gate exploded (test injection)")
		}
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ans, err := postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-panic-1")
	if err != nil {
		t.Fatalf("first (panicking) POST must still answer: %v", err)
	}
	if ans["behavior"] != "deny" {
		t.Fatalf("panicked ruling = %v, want deny", ans["behavior"])
	}
	probeOn = false // 新评估不再 panic → allow 规则生效
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	ans2, err := postCallbackOnce(ctx2, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-panic-1")
	if err != nil {
		t.Fatalf("fresh POST after panic must re-evaluate (not hang): %v", err)
	}
	if ans2["behavior"] != "allow" {
		t.Fatalf("fresh ruling = %v, want allow (re-evaluation)", ans2["behavior"])
	}
}

var _ = httptest.NewRecorder // 保留:closed-session 子测试同型参考(closed POST handler 直测)

// TestResolvedBoundedAcrossTurns 钉 FR-103:裁决缓存表随回合收口回收——
// 收口后条目数回落常数,不随历史工具调用数增长;进行中审批(收口前)不受影响。
func TestResolvedBoundedAcrossTurns(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	// 白盒前置:回合 1 进行中(onResult 只消费 running 会话的 result)。
	c.mu.Lock()
	c.running = true
	c.turn = 1
	c.mu.Unlock()

	// 回合 1:三次裁决(allow 规则命中,走正常 leader 路径)。
	c.evaluateCanUseTool("r1", "tu-b1", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`1`))
	c.evaluateCanUseTool("r2", "tu-b2", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`2`))
	c.evaluateCanUseTool("r3", "tu-b3", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`3`))

	c.mu.Lock()
	sizeAfterTurn1 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterTurn1 != 3 {
		t.Fatalf("resolved size after 3 rulings = %d, want 3 (in-flight turn entries present)", sizeAfterTurn1)
	}

	// 回合 1 收口 → 条目回收。
	c.onResult(engine.Event{Kind: engine.KindResult,
		Result: &engine.ResultInfo{SubType: "success", IsError: false, NumTurns: 1}})
	c.mu.Lock()
	sizeAfterClose1 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterClose1 != 0 {
		t.Fatalf("resolved size after turn close = %d, want 0 (bounded per FR-103)", sizeAfterClose1)
	}

	// 回合 2:panic 兜底裁决不入缓存(Task 1 设计)——只有正常裁决 tu-b4 在表;
	// 收口后 0 条(兜底天然有界,无豁免问题)。
	c.mu.Lock()
	c.running = true // onResult 首轮已置 false;不置位则回合 2 收口被 running guard 忽略
	c.turn = 2
	c.mu.Unlock()
	probeOn := true
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		if probeOn {
			panic("gate exploded (test injection)")
		}
	}
	c.evaluateCanUseTool("r4", "tu-p1", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`4`))
	probeOn = false
	c.evaluateCanUseTool("r5", "tu-b4", "Write", json.RawMessage(`{"file_path":"x.txt"}`), json.RawMessage(`5`))
	c.mu.Lock()
	sizeAfterTurn2 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterTurn2 != 1 {
		t.Fatalf("resolved size in turn 2 = %d, want 1 (panic fallback never cached; normal entry present)", sizeAfterTurn2)
	}
	c.onResult(engine.Event{Kind: engine.KindResult,
		Result: &engine.ResultInfo{SubType: "success", IsError: false, NumTurns: 2}})
	c.mu.Lock()
	sizeAfterClose2 := len(c.resolved)
	c.mu.Unlock()
	if sizeAfterClose2 != 0 {
		t.Fatalf("resolved size after turn 2 close = %d, want 0 (fallback never cached, normal entry cleaned)", sizeAfterClose2)
	}
}

// TestLeaderPanicDoesNotPoisonApproveMu 钉终审 Critical:panic 发生在
// approveMu 临界区内(SetAlias/Evaluate——生产最真实的 panic 位点)时,
// 锁必须被 panic-safe 释放;否则后续所有 leader 在 approveMu.Lock 永久
// 悬挂,审批回路整体卡死(FR-101 复活)。
func TestLeaderPanicDoesNotPoisonApproveMu(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	probeOn := true
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		if probeOn {
			panic("gate exploded inside critical section (test injection)")
		}
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()

	// 第一次:panic 发生在临界区内 → fail-closed deny。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ans, err := postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-poison-1")
	if err != nil {
		t.Fatalf("panicking POST must still answer: %v", err)
	}
	if ans["behavior"] != "deny" {
		t.Fatalf("panicked ruling = %v, want deny", ans["behavior"])
	}

	// 第二次:关闭钩子后,新 leader 必须能拿到 approveMu 并正常评估
	// (锁泄漏时此处悬挂到 ctx 超时 → RED)。
	probeOn = false
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	ans2, err := postCallbackOnce(ctx2, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-poison-2")
	if err != nil {
		t.Fatalf("POST after critical-section panic must not deadlock on approveMu: %v", err)
	}
	if ans2["behavior"] != "allow" {
		t.Fatalf("ruling after recovered panic = %v, want allow", ans2["behavior"])
	}
}

// noticePanicSink 包装 Sink:NoticeEvent 含 "leader panic" 时 Publish panic
// (模拟「publish 自身 panic」的双重故障源——判别注入)。
type noticePanicSink struct {
	Sink
	events []any
}

func (s *noticePanicSink) Publish(ev any) {
	s.events = append(s.events, ev)
	if n, ok := ev.(event.NoticeEvent); ok && strings.Contains(n.Content, "leader panic") {
		panic("publish exploded (double-fault injection)")
	}
	s.Sink.Publish(ev)
}

// TestLeaderPanicDoubleFaultProtected 钉 009 FR-103(G2):原始 panic 源于
// publish 自身时,收口路径的 Notice 发布(第二次 publish)被内层防护捕获
// ——双重故障不得使 panic 逃出 defer(逃出 = goroutine 崩溃 = 测试二进制
// 失败,RED)。判别形态:Sink 包装注入——Publish 在 NoticeEvent 含
// "leader panic" 时 panic(首 panic 即此形态:兜底 Notice 是首次 Notice
// 发布);修复前 panic 逃出 defer → RED;修复后内层 recover 捕获 → 绿。
func TestLeaderPanicDoubleFaultProtected(t *testing.T) {
	base := &recordingSink{}
	sink := &noticePanicSink{Sink: base}
	c := newTestControllerWithPolicyRules(t, nil, sink,
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	c.opts.EvaluateProbe = func(tool string, input json.RawMessage) {
		panic("gate exploded (double-fault probe)")
	}
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ans, err := postCallbackOnce(ctx, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-double-fault")
	if err != nil {
		t.Fatalf("double-fault path must answer (panic must not escape): %v", err)
	}
	if ans["behavior"] != "deny" {
		t.Fatalf("behavior = %v, want deny", ans["behavior"])
	}
	if len(base.events) == 0 {
		t.Fatal("sink must record pre-panic events (panicking notice intentionally never forwarded)")
	}
}
