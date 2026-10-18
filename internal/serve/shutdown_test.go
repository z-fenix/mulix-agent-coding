// shutdown_test.go——Run 关停逐 host 收尾（Fix Round 1，2026-10-18-web-ui-mvp
// Task 8 修复轮）：serve.Run 的 ctx 取消分支必须把在册 host 收口（ctrl/
// meter/store/web.lock），否则 Ctrl+C 后引擎子进程孤儿化。断言面：
//   - 单元：hostManager.closeAll 逐 host close、锁文件释放、注册表清空、
//     二次调用幂等安全；
//   - 集成：真实 Run（随机空闲端口）+ POST /api/sessions → cancel ctx →
//     Run 返回后 web.lock 消失。h.close 顺序为 ctrl.Close → assembleRelease
//     → unlock——锁文件消失即 ctrl.Close 已完成（传递性断言；修复前仅
//     mgr.Close 停扫描，锁残留即 RED）。
package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/sessionx"
)

// lockPath 是会话 web.lock 路径（锁随会话镜像同目录）。
func lockPath(root, id string) string {
	return filepath.Join(sessionx.SessionDir(root, id), "web.lock")
}

// waitLockGone 轮询锁文件消失（上界 5s；host.close 同步收口，正常毫秒级）。
func waitLockGone(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("web.lock 未释放（在册 host 未随 Run 关停收口）: %s", path)
}

// TestHostManagerCloseAll 钉 closeAll 语义：逐 host close（ctrl 收口 + 锁
// 释放）→ 注册表清空 → 幂等（二次调用不 panic、无错）。
func TestHostManagerCloseAll(t *testing.T) {
	root := t.TempDir()
	mgr := newHostManager(time.Hour)
	t.Cleanup(mgr.Close)
	ids := []string{"s-a", "s-b"}
	for _, id := range ids {
		h, err := newSessionHost(context.Background(), id, testWorkspace, false, stubAssemble(t, root, engine.FakeScript{}))
		if err != nil {
			t.Fatalf("new host %s: %v", id, err)
		}
		release, err := acquireSessionLock(sessionx.SessionDir(root, id))
		if err != nil {
			t.Fatalf("acquire lock %s: %v", id, err)
		}
		h.attachLock(release)
		mgr.add(h)
		if _, err := os.Stat(lockPath(root, id)); err != nil {
			t.Fatalf("lock 应在收口前存在: %v", err)
		}
	}
	if errs := mgr.closeAll(context.Background()); len(errs) != 0 {
		t.Fatalf("closeAll errors: %v", errs)
	}
	mgr.mu.Lock()
	remaining := len(mgr.hosts)
	mgr.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("注册表未清空: %d hosts", remaining)
	}
	for _, id := range ids {
		if _, err := os.Stat(lockPath(root, id)); !os.IsNotExist(err) {
			t.Fatalf("web.lock 未释放 for %s: %v", id, err)
		}
	}
	// 二次 closeAll：已清空 + close 幂等——安全 no-op。
	if errs := mgr.closeAll(context.Background()); len(errs) != 0 {
		t.Fatalf("second closeAll errors: %v", errs)
	}
}

// TestRunShutdownClosesOpenHosts 钉 Run 关停链路：ctx cancel → 优雅 Shutdown
// → 在册 host 逐个收口（锁释放为可观测面）。closeAll 挂在 Run 内部 manager
// 上，httptest 进不来——用真实随机空闲端口。
func TestRunShutdownClosesOpenHosts(t *testing.T) {
	root := t.TempDir()
	cfg, _, err := config.NewLoader().Load(config.Flags{}, config.Paths{})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.Web.WorkspaceRoots = []string{testWorkspace}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, Options{
			Config: cfg, Workspace: testWorkspace, SessionsRoot: root,
			Port: port, NoOpen: true,
			Stdout: io.Discard, Stderr: io.Discard,
			assemble: stubAssemble(t, root, engine.FakeScript{}),
		})
	}()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/api/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("healthz 未就绪")
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp := httpDo(t, "POST", base+"/api/sessions", "c-shutdown", base, `{"workspace":"`+testWorkspace+`"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create status = %d, want 201(body: %s)", resp.StatusCode, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
		t.Fatalf("create 响应缺 id: %v", err)
	}
	lock := lockPath(root, created.ID)
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("web.lock 应在关停前存在: %v", err)
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
	waitLockGone(t, lock)
}
