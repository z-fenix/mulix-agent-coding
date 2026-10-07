package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mulix/internal/guard"
)

// TestPermissionCallbackBridge pins（终评 Critical-1 第三层）：前台回调服务器
// 把 serve 子进程的审批 POST 路由进 evaluateCanUseTool 裁决核——allow 规则
// 命中 → behavior allow；未命中（无人值守）→ behavior deny。
func TestPermissionCallbackBridge(t *testing.T) {
	t.Run("allow rule", func(t *testing.T) {
		c := newTestControllerWithPolicyRules(t, nil, &recordingSink{},
			policyRuleIn{raw: "Write", effect: guard.DecisionAllow})
		pcs := c.startPermissionCallback()
		if pcs == nil {
			t.Fatal("callback server must start")
		}
		defer pcs.close()
		resp := postCallback(t, pcs.addr, "Write", `{"file_path":"x.txt"}`, "tu-allow")
		if resp["behavior"] != "allow" {
			t.Fatalf("behavior = %v, want allow", resp["behavior"])
		}
	})
	t.Run("deny unattended", func(t *testing.T) {
		c := newTestControllerWithPolicyRules(t, nil, &recordingSink{})
		pcs := c.startPermissionCallback()
		if pcs == nil {
			t.Fatal("callback server must start")
		}
		defer pcs.close()
		resp := postCallback(t, pcs.addr, "Bash", `{"command":"rm"}`, "tu-deny")
		if resp["behavior"] != "deny" {
			t.Fatalf("behavior = %v, want deny", resp["behavior"])
		}
	})
	t.Run("malformed body", func(t *testing.T) {
		c := newTestControllerWithPolicyRules(t, nil, &recordingSink{})
		pcs := c.startPermissionCallback()
		defer pcs.close()
		req, _ := http.NewRequest(http.MethodPost, pcs.addr, strings.NewReader(`{bad`))
		w := httptest.NewRecorder()
		pcs.handle(w, req)
		var b map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b["behavior"] != "deny" {
			t.Fatalf("malformed body must deny, got %s", w.Body.String())
		}
	})
}

// postCallback 向回调端发一次审批请求并解析 behavior JSON。
func postCallback(t *testing.T, addr, tool, input, toolUseID string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"tool_name": tool, "input": json.RawMessage(input), "tool_use_id": toolUseID})
	resp, err := http.Post(addr, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var b map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b
}
