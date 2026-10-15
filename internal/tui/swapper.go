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
	// Cancel 取消当前回合（D10 Esc 取消阶梯的唯一引擎触点；签名与语义 =
	// control.Controller.Cancel——控制协议 interrupt，等待裁决的待审请求一并
	// fail-closed 拒绝）。组合根直供 *control.Controller，无需转发层。
	Cancel(ctx context.Context) error
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

// SessionSummary 是 /resume 选择器的会话摘要行（023 FR-009；组合根经
// Options.ListSessions 供给——cli 侧由 sessionx.ScanSessions 的 SessionInfo
// 投影；tui 不 import sessionx，组合根注入面与 Doctor 同法）。EngineID 是
// 换绑 Resume 的实参——空链会话不可续（cli 侧投影已滤除；防御面兜底拒绝）。
type SessionSummary struct {
	ID         string // mulix 会话 id（镜像目录名）
	EngineID   string // 引擎 session id（Open(resumeID) 的实参）
	Model      string
	Workspace  string
	CreatedAt  string // RFC3339（ScanSessions 已按新在前排序）
	Unattended bool   // 无人值守会话标注（选择器行内呈现）
}
