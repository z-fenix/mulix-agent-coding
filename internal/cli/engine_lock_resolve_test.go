package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveEngineLockPath 钉（026 FR-001）：锁解析链——工作区根 → exe 同
// 目录 → 相对路径，命中即止；全缺回退 "engine.lock"（既有错误形态保持）。
func TestResolveEngineLockPath(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "engine.lock")
	if err := os.WriteFile(lock, []byte(`{"cli_version":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("工作区锁命中优先", func(t *testing.T) {
		got := resolveEngineLockPathFrom([]string{lock, filepath.Join(dir, "nope")})
		if got != lock {
			t.Fatalf("got %q, want workspace lock %q", got, lock)
		}
	})
	t.Run("工作区缺回退次选", func(t *testing.T) {
		got := resolveEngineLockPathFrom([]string{filepath.Join(dir, "nope"), lock, "engine.lock"})
		if got != lock {
			t.Fatalf("got %q, want second candidate %q", got, lock)
		}
	})
	t.Run("全缺回退相对路径", func(t *testing.T) {
		got := resolveEngineLockPathFrom([]string{filepath.Join(dir, "nope1"), filepath.Join(dir, "nope2"), "engine.lock"})
		if got != "engine.lock" {
			t.Fatalf("got %q, want relative fallback", got)
		}
	})
	t.Run("组装：工作区根 + exe 同目录 + 相对路径", func(t *testing.T) {
		cands := engineLockCandidates(dir)
		if len(cands) != 3 || cands[0] != filepath.Join(dir, "engine.lock") || cands[2] != "engine.lock" {
			t.Fatalf("candidates = %#v", cands)
		}
	})
}
