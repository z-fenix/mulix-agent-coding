// Package agentloop 提供回合循环抽象(P4b-2,dsh ctx.agentLoop 对齐):
// loop 可替换;默认驱动=controller 泵逻辑逐字节搬家(解码后事件按 Kind
// 路由到治理回调集;流关闭 → OnEngineClosed)。FR-101。
// 依赖约束:不 import control——治理动作经 Callbacks 小接口注入(方法数
// =事件类数;FR-103)。
package agentloop

import (
	"context"

	"mulix/internal/engine"
)

// EventSource 是事件源最小接口(llm.Provider 满足——012 裁定 A:
// agent loop 消费 ctx.llm)。
type EventSource interface {
	Events() <-chan engine.Event
	Host() engine.Host // OnInit/OnControlRequest 的宿主透传
}

// Callbacks 是治理动作回调集(controller 实现之;方法数=事件类数)。
type Callbacks interface {
	OnInit(host engine.Host, ev engine.Event)
	OnAssistant(ev engine.Event)
	OnToolResult(ev engine.Event)
	OnResult(ev engine.Event)
	OnTrace(ev engine.Event)
	OnPermissionDenied(ev engine.Event)
	OnExit(ev engine.Event)
	OnControlRequest(host engine.Host, ev engine.Event)
	OnHookResponse(ev engine.Event)
	OnEngineClosed()
}

// Loop 是回合循环抽象(可替换——P5 PTC 模式在此接缝扩展)。
type Loop interface {
	Run(ctx context.Context, src EventSource, cb Callbacks) error
}

// Default 是默认驱动:泵逻辑逐字节搬家(controller.go pump/dispatch,
// 012 FR-101)。
type Default struct{}

// Run 消费事件流直至关闭,按 Kind 路由到回调;流关闭 → OnEngineClosed。
func (Default) Run(ctx context.Context, src EventSource, cb Callbacks) error {
	for ev := range src.Events() {
		dispatch(src, cb, ev)
	}
	cb.OnEngineClosed()
	return nil
}

// dispatch 单事件路由(与 controller 既有 dispatch 语义逐字节一致;default
// 未知 kind → 跳过——design §1.2 裁定,扩展点留 P5)。
func dispatch(src EventSource, cb Callbacks, ev engine.Event) {
	// src 可为 nil(Replay 历史重放无宿主——host 收 nil)。
	var host engine.Host
	if src != nil {
		host = src.Host()
	}
	switch ev.Kind {
	case engine.KindInit:
		cb.OnInit(host, ev)
	case engine.KindAssistant:
		cb.OnAssistant(ev)
	case engine.KindToolResult:
		cb.OnToolResult(ev)
	case engine.KindResult:
		cb.OnResult(ev)
	case engine.KindTrace:
		cb.OnTrace(ev)
	case engine.KindPermissionDenied:
		cb.OnPermissionDenied(ev)
	case engine.KindExit:
		cb.OnExit(ev)
	case engine.KindControlRequest:
		cb.OnControlRequest(host, ev)
	case engine.KindHookResponse:
		cb.OnHookResponse(ev)
	default:
		// 未知 kind:跳过(与现状 dispatch default 一致——design §1.2 裁定,
		// 不进回调;扩展点留 P5)。
	}
}

// Replay 将历史事件流经 Callbacks 重放(只读——零镜像追加;观测面)。
// 事件序列经同一 dispatch 路由(与引擎流语义一致);OnEngineClosed 不触发
// (非引擎关闭——历史重放无关闭语义)。FR-102。
func Replay(ctx context.Context, events []engine.Event, cb Callbacks) error {
	for _, ev := range events {
		dispatch(EventSource(nil), cb, ev)
	}
	return nil
}
