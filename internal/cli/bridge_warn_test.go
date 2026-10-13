package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSharedEventBusWarnsOnFailure 钉 019 FR-101:OpenBus 失败 swallow 前向
// stderr 透出一行(012「桥关闭如实记录」字面落实)——显式 enable=true 却
// 静默桥关的窗口关闭。快速注入:预置 <ws>/.mulix/bus/endpoint 为**目录**
// → OpenBus 的 endpoint rename 对目录即败(毫秒级;JetStream 用
// bus/jetstream 子目录不冲突)。
func TestSharedEventBusWarnsOnFailure(t *testing.T) {
	ws := t.TempDir()
	busDir := filepath.Join(ws, ".mulix", "bus")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(busDir, "endpoint"), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	o := Options{Workspace: ws, Stderr: &buf}
	if bus := sharedEventBus(o); bus != nil {
		t.Fatal("bridge must be closed on OpenBus failure")
	}
	if !strings.Contains(buf.String(), "eventbus: 桥关闭(OpenBus 失败)") {
		t.Fatalf("stderr = %q, want 桥关闭警告", buf.String())
	}
}
