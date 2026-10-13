package mulixkernel

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mulix/internal/ptc"
	"mulix/internal/registry"
)

// CordisRunTool 实现 registry.Tool:goja 沙箱执行任意 JS(016 FR-102;
// 复用 P5c ptc.Sandbox——JS 语言全量,安全白名单/超时同 P5c fail-closed;
// 沙箱内仅暴露 ReadOnly 注册工具——ptc.go 既有治理过滤)。
type CordisRunTool struct {
	tools   func() map[string]registry.Tool
	timeout time.Duration
}

// NewCordisRunTool 创建(tools=注册工具面快照函数;timeout=执行上限,30s 缺省)。
func NewCordisRunTool(tools func() map[string]registry.Tool, timeout time.Duration) *CordisRunTool {
	return &CordisRunTool{tools: tools, timeout: timeout}
}

func (t *CordisRunTool) Name() string { return "cordis_run" }
func (t *CordisRunTool) Description() string {
	return "在 fail-closed 沙箱中执行 JS 代码(全量语言;可调用注册工具)"
}
func (t *CordisRunTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"JS 代码(沙箱内执行;可调用注册工具函数)"}},"required":["code"]}`)
}
func (t *CordisRunTool) ReadOnly() bool { return false }

func (t *CordisRunTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var params struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("cordis_run: parse args: %w", err)
	}
	s := ptc.NewSandbox(t.tools(), t.timeout) // func 求值——015 execute.go 同款
	return s.Execute(params.Code)
}
