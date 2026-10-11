// Package llm 定义模型提供方接缝(dsh ctx.llm 对齐):回合语义抽象,
// 不含任何具体引擎概念,也不提及任何具体引擎名——实现=命令行引擎
// 适配器插件(默认适配器,包装 internal/engine 的宿主机制)。FR-102。
package llm

import (
	"context"
	"fmt"

	"mulix/internal/engine"
)

// ErrNotBound 是 seam-only 形态 Provider 的回合方法返回值:fail-closed,
// 不静默(委托接线落地前的显式占位错误——010 裁定 b 形态)。
var ErrNotBound = fmt.Errorf("llm: provider not bound (seam-only; delegation lands in P4b-2)")

// Provider 是引擎回合的抽象接缝。形状与 engine.Host 逐一对应(boot 提交、
// 消息发送、事件流、控制应答、打断、收口、信息)——engine.BootConfig/
// UserMessage/ControlResponse/EngineInfo 为通用引擎概念(非具体引擎特有)。
// 接缝纯净性验收=本文件无具体引擎名字样。实现=默认命令行引擎适配器插件。
type Provider interface {
	Name() string
	Start(ctx context.Context, cfg engine.BootConfig) error
	Send(ctx context.Context, msg engine.UserMessage) error
	Events() <-chan engine.Event
	RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error
	Interrupt(ctx context.Context) error
	Close(ctx context.Context) error
	Info() engine.EngineInfo
}

// FakeProvider 是测试装配用的 seam-only Provider(Name()="fake";同形态)。
type FakeProvider struct{}

// NewFakeProvider 创建测试装配 Provider。
func NewFakeProvider() *FakeProvider { return &FakeProvider{} }

// Name 返回 "fake"。
func (p *FakeProvider) Name() string { return "fake" }

// Start 未绑定 → ErrNotBound(同 seam-only 语义)。
func (p *FakeProvider) Start(ctx context.Context, cfg engine.BootConfig) error { return ErrNotBound }

// Send 未绑定 → ErrNotBound。
func (p *FakeProvider) Send(ctx context.Context, msg engine.UserMessage) error { return ErrNotBound }

// Events 未绑定 → nil。
func (p *FakeProvider) Events() <-chan engine.Event { return nil }

// RespondControl 未绑定 → ErrNotBound。
func (p *FakeProvider) RespondControl(ctx context.Context, id string, payload engine.ControlResponse) error {
	return ErrNotBound
}

// Interrupt 未绑定 → ErrNotBound。
func (p *FakeProvider) Interrupt(ctx context.Context) error { return ErrNotBound }

// Close 未绑定 → nil。
func (p *FakeProvider) Close(ctx context.Context) error { return nil }

// Info 未绑定 → 零值。
func (p *FakeProvider) Info() engine.EngineInfo { return engine.EngineInfo{} }
