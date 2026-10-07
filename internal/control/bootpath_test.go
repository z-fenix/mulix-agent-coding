package control

import (
	"testing"

	"mulix/internal/engine"
)

// TestComposeBootEnginePath pins（终评 Critical-1）：composeBoot 必须产出
// EnginePath——ClaudeHost.Start 对空 EnginePath fail-closed（"engine path
// required"），缺该接线则两个产品入口（TUI/run）都无法拉起真实引擎。
// 语义：config engine.path 显式指定优先；空值回退 "claude"（PATH 解析，
// 与 doctor 的 LookPath 探测同一约定）。
func TestComposeBootEnginePath(t *testing.T) {
	t.Run("explicit config", func(t *testing.T) {
		c := newTestController(t, nil, &recordingSink{})
		c.opts.Config.Engine.Path = "my-claude-bin"
		boot, err := c.composeBoot(engine.BootConfig{})
		if err != nil {
			t.Fatalf("composeBoot: %v", err)
		}
		if boot.EnginePath != "my-claude-bin" {
			t.Fatalf("EnginePath = %q, want my-claude-bin", boot.EnginePath)
		}
	})
	t.Run("default claude", func(t *testing.T) {
		c := newTestController(t, nil, &recordingSink{})
		c.opts.Config.Engine.Path = ""
		boot, err := c.composeBoot(engine.BootConfig{})
		if err != nil {
			t.Fatalf("composeBoot: %v", err)
		}
		if boot.EnginePath != "claude" {
			t.Fatalf("EnginePath default = %q, want claude", boot.EnginePath)
		}
	})
}
