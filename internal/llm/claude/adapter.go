// Package claude 是 llm.Provider 的 claude CLI 适配器(默认实现)。
// **seam-only 形态**(011 裁定 b):Host 实例只在 controller.start 期存在,
// 本变更不绑定——方法集与 engine.Host 逐一对应,P4b-2 增加 Bind(host) 与
// 委托体即可,接口零变更。engine.lock 漂移治理随实现走(语义不变)。
package claude

import (
	"context"

	"mulix/internal/engine"
	"mulix/internal/llm"
)

// ClaudeAdapter 是 claude CLI 适配器(seam-only)。
type ClaudeAdapter struct{}

// NewClaudeAdapter 创建 seam-only 适配器。
func NewClaudeAdapter() *ClaudeAdapter { return &ClaudeAdapter{} }

// Name 返回适配器名(engine.lock 治理随此实现走)。
func (a *ClaudeAdapter) Name() string { return "claude-cli" }

// Start 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Start(ctx context.Context, cfg engine.BootConfig) error {
	return llm.ErrNotBound
}

// Send 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Send(ctx context.Context, msg engine.UserMessage) error {
	return llm.ErrNotBound
}

// Events 未绑定 → nil 流(fail-closed:无宿主即无事件)。
func (a *ClaudeAdapter) Events() <-chan engine.Event { return nil }

// RespondControl 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error {
	return llm.ErrNotBound
}

// Interrupt 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Interrupt(ctx context.Context) error { return llm.ErrNotBound }

// Close 未绑定 → nil(无资源)。
func (a *ClaudeAdapter) Close(ctx context.Context) error { return nil }

// Info 未绑定 → 零值。
func (a *ClaudeAdapter) Info() engine.EngineInfo { return engine.EngineInfo{} }
