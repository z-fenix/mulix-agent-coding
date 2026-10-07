package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadEngineLockMissing pins：lock 缺失 → 错误（不静默放行）。
func TestLoadEngineLockMissing(t *testing.T) {
	_, err := LoadEngineLock(filepath.Join(t.TempDir(), "nope.lock"))
	if err == nil || !strings.Contains(err.Error(), "read engine.lock") {
		t.Fatalf("missing lock must fail: %v", err)
	}
}

// TestLoadEngineLockCorrupt pins：lock 损坏 → 错误（治理文件损坏等同未治理）。
func TestLoadEngineLockCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.lock")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadEngineLock(path)
	if err == nil || !strings.Contains(err.Error(), "parse engine.lock") {
		t.Fatalf("corrupt lock must fail: %v", err)
	}
}

// TestLoadEngineLockEmptyVersion pins：cli_version 缺失 → 错误（无版本可比对
// 即无法治理漂移）。
func TestLoadEngineLockEmptyVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.lock")
	if err := os.WriteFile(path, []byte(`{"pinned_at":"2026-10-06"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadEngineLock(path)
	if err == nil || !strings.Contains(err.Error(), "cli_version") {
		t.Fatalf("lock without cli_version must fail: %v", err)
	}
}

// TestLoadEngineLockOK pins：合法 lock 解析（与仓库根 engine.lock 形状一致）。
func TestLoadEngineLockOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.lock")
	content := `{"cli_version":"2.1.291","protocol_probe":{"init_version_field":"claude_code_version"},"pinned_at":"2026-10-06","baseline":{"cache_hit_rate":0.0}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := LoadEngineLock(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if lock.CLIVersion != "2.1.291" || lock.ProtocolProbe.InitVersionField != "claude_code_version" {
		t.Fatalf("lock fields wrong: %+v", lock)
	}
}
