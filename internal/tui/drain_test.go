package tui

import (
	"testing"

	"mulix/internal/event"
)

// ---- 排水测试辅助（brief 形态适配 tui_test.go 现有替身）----

// sinkPub 直塞事件通道（绕过 tea runtime；满则阻塞背压——同生产 Publish）。
func (m *tuiModel) sinkPub(ev any) { m.sink.Publish(ev) }

// transcriptLineCount 是排水消费量的直接观测面（每条 notice 恰好一行——
// 块模型迁移下按渲染行计数，语义不变：事件→可见行）。
func (m *tuiModel) transcriptLineCount() int {
	n := 0
	for _, b := range m.transcriptState.blocks {
		n += len(b.render(m.width))
	}
	return n
}

// drainOne 从通道取下一条事件（测试循环驱动后续 Update）。
func (m *tuiModel) drainOne(t *testing.T) tuiEvent {
	t.Helper()
	return <-m.events
}

// TestDrainCoalescing pins（D4 洪峰合并）：一次 Update 内首条 tuiEvent 触发
// 非阻塞排水——合并消费至多 maxEventDrain(512) 条后统一收口；超出上限的
// 部分留通道由后续 Update 继续（不丢）。n=700 > 512 → 必然分两次 Update。
func TestDrainCoalescing(t *testing.T) {
	m, _, _ := newTestModel(t) // testMode：不回挂阻塞 cmd
	const n = 700              // > maxEventDrain(512)，分两次 Update 消费完
	for i := 0; i < n; i++ {
		m.sinkPub(event.NoticeEvent{Seq: int64(i), Content: "x"}) // 直塞通道
	}
	m = update(t, m, <-m.events) // 第一条触发排水（tuiEvent 本身即 tea.Msg——不可再包一层信封）
	if got := m.transcriptLineCount(); got != maxEventDrain {
		t.Fatalf("第一次 Update 应消费 %d 条, got %d", maxEventDrain, got)
	}
	m = update(t, m, m.drainOne(t)) // 继续消费（测试循环驱动）
	if got := m.transcriptLineCount(); got != n {
		t.Fatalf("应全部消费不丢, got %d, want %d", got, n)
	}
}
