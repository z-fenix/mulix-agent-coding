package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStaleEndpointRemovedWhenGateOff 钉 020 FR-102:门控关=无进程持有
// 总线——endpoint 必为死残留(kill/crash 收尾无法覆盖),openSession 侧
// 删除清痕;删除失败不阻断启动。
func TestStaleEndpointRemovedWhenGateOff(t *testing.T) {
	ws := t.TempDir()
	busDir := filepath.Join(ws, ".mulix", "bus")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(busDir, "endpoint")
	if err := os.WriteFile(stale, []byte("nats://127.0.0.1:1"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 门控关会话(cfg 无 [eventbus])——经 assembleController 不可轻启,
	// 残留清理落 sharedEventBus 邻位:直调形态以 openSession 同函数核验。
	h := mustOpenSession(t, Options{Workspace: ws, SessionsRoot: ws, UsageRoot: ws,
		Config: testConfig(), HostFactory: fakeHostFactory(t)}, "", nil)
	h.close() // close() 无返回值(session.go:616)——语句式调用(评审 Minor 1)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale endpoint after gate-off session = %v, want removed", err)
	}
}
