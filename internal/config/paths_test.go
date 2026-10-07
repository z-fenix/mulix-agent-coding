package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultPathsIncludesProjectLayer pins（终评审 Minor-7）：DefaultPaths
// 必须返回项目层 mulix.toml（cwd）——该层曾在冒烟前静默缺失（FR-016 分层
// 退化为 user > default，config allow 规则整层失效）。chdir + 逐字段断言
// 防回归。
func TestDefaultPathsIncludesProjectLayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows os.UserHomeDir
	t.Setenv("HOME", home)        // 跨平台兜底
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths: %v", err)
	}
	if paths.Project != "mulix.toml" {
		t.Fatalf("project layer missing: %q (must be cwd/mulix.toml)", paths.Project)
	}
	if want := filepath.Join(home, ".mulix", "config.toml"); paths.User != want {
		t.Fatalf("user layer = %q, want %q", paths.User, want)
	}

	// 端到端：项目层的 permissions.allow 真的被加载（冒烟发现的回归形态）。
	if err := os.WriteFile("mulix.toml", []byte("[permissions]\nallow = [\"Write\"]\n"), 0o644); err != nil {
		t.Fatalf("write mulix.toml: %v", err)
	}
	cfg, _, err := NewLoader().Load(Flags{}, paths)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Permissions.Allow) != 1 || cfg.Permissions.Allow[0] != "Write" {
		t.Fatalf("project allow rules not loaded: %v", cfg.Permissions.Allow)
	}
}
