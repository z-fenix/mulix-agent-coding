// PTCExecuteTool 实现 registry.Tool:模型提交 PTC 脚本(参数=脚本文本)→
// Sandbox 执行 → 结果返回(治理回路照旧)。standard profile 注册。
package ptc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mulix/internal/registry"
)

// PTCExecuteTool 实现 registry.Tool。
type PTCExecuteTool struct {
	tools   func() map[string]registry.Tool
	timeout time.Duration
}

// NewPTCExecuteTool 创建(tools=延迟取工具快照——组合根注册完后调用)。
func NewPTCExecuteTool(tools func() map[string]registry.Tool, timeout time.Duration) *PTCExecuteTool {
	return &PTCExecuteTool{tools: tools, timeout: timeout}
}

func (t *PTCExecuteTool) Name() string        { return "ptc_execute" }
func (t *PTCExecuteTool) Description() string { return "执行 PTC 脚本组合多步工具调用" }
func (t *PTCExecuteTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"script":{"type":"string","description":"JS 脚本(调用注册工具组合多步操作)"}},"required":["script"]}`)
}
func (t *PTCExecuteTool) ReadOnly() bool { return false }

func (t *PTCExecuteTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("ptc: parse args: %w", err)
	}
	s := NewSandbox(t.tools(), t.timeout)
	return s.Execute(params.Script)
}
