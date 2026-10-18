// sse_test.go——SSE 流钉子(2026-10-18-web-ui-mvp Task 7):订阅 → 事件按
// Publish 序成帧(镜像 wire record 同形);客户端断开不 panic(退订幂等 +
// release no-op 面);`: connected` 握手注释行 = 订阅就绪同步缝。
package serve

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mulix/internal/engine"
	"mulix/internal/event"
)

// sseStream 是 SSE 客户端侧观测点:后台 goroutine 逐行读,
// `data: ` 帧解 JSON 入 channel;`: connected` 触发 connected。
type sseStream struct {
	resp      *http.Response
	frames    chan map[string]any
	connected chan struct{}
}

// openSSE 发起 GET /api/sessions/{id}/stream(带 clientID Cookie)。
func openSSE(t *testing.T, baseURL, id, clientID string) *sseStream {
	t.Helper()
	req, err := http.NewRequest("GET", baseURL+"/api/sessions/"+id+"/stream", nil)
	if err != nil {
		t.Fatalf("new stream req: %v", err)
	}
	if clientID != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: clientID})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	s := &sseStream{
		resp:      resp,
		frames:    make(chan map[string]any, 64),
		connected: make(chan struct{}),
	}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == ": connected":
				select {
				case <-s.connected:
				default:
					close(s.connected)
				}
			case strings.HasPrefix(line, "data: "):
				var f map[string]any
				if json.Unmarshal([]byte(line[len("data: "):]), &f) == nil {
					s.frames <- f
				}
			}
		}
		close(s.frames)
	}()
	return s
}

// waitConnected 等订阅就绪(服务端已 Subscribe,后续 submit 事件必达)。
func (s *sseStream) waitConnected(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-s.connected:
	case <-time.After(timeout):
		t.Fatal("SSE 握手注释行未到达(订阅未就绪)")
	}
}

// waitFrame 等待指定 kind 的帧(kind 空串 = 任意首帧)。
func (s *sseStream) waitFrame(t *testing.T, kind string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				t.Fatalf("SSE 流在等待 %s 前终结", kind)
			}
			if kind == "" || f["kind"] == kind {
				return f
			}
		case <-deadline:
			t.Fatalf("timeout waiting for SSE frame %q", kind)
		}
	}
}

// TestSSEFirstEventAndOrder 钉:首帧 = turn_started;帧序 = Publish 序
// (turn_started < assistant_text < tool_use_requested < tool_result <
// turn_done);客户端断开(Fiber)不 panic 且 host 存活。
func TestSSEFirstEventAndOrder(t *testing.T) {
	env := newHTTPEnv(t, fixtureTurnScript(t))
	id := createAndOpen(t, env, "sse-client")

	s := openSSE(t, env.srv.URL, id, "sse-client")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	resp := httpDo(t, "POST", env.srv.URL+"/api/sessions/"+id+"/submit", "sse-client", env.srv.URL, `{"text":"go"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d, want 202", resp.StatusCode)
	}

	first := s.waitFrame(t, "", 5*time.Second)
	if first["kind"] != "turn_started" {
		t.Fatalf("首帧 kind = %v, want turn_started(Publish 序)", first["kind"])
	}

	// 帧序 = Publish 序(fixture 回合事实序:thinking → 工具请求 → 工具结果
	// → 文本 → usage → done;相对序断言,不钉帧全量)。
	kinds := []string{first["kind"].(string)}
	for {
		f := s.waitFrame(t, "", 5*time.Second)
		k := f["kind"].(string)
		kinds = append(kinds, k)
		if k == "turn_done" {
			break
		}
	}
	pos := func(kind string) int {
		for i, k := range kinds {
			if k == kind {
				return i
			}
		}
		return -1
	}
	for _, seq := range [][2]string{
		{"turn_started", "tool_use_requested"},
		{"tool_use_requested", "tool_result"},
		{"tool_result", "turn_done"},
	} {
		a, b := pos(seq[0]), pos(seq[1])
		if a < 0 || b < 0 || a >= b {
			t.Fatalf("帧序破坏: %v(kinds=%v)", seq, kinds)
		}
	}
	if pos("assistant_text") < 0 {
		t.Fatalf("assistant_text 帧缺失(kinds=%v)", kinds)
	}

	// 客户端断开:退订 + release(持有者→宽限;此处 host 由 closeAllHosts 收口)
	// ——任何二次 close/panic 即测试进程崩,即 RED。
	s.resp.Body.Close()
	time.Sleep(150 * time.Millisecond)

	h, ok := env.mgr.get(id)
	if !ok {
		t.Fatal("断开后 host 被误摘除")
	}
	// 断开后发布(退订已生效;订阅者满缓冲断开路径的幂等性——再 Publish 无 panic)
	h.sink.Publish(event.NoticeEvent{Seq: 9999, Level: "info", Content: "after-disconnect"})
	waitForCond(t, 2*time.Second, "断开后 host 仍可服务", func() bool {
		return getSessionState(t, env, id) != nil
	})
}

// TestSSEShutdownBreaksStream 钉(SSE 生命线):http.Server.Shutdown 不取消
// 请求 ctx——流必须消费 Run 注入的 shutdown 通道,否则优雅关停拖满窗口。
// close(shutdown) 后流应终结(退订 + release 路径同断开面)。
func TestSSEShutdownBreaksStream(t *testing.T) {
	env := newHTTPEnv(t, engine.FakeScript{})
	id := createAndOpen(t, env, "c-shutdown")

	shutdown := make(chan struct{})
	opts := env.opts
	opts.shutdown = shutdown
	srv2 := httptest.NewServer(newMux(env.mgr, opts))
	t.Cleanup(srv2.Close)

	s := openSSE(t, srv2.URL, id, "c-shutdown")
	defer s.resp.Body.Close()
	s.waitConnected(t, 2*time.Second)

	close(shutdown)
	select {
	case <-s.frames: // 流终结:处理器已退订返回,响应体 EOF
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown 生命线未断流(优雅关停将拖满窗口)")
	}
}
