package control

// M14 评审修复轮 1（F2 接线 + F3 零 delta）：cache.Compose 以全组最小
// timeout_ms 算出的引擎 env MCP_TOOL_TIMEOUT 经 composeBoot 并入 Boot Env
// 通道（与回调地址同面）；无外部声明 → boot.Env 零 delta（EngineEnv 为 nil）。

import (
	"context"
	"strings"
	"testing"

	"mulix/internal/config"
	"mulix/internal/engine"
)

// TestMCPToolTimeoutWiredToBootEnv 钉：外部 server 配置 → 引擎 boot env 恰含
// MCP_TOOL_TIMEOUT=<全组最小生效值>（Boot Env 通道，绝不入 mcp-config 字节
// ——SC-003）；无外部声明 → boot.Env 无 MCP_TOOL_TIMEOUT（P1-only 零 delta）。
func TestMCPToolTimeoutWiredToBootEnv(t *testing.T) {
	t.Run("external servers = timeout in boot env", func(t *testing.T) {
		fake := engine.NewFakeHost(engine.FakeScript{})
		sink := &recordingSink{}
		var boot engine.BootConfig
		c := newTestControllerCapturingBoot(t, fake, sink, &boot, func(o *Options) {
			cfg := mustDefaultConfig()
			cfg.MCP.Servers = []config.MCPServerDecl{
				{Name: "slow", Command: []string{"s"}, TimeoutMS: 120000},
				{Name: "fast", Command: []string{"f"}, TimeoutMS: 30000},
			}
			o.Config = cfg
		})
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		if !envContains(boot.Env, "MCP_TOOL_TIMEOUT=30000") {
			t.Fatalf("boot env must carry MCP_TOOL_TIMEOUT=min(120000,30000) (env=%v)", boot.Env)
		}
		if strings.Contains(boot.MCPConfigJSON, "MCP_TOOL_TIMEOUT") {
			t.Fatalf("MCP_TOOL_TIMEOUT must never enter mcp-config bytes: %s", boot.MCPConfigJSON)
		}
	})
	t.Run("no external servers = zero boot env delta", func(t *testing.T) {
		fake := engine.NewFakeHost(engine.FakeScript{})
		sink := &recordingSink{}
		var boot engine.BootConfig
		c := newTestControllerCapturingBoot(t, fake, sink, &boot)
		if err := c.NewSession(context.Background()); err != nil {
			t.Fatalf("new session: %v", err)
		}
		for _, e := range boot.Env {
			if strings.HasPrefix(e, "MCP_TOOL_TIMEOUT=") {
				t.Fatalf("P1-only config must not gain MCP_TOOL_TIMEOUT (env=%v)", boot.Env)
			}
		}
	})
}

// envContains 报告 env 列表是否恰含给定 "KEY=VALUE" 项。
func envContains(env []string, entry string) bool {
	for _, e := range env {
		if e == entry {
			return true
		}
	}
	return false
}
