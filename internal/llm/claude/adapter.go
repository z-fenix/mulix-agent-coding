// Package claude 是 llm.Provider 的 claude CLI 适配器(默认实现)。
// **seam-only 形态**(011 裁定 b):Host 实例只在 controller.start 期存在,
// 本变更不绑定——方法集与 engine.Host 逐一对应,P4b-2 增加 Bind(host) 与
// 委托体即可,接口零变更。engine.lock 漂移治理随实现走(语义不变)。
package claude

import (
	"context"
	"fmt"
	"sync"

	"mulix/internal/engine"
	"mulix/internal/llm"
)

// ClaudeAdapter 是 claude CLI 适配器(seam-only 形态 + P4b-2 Bind 升级)。
type ClaudeAdapter struct {
	mu   sync.Mutex
	host engine.Host // Bind 后就位(会话生命周期一次)
}

// NewClaudeAdapter 创建 seam-only 适配器。
func NewClaudeAdapter() *ClaudeAdapter { return &ClaudeAdapter{} }

// Name 返回适配器名(engine.lock 治理随此实现走)。
func (a *ClaudeAdapter) Name() string { return "claude-cli" }

// Start 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Start(ctx context.Context, cfg engine.BootConfig) error {
	if h := a.Host(); h != nil {
		return h.Start(ctx, cfg)
	}
	return llm.ErrNotBound // 未绑定:fail-closed(既有语义保持)
}

// Send 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Send(ctx context.Context, msg engine.UserMessage) error {
	if h := a.Host(); h != nil {
		return h.Send(ctx, msg)
	}
	return llm.ErrNotBound
}

// Events 未绑定 → nil 流(fail-closed:无宿主即无事件)。
func (a *ClaudeAdapter) Events() <-chan engine.Event {
	if h := a.Host(); h != nil {
		return h.Events()
	}
	return nil // 未绑定:fail-closed(既有语义保持)
}

// RespondControl 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error {
	if h := a.Host(); h != nil {
		return h.RespondControl(ctx, id, payload)
	}
	return llm.ErrNotBound
}

// Interrupt 未绑定 → ErrNotBound。
func (a *ClaudeAdapter) Interrupt(ctx context.Context) error {
	if h := a.Host(); h != nil {
		return h.Interrupt(ctx)
	}
	return llm.ErrNotBound
}

// Close 未绑定 → nil(无资源)。
func (a *ClaudeAdapter) Close(ctx context.Context) error {
	if h := a.Host(); h != nil {
		return h.Close(ctx)
	}
	return nil // 未绑定:无资源
}

// Info 未绑定 → 零值。
func (a *ClaudeAdapter) Info() engine.EngineInfo {
	if h := a.Host(); h != nil {
		return h.Info()
	}
	return engine.EngineInfo{} // 未绑定:零值
}

// Bind 绑定宿主实例(会话生命周期一次;重复 Bind 报错——start 路径唯一调用点)。
// 绑定后 seam-only 的 ErrNotBound 形态退役:回合方法逐方法委托宿主(012 FR-101a)。
func (a *ClaudeAdapter) Bind(host engine.Host) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.host != nil {
		return fmt.Errorf("llm/claude: adapter already bound")
	}
	a.host = host
	return nil
}

// Host 返回绑定的宿主(未绑定 nil——EventSource.Host() 透传面)。
func (a *ClaudeAdapter) Host() engine.Host {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.host
}
