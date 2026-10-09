package mulixsubagent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mulix/internal/registry"
)

// newCallbackStub 起一个假前台回调端：记录最近一次 POST body，应答 respBody。
func newCallbackStub(t *testing.T, status int, respBody string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// decodeToolOut 解析工具应答 JSON（工具统一返回 text content JSON）。
func decodeToolOut(t *testing.T, out string, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatalf("execute error = %v, want nil (fail-closed 走 ok=false JSON)", err)
	}
	var m map[string]any
	if e := json.Unmarshal([]byte(out), &m); e != nil {
		t.Fatalf("tool output not JSON: %q: %v", out, e)
	}
	return m
}

func TestSpawnToolRelaysCallback(t *testing.T) {
	srv, got := newCallbackStub(t, http.StatusOK, `{"ok":true,"sub_session_id":"sub9"}`)
	t.Setenv("MULIX_SUBAGENT_CALLBACK", srv.URL)
	out, err := (&spawnTool{}).Execute(nil, []byte(`{"task":"write tests"}`))
	m := decodeToolOut(t, out, err)
	if m["ok"] != true || m["sub_session_id"] != "sub9" {
		t.Fatalf("relay resp = %v", m)
	}
	if len(*got) != 1 {
		t.Fatalf("callback posts = %d, want 1", len(*got))
	}
	if (*got)[0]["op"] != "spawn" || (*got)[0]["task"] != "write tests" {
		t.Fatalf("callback body = %v", (*got)[0])
	}
}

func TestPollAndCancelRelayCallback(t *testing.T) {
	srv, got := newCallbackStub(t, http.StatusOK, `{"ok":true,"status":"running"}`)
	t.Setenv("MULIX_SUBAGENT_CALLBACK", srv.URL)
	out, err := (&pollTool{}).Execute(nil, []byte(`{"sub_session_id":"sub1"}`))
	m := decodeToolOut(t, out, err)
	if m["ok"] != true || m["status"] != "running" {
		t.Fatalf("poll resp = %v", m)
	}
	srv2, got2 := newCallbackStub(t, http.StatusOK, `{"ok":true,"sub_session_id":"sub1"}`)
	t.Setenv("MULIX_SUBAGENT_CALLBACK", srv2.URL)
	out, err = (&cancelTool{}).Execute(nil, []byte(`{"sub_session_id":"sub1"}`))
	m = decodeToolOut(t, out, err)
	if m["ok"] != true || m["sub_session_id"] != "sub1" {
		t.Fatalf("cancel resp = %v", m)
	}
	if (*got)[0]["op"] != "poll" || (*got)[0]["sub_session_id"] != "sub1" {
		t.Fatalf("poll body = %v", (*got)[0])
	}
	if (*got2)[0]["op"] != "cancel" || (*got2)[0]["sub_session_id"] != "sub1" {
		t.Fatalf("cancel body = %v", (*got2)[0])
	}
}

