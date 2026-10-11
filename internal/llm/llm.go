// Package llm 定义模型提供方接缝(dsh ctx.llm 对齐):回合语义抽象,
// 不含任何具体引擎概念,也不提及任何具体引擎名——实现=命令行引擎
// 适配器插件(默认适配器,包装 internal/engine 的宿主机制)。FR-102。
package llm

import (
	"context"

	"mulix/internal/engine"
)

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
