package control

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/event"
	"mulix/internal/guard"
)

// TestLeaderPanicWakeFollowers 钉 FR-101/102/105:单飞 leader 评估期 panic
// 时,等待方在有限时间内被唤醒且拿到一致的 fail-closed 裁决;在飞条目被
// 清理;异常路径镜像如实留痕(Notice);resolved 恰一次(leader 兜底发布)。
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
