// serve_test.go——HTTP 面钉子(2026-10-18-web-ui-mvp Task 3):GET /api/healthz
// → 200 {"ok":true};GET / → 嵌入 dist 的占位 index.html。经 newMux +
// httptest 验证,不真听端口。(Task 7 起 newMux 携带 hostManager + Options——
// 本文件只消费健康探针与静态面,传空 Options 不触会话路由。)
package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mgr := newHostManager(time.Hour)
	t.Cleanup(mgr.Close)
	srv := httptest.NewServer(newMux(mgr, Options{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/healthz")
	if err != nil {
		t.Fatalf("GET /api/healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("解码响应体: %v", err)
	}
	if !got["ok"] {
		t.Fatalf("响应体 = %v, want {\"ok\":true}", got)
	}
}

func TestEmbedDistServed(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200(嵌入占位 index.html)", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	if !strings.Contains(string(body), "mulix") {
		t.Fatalf("GET / 响应体不含占位页标记(嵌入 dist 未生效?)")
	}
}
