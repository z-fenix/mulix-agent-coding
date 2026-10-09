package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"mulix/internal/event"
	"mulix/internal/guard"
)

// postCallbackOnce 向回调端发一次审批 POST 并解析 behavior JSON；错误返回给
// 调用方——并发 goroutine 内不得 t.Fatalf（FailNow 仅限测试主 goroutine）。
// ctx 超时即「等待方未收到裁决」的探测口（单飞 fan-out 缺陷时不得挂死测试）。
func postCallbackOnce(ctx context.Context, addr, tool, input, toolUseID string) (map[string]any, error) {
	body, _ := json.Marshal(map[string]any{"tool_name": tool,
		"input": json.RawMessage(input), "tool_use_id": toolUseID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr,
		strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status = %d", resp.StatusCode)
	}
	var b map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return nil, err
	}
	return b, nil
}

// countApprovalEvents 统计镜像里同 tool_use_id 的 approval_request /
// approval_resolved 事件数（P1 复审 N4/N5 的审计计数口径）。
func countApprovalEvents(t *testing.T, c *Controller, toolUseID string) (requests, resolved int) {
	t.Helper()
	mirror, err := c.Mirror().ReadAllEvents()
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	for _, ev := range mirror {
		switch e := ev.(type) {
		case event.ApprovalRequestEvent:
			if e.ToolUseID == toolUseID {
				requests++
			}
		case event.ApprovalResolvedEvent:
			if e.ToolUseID == toolUseID {
				resolved++
			}
		}
	}
	return requests, resolved
}

// TestPermissionCallbackSingleflightConcurrent pins（P1 复审 N4/N5）：20 个
// 并发同 tool_use_id 的审批 POST 只评估一次——镜像恰好 1 次 approval_request
// + 1 次 approval_resolved（单飞 leader 唯一发布，等待方/缓存命中方不重复），
// 且全员拿到同一裁决（等待方广播唤醒——单飞等待不得悬挂）。
func TestPermissionCallbackSingleflightConcurrent(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
		policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	defer pcs.close()

	const n = 20
	const toolUseID = "tu-sf-concurrent"
	answers := make([]map[string]any, n)
	errs := make([]error, n)
	start := make(chan struct{}) // 屏障：全员就绪后同时放行（并发测试确定性窗口）
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

	// 镜像计数（N4）：同 tool_use_id 恰好 1 request + 1 resolved。
	requests, resolved := countApprovalEvents(t, c, toolUseID)
	if requests != 1 {
		t.Fatalf("approval_request count = %d, want 1", requests)
	}
	if resolved != 1 {
		t.Fatalf("approval_resolved count = %d, want 1 (N4: waiters must not re-publish)", resolved)
	}

	// 等待方拿到同一裁决：全员 POST 成功（fan-out 无悬挂）且 allow 规则命中。
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: callback POST failed (waiter fan-out must not hang): %v", i, errs[i])
		}
		if answers[i]["behavior"] != "allow" {
			t.Fatalf("goroutine %d behavior = %v, want allow", i, answers[i]["behavior"])
		}
	}
}

// TestPermissionCallbackClosedSessionDenies pins（P1 复审 N5）：会话收口后的
// 审批 POST → deny JSON（behavior=deny, message="session closed"——回调桥
// serve 侧 fail-closed 契约）。Close 已关停监听，deny 决策点在 handler：
// 与 malformed-body 子测试同型，handler 直测（httptest.NewRecorder）。
func TestPermissionCallbackClosedSessionDenies(t *testing.T) {
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{})
	pcs := c.startPermissionCallback()
	if pcs == nil {
		t.Fatal("callback server must start")
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"tool_name": "Write",
		"input": json.RawMessage(`{"file_path":"x.txt"}`), "tool_use_id": "tu-closed"})
	req, err := http.NewRequest(http.MethodPost, pcs.addr, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	w := httptest.NewRecorder()
	pcs.handle(w, req)
	var b map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("closed POST must answer JSON, got %q", w.Body.String())
	}
	if b["behavior"] != "deny" || b["message"] != "session closed" {
		t.Fatalf("closed POST = %s, want {behavior:deny, message:session closed}", w.Body.String())
	}
}
