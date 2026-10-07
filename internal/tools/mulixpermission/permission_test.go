package mulixpermission

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExecuteDenyWithoutCallback pins（fail-closed）：无 MULIX_PERMISSION_CALLBACK
// → deny + 原因（引擎侧绝不悬挂、绝不放行）。
func TestExecuteDenyWithoutCallback(t *testing.T) {
	t.Setenv("MULIX_PERMISSION_CALLBACK", "")
	out, err := (&permissionTool{}).Execute(t.Context(), json.RawMessage(`{"tool_name":"Write","tool_use_id":"tu-1","input":{}}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	assertDeny(t, out, "not configured")
}

// TestExecuteAllowPassthrough pins：回调 200 + behavior JSON → 原样透传。
func TestExecuteAllowPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ToolName  string          `json:"tool_name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		if payload.ToolName != "Write" || payload.ToolUseID != "tu-2" {
			t.Errorf("payload = %+v", payload)
		}
		_, _ = w.Write([]byte(`{"behavior":"allow","updatedInput":{"file_path":"x.txt"}}`))
	}))
	defer srv.Close()
	t.Setenv("MULIX_PERMISSION_CALLBACK", srv.URL)
	out, err := (&permissionTool{}).Execute(t.Context(), json.RawMessage(`{"tool_name":"Write","tool_use_id":"tu-2","input":{"file_path":"x.txt"}}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != `{"behavior":"allow","updatedInput":{"file_path":"x.txt"}}` {
		t.Fatalf("passthrough broken: %s", out)
	}
}

// TestExecuteDenyOnServerErrors pins（fail-closed 矩阵）：回调不可达 / 非 200 /
// 非 behavior JSON → 一律 deny + 原因。
func TestExecuteDenyOnServerErrors(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	weird := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer weird.Close()

	t.Run("non-200", func(t *testing.T) {
		t.Setenv("MULIX_PERMISSION_CALLBACK", bad.URL)
		out, _ := (&permissionTool{}).Execute(t.Context(), json.RawMessage(`{"tool_name":"Bash","tool_use_id":"tu-3"}`))
		assertDeny(t, out, "status 500")
	})
	t.Run("invalid behavior", func(t *testing.T) {
		t.Setenv("MULIX_PERMISSION_CALLBACK", weird.URL)
		out, _ := (&permissionTool{}).Execute(t.Context(), json.RawMessage(`{"tool_name":"Bash","tool_use_id":"tu-4"}`))
		assertDeny(t, out, "invalid behavior")
	})
	t.Run("unreachable", func(t *testing.T) {
		t.Setenv("MULIX_PERMISSION_CALLBACK", "http://127.0.0.1:1")
		out, _ := (&permissionTool{}).Execute(t.Context(), json.RawMessage(`{"tool_name":"Bash","tool_use_id":"tu-5"}`))
		assertDeny(t, out, "unreachable")
	})
}

func assertDeny(t *testing.T, out, wantMsgPart string) {
	t.Helper()
	var b struct {
		Behavior string `json:"behavior"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("out not behavior JSON: %v (%s)", err, out)
	}
	if b.Behavior != "deny" {
		t.Fatalf("behavior = %q, want deny (%s)", b.Behavior, out)
	}
	if wantMsgPart != "" && !jsonContains(b.Message, wantMsgPart) {
		t.Fatalf("message %q must mention %q", b.Message, wantMsgPart)
	}
}

func jsonContains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
