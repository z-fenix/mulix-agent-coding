package tui

import (
	"context"

	"mulix/internal/control"
	"mulix/internal/subagent"
)

// SessionController 是 TUI 消费的会话控制面（*control.Controller 的子集——
// 状态机单测可注入替身）。
type SessionController interface {
	Submit(ctx context.Context, text string) error
	Approve(toolUseID string, allow bool, resolvedBy string) error
	ApproveSession(toolUseID, resolvedBy string) error
	Snapshot() control.Snapshot
	// Subagent 是子代理编排器的窄消费面（M12/T12.4 /agents 面板）；
	// nil = 子代理未启用（工厂未接线）。
	Subagent() *subagent.Manager
}

// SessionSwapper 是会话换绑接缝（组合根 internal/cli 实现；TUI 只见接口）：
// /new /resume 的异步换绑经此收口旧会话并以同 sink 开新/续链会话。事件通道
// 归 tui 所有——CloseCurrent 的收尾 publish 入 tui 的 sink，tui 在 Close 与
// Open 之间排空旧残响（顺序 close→drain→open 钉死，见 swapSession）。
type SessionSwapper interface {
	CloseCurrent()                                                              // 关当前会话（收尾 publish 入 sink）
	Open(resumeID string, sink control.Sink) (SessionController, string, error) // 同 sink 开新/续链会话
}
