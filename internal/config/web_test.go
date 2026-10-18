package config

import (
	"strings"
	"testing"
)

// TestWebConfigDefaults pins [web] 内置默认（2026-10-18-web-ui-mvp Task 1）：
// 空 toml → Web 经 Load 规整后 Port 3180 / Bind "127.0.0.1" /
// IdleCloseMinutes 10 / WorkspaceRoots 空（serve 时取 cwd）。
func TestWebConfigDefaults(t *testing.T) {
	cfg, _, err := NewLoader().Load(Flags{}, Paths{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Web.Port != 3180 {
		t.Errorf("Web.Port = %d, want 3180", cfg.Web.Port)
	}
	if cfg.Web.Bind != "127.0.0.1" {
		t.Errorf("Web.Bind = %q, want 127.0.0.1", cfg.Web.Bind)
	}
	if cfg.Web.IdleCloseMinutes != 10 {
		t.Errorf("Web.IdleCloseMinutes = %d, want 10", cfg.Web.IdleCloseMinutes)
	}
	if len(cfg.Web.WorkspaceRoots) != 0 {
		t.Errorf("Web.WorkspaceRoots = %v, want empty", cfg.Web.WorkspaceRoots)
	}
}

// TestWebConfigParse pins [web] TOML 键位（port/bind/workspace_roots/
// idle_close_minutes）逐字段解析；workspace_roots 为字符串数组。
func TestWebConfigParse(t *testing.T) {
	project := writeTOML(t, "[web]\nport = 4000\nbind = \"127.0.0.1\"\nworkspace_roots = [\"a\", \"b\"]\nidle_close_minutes = 5\n")
	cfg, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Web.Port != 4000 {
		t.Errorf("Web.Port = %d, want 4000", cfg.Web.Port)
	}
	if cfg.Web.Bind != "127.0.0.1" {
		t.Errorf("Web.Bind = %q, want 127.0.0.1", cfg.Web.Bind)
	}
	if len(cfg.Web.WorkspaceRoots) != 2 || cfg.Web.WorkspaceRoots[0] != "a" || cfg.Web.WorkspaceRoots[1] != "b" {
		t.Errorf("Web.WorkspaceRoots = %v, want [a b]", cfg.Web.WorkspaceRoots)
	}
	if cfg.Web.IdleCloseMinutes != 5 {
		t.Errorf("Web.IdleCloseMinutes = %d, want 5", cfg.Web.IdleCloseMinutes)
	}
}

// TestWebConfigValidationFailClosed pins [web] 非法值 fail-closed（仿
// [eventbus] 门惯例：Load 末尾拒绝，错误消息含 "web"）：port ≤0 或 >65535、
// bind 显式空串、idle_close_minutes ≤0。
func TestWebConfigValidationFailClosed(t *testing.T) {
	cases := []struct {
		name string
		toml string
	}{
		{"port zero", "[web]\nport = 0\n"},
		{"port negative", "[web]\nport = -1\n"},
		{"port too large", "[web]\nport = 70000\n"},
		{"bind empty string", "[web]\nbind = \"\"\n"},
		{"idle_close_minutes zero", "[web]\nidle_close_minutes = 0\n"},
		{"idle_close_minutes negative", "[web]\nidle_close_minutes = -1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project := writeTOML(t, tc.toml)
			cfg, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
			if err == nil {
				t.Fatalf("Load(%q) = %+v, want fail-closed error", tc.toml, cfg)
			}
			if !strings.Contains(err.Error(), "web") {
				t.Fatalf("error %q does not mention web", err)
			}
		})
	}
}

// TestWebConfigPartialLayer pins [web] 部分键层叠：只写一个键 → 其余键保
// 内置默认（BurntSushi 解码保底值语义，与 [subagent] 同款）。
func TestWebConfigPartialLayer(t *testing.T) {
	project := writeTOML(t, "[web]\nport = 4000\n")
	cfg, _, err := NewLoader().Load(Flags{}, Paths{Project: project})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Web.Port != 4000 {
		t.Errorf("Web.Port = %d, want 4000", cfg.Web.Port)
	}
	if cfg.Web.Bind != "127.0.0.1" || cfg.Web.IdleCloseMinutes != 10 || len(cfg.Web.WorkspaceRoots) != 0 {
		t.Fatalf("partial layer web = %+v, want bind/idle/roots at defaults", cfg.Web)
	}
}
