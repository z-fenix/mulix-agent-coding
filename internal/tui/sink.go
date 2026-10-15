package tui

import (
	"charm.land/bubbletea/v2"
)

// capTuiEventCh 是事件通道容量：满时 Publish 阻塞背压（前端可慢不可丢——
// 镜像先行的对面；阻塞的是 control 的 publish，天然节流引擎事件流）。
// 已知限制（T28 评审 Minor-C，P2 权衡）：退出/换绑时若积压 >容量，旧泵的
// 收尾 publish 会阻塞至新消费者排空——语义为「延迟」而非「丢失/死锁」。
const capTuiEventCh = 1024

// tuiEvent 是 control 事件流转成 tea 消息的信封。
type tuiEvent struct{ ev any }

// Sink 把 control 事件流转成 tea 消息（control.Sink 实现）。通道是 TUI 的
// 唯一事件源：初始会话与换绑后的新会话共享同一 Sink——SessionSwapper.Open
// 的 sink 入参由模型供给本字段（新会话事件续流同一通道）。
type Sink struct {
	ch chan tuiEvent
}

// NewSink 构造事件汇（容量 capTuiEventCh）。
func NewSink() *Sink {
	return &Sink{ch: make(chan tuiEvent, capTuiEventCh)}
}

// Publish 实现 control.Sink：满则阻塞背压（可慢不可丢）。
func (s *Sink) Publish(ev any) {
	s.ch <- tuiEvent{ev: ev}
}

// Events 返回只读消费侧（Run 的事件源；换绑排空读同一通道）。
func (s *Sink) Events() <-chan tuiEvent { return s.ch }

// waitForEvent 把事件通道挂成阻塞型 tea.Cmd。
func waitForEvent(ch <-chan tuiEvent) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}