// TestFailClosedMatrix 钉 deny 矩阵（同 mulixpermission）：env 缺失 / 回调
// 不可达 / 非 200 / 应答非 JSON → {"ok":false,"error":...}，协议层不失败。
func TestFailClosedMatrix(t *testing.T) {
	t.Run("env missing", func(t *testing.T) {
		t.Setenv("MULIX_SUBAGENT_CALLBACK", "")
		out, err := (&spawnTool{}).Execute(nil, []byte(`{"task":"t"}`))
		m := decodeToolOut(t, out, err)
		if m["ok"] != false || !strings.Contains(fmt.Sprint(m["error"]), "MULIX_SUBAGENT_CALLBACK") {
			t.Fatalf("env missing resp = %v", m)
		}
	})
	t.Run("callback unreachable", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		dead.Close() // 先关——地址必然不可达
		t.Setenv("MULIX_SUBAGENT_CALLBACK", dead.URL)
		out, err := (&pollTool{}).Execute(nil, []byte(`{"sub_session_id":"s"}`))
		m := decodeToolOut(t, out, err)
		if m["ok"] != false || !strings.Contains(fmt.Sprint(m["error"]), "unreachable") {
			t.Fatalf("unreachable resp = %v", m)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		srv, _ := newCallbackStub(t, http.StatusInternalServerError, `{}`)
		t.Setenv("MULIX_SUBAGENT_CALLBACK", srv.URL)
		out, err := (&cancelTool{}).Execute(nil, []byte(`{"sub_session_id":"s"}`))
		m := decodeToolOut(t, out, err)
		if m["ok"] != false || !strings.Contains(fmt.Sprint(m["error"]), "status 500") {
			t.Fatalf("non-200 resp = %v", m)
		}
	})
	t.Run("bad JSON", func(t *testing.T) {
		srv, _ := newCallbackStub(t, http.StatusOK, `{bad`)
		t.Setenv("MULIX_SUBAGENT_CALLBACK", srv.URL)
		out, err := (&spawnTool{}).Execute(nil, []byte(`{"task":"t"}`))
		m := decodeToolOut(t, out, err)
		if m["ok"] != false || !strings.Contains(fmt.Sprint(m["error"]), "invalid JSON") {
			t.Fatalf("bad JSON resp = %v", m)
		}
	})
}

// TestToolArgumentValidation 钉 schema 必填的工具侧校验（引擎侧漏检的兜底）。
func TestToolArgumentValidation(t *testing.T) {
	cases := []struct {
		name string
		tool registry.Tool
		args string
	}{
		{"spawn empty task", &spawnTool{}, `{}`},
		{"poll empty id", &pollTool{}, `{}`},
		{"cancel bad json", &cancelTool{}, `{bad`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MULIX_SUBAGENT_CALLBACK", "http://127.0.0.1:1/op") // 不会到达——错误须来自本地校验而非传输
			out, err := tc.tool.Execute(nil, []byte(tc.args))
			m := decodeToolOut(t, out, err)
			if m["ok"] != false || !strings.Contains(fmt.Sprint(m["error"]), "malformed mulix_") {
				t.Fatalf("args validation resp = %v (须为本地校验错误，而非传输错误)", m)
			}
		})
	}
}

func TestToolSchemasAndNames(t *testing.T) {
	schemaOf := func(raw json.RawMessage) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("schema: %v", err)
		}
		return m
	}
	st := schemaOf((&spawnTool{}).Schema())
	if st["type"] != "object" {
		t.Fatalf("spawn schema type = %v", st["type"])
	}
	if props, _ := st["properties"].(map[string]any); props == nil || props["task"] == nil {
		t.Fatalf("spawn schema properties = %v", st["properties"])
	}
	if req, _ := st["required"].([]any); len(req) != 1 || req[0] != "task" {
		t.Fatalf("spawn schema required = %v", st["required"])
	}
	for _, tc := range []struct {
		tool registry.Tool
		name string
	}{
		{&spawnTool{}, "mulix_spawn"},
		{&pollTool{}, "mulix_poll"},
		{&cancelTool{}, "mulix_cancel"},
	} {
		if tc.tool.Name() != tc.name {
			t.Fatalf("name = %q, want %q", tc.tool.Name(), tc.name)
		}
	}
	if (&pollTool{}).ReadOnly() != true {
		t.Fatal("mulix_poll must be read-only")
	}
	if (&spawnTool{}).ReadOnly() || (&cancelTool{}).ReadOnly() {
		t.Fatal("spawn/cancel must not be read-only")
	}
}

// TestRegisterConditionalRegistersOnce 钉条件注册：enabled=true 恰好注册三个
// 工具；重复调用不得 panic（sync.Once 兜底——registry.Register 对重名 panic）。
func TestRegisterConditionalRegistersOnce(t *testing.T) {
	RegisterConditional(nil, true) // 进程内首次调用（本包测试仅此一处）
	RegisterConditional(nil, true) // 重复调用——Once 拦截，不得 panic
	names := registry.Registered()
	for _, want := range []string{"mulix_spawn", "mulix_poll", "mulix_cancel"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("RegisterConditional(true) missing %q in %v", want, names)
		}
	}
}
