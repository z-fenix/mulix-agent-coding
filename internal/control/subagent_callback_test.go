package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mulix/internal/subagent"
)

type stubManager struct {
	spawnID    string
	spawned    int
	cancels    int
	lastDepth  int
	lastParent string
	// poll/summary 可编程面（默认保持 running / 无摘要——既有用例形状）。
	pollStatus subagent.Status
	pollErr    error
	summary    string
	hasSummary bool
}

func (s *stubManager) Spawn(task, parentID string, depth int) (string, error) {
	s.spawned++
	s.lastDepth = depth
	s.lastParent = parentID
	return s.spawnID, nil
}
func (s *stubManager) Poll(subID string) (subagent.Status, error) {
	if s.pollStatus != "" {
		return s.pollStatus, s.pollErr
	}
	return subagent.StatusRunning, nil
}
func (s *stubManager) Cancel(subID, reason string) error   { s.cancels++; return nil }
func (s *stubManager) Summary(subID string) (string, bool) { return s.summary, s.hasSummary }

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
	t.Run("poll completed returns summary", func(t *testing.T) {
		// Task 3 裁定（FR-102 最终消息回收）：poll 应答的 summary 来自
		// Manager.Summary——completed/failed 终态携带摘要，running 不携带。
		mgr := &stubManager{spawnID: "sub1", pollStatus: subagent.StatusCompleted,
			summary: "最终结论", hasSummary: true}
		pcs := startSubagentCallback(t, mgr)
		defer pcs.close()
		r := postOp(t, pcs.addr, map[string]any{"op": "poll", "sub_session_id": "sub1"})
		if r["ok"] != true || r["status"] != "completed" || r["summary"] != "最终结论" {
			t.Fatalf("poll resp = %v, want completed + summary", r)
		}
		// running（无摘要）→ 应答不含 summary 键
		mgr2 := &stubManager{spawnID: "sub2"}
		pcs2 := startSubagentCallback(t, mgr2)
		defer pcs2.close()
		r2 := postOp(t, pcs2.addr, map[string]any{"op": "poll", "sub_session_id": "sub2"})
		if r2["status"] != "running" {
			t.Fatalf("poll resp = %v", r2)
		}
		if _, has := r2["summary"]; has {
			t.Fatalf("running poll must not carry summary: %v", r2)
		}
	})
	t.Run("spawn depth from mirror chain", func(t *testing.T) {
		// Task 3 评审移交（必须落实）：wire depth 恒 0（工具契约无 depth 字段）
		// 不可信——真实深度由桥按本会话镜像的 parent_id 链计算（主会话直生子
		// 代理 depth=0，每深一层 +1），MaxDepth 限额因此可达。
		mgr := &stubManager{spawnID: "sub1"}
		c := newTestControllerWithPolicyRules(t, nil, &recordingSink{})
		// 主会话镜像（s-test）挂 parent_id="s-parent"（其父目录不存在——链走
		// 一层即止）：本会话 = 第一层子代理 → 其 spawn 的孙代 depth = 1。
		dir := c.opts.Store.Dir()
		meta, err := c.opts.Store.ReadMeta()
		if err != nil {
			t.Fatalf("read meta: %v", err)
		}
		meta["parent_id"] = "s-parent"
		raw, _ := json.Marshal(meta)
		if werr := os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o644); werr != nil {
			t.Fatalf("seed parent_id: %v", werr)
		}
		scs := c.startSubagentCallback(mgr)
		if scs == nil {
			t.Fatal("subagent callback server must start")
		}
		defer scs.close()
		// wire 里显式携带 depth=99（伪造）——桥必须忽略，按镜像链计算
		//（addr 去 scheme 与 postOp 的 "http://" 前缀约定一致——helper 同款）。
		r := postOp(t, strings.TrimPrefix(scs.addr, "http://"), map[string]any{"op": "spawn", "task": "t", "parent_id": "", "depth": 99})
		if r["ok"] != true || r["sub_session_id"] != "sub1" {
			t.Fatalf("spawn resp = %v", r)
		}
		if mgr.lastDepth != 1 {
			t.Fatalf("spawn depth = %d, want 1（镜像链一层父）", mgr.lastDepth)
		}
		if mgr.lastParent != "s-test" {
			t.Fatalf("spawn parent = %q, want 缺省主会话 id", mgr.lastParent)
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
