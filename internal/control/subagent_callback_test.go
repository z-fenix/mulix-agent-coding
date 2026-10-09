package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"mulix/internal/subagent"
)

type stubManager struct {
	spawnID string
	spawned int
	cancels int
}

func (s *stubManager) Spawn(task, parentID string, depth int) (string, error) {
	s.spawned++
	return s.spawnID, nil
}
func (s *stubManager) Poll(subID string) (subagent.Status, error) { return subagent.StatusRunning, nil }
func (s *stubManager) Cancel(subID, reason string) error          { s.cancels++; return nil }

func postOp(t *testing.T, addr string, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post("http://"+addr+"/op", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestSubagentCallbackBridge(t *testing.T) {
	t.Run("spawn→poll", func(t *testing.T) {
		mgr := &stubManager{spawnID: "sub1"}
		pcs := startSubagentCallback(t, mgr) // 测试助手：起监听、返回地址（复用 P1 permission 桥模式）
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "spawn", "task": "t", "parent_id": "p", "depth": 0})
		if r["sub_session_id"] != "sub1" || r["ok"] != true {
			t.Fatalf("spawn resp = %v", r)
		}
		pr := postOp(t, pcs.addr, map[string]any{"op": "poll", "sub_session_id": "sub1"})
		if pr["status"] != "running" {
			t.Fatalf("poll resp = %v", pr)
		}
	})
	t.Run("bad op denied", func(t *testing.T) {
		pcs := startSubagentCallback(t, &stubManager{})
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "evil"})
		if r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "op") {
			t.Fatalf("bad op resp = %v", r)
		}
	})
	t.Run("closed session denied", func(t *testing.T) {
		pcs := startSubagentCallback(t, &stubManager{})
		pcs.setClosed() // 模拟控制器已关闭
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "spawn", "task": "t", "parent_id": "p", "depth": 0})
		if r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "closed") {
			t.Fatalf("closed resp = %v", r)
		}
	})
}

// ---- 测试助手（生产实现前的 RED 面：startSubagentCallback/subagentOps/
// subagentCallbackServer 由 controller.go 提供） ----

// subCallbackHandle 是回调桥的测试句柄：addr 已去 scheme（postOp 自行拼
// http:// 前缀与 /op 后缀），close/setClosed 转发到生产回调服务器。
type subCallbackHandle struct {
	addr      string
	srv       *subagentCallbackServer
	closeOnce sync.Once
}

func (h *subCallbackHandle) close() { h.closeOnce.Do(func() { h.srv.close() }) }

// setClosed 模拟控制器已收口（生产路径由 Controller.Close 先置 closed 再收口
// 监听——与 permissionCallbackServer 的 ctrl.closed 判据同源）。
func (h *subCallbackHandle) setClosed() {
	h.srv.ctrl.mu.Lock()
	h.srv.ctrl.closed = true
	h.srv.ctrl.mu.Unlock()
}

// startSubagentCallback 起监听、返回地址（复用 P1 permission 桥模式）：经
// 测试控制器走生产 startSubagentCallback 路径；stubManager 直接实现窄接口。
func startSubagentCallback(t *testing.T, mgr subagentOps) *subCallbackHandle {
	t.Helper()
	c := newTestControllerWithPolicyRules(t, nil, &recordingSink{})
	scs := c.startSubagentCallback(mgr)
	if scs == nil {
		t.Fatal("subagent callback server must start")
	}
	return &subCallbackHandle{
		addr: strings.TrimPrefix(scs.addr, "http://"),
		srv:  scs,
	}
}
