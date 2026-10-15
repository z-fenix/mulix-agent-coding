package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// ---- 帧合成（FR-001：alt-screen 全接管布局）----
//
// 帧 = 转写视口（高 = H − 底部固定区行数）+ 底部固定区。底部固定区自上而下：
// 模态面板（/agents，有则）→ 授权卡/编辑器（互斥）→ 状态行。模态面板与
// 授权卡的渲染沿用迁移前形态（023 Task 2 逐字节迁移件），本文件只负责
// 把它们与视口合成满屏帧。

// bottomView 底部固定区：模态面板（有则）→ 授权卡/编辑器（互斥）→ 状态行。
func (m *tuiModel) bottomView() string {
	var b strings.Builder
	if m.approval != nil {
		card := fmt.Sprintf("工具 %s（%s）等待授权\n[a] 本次允许  [s] 本会话允许  [d] 拒绝",
			m.approval.tool, shortID(m.approval.toolUseID))
		b.WriteString(styleCard.Render(card))
		b.WriteString("\n")
	} else if m.agentsOpen {
		b.WriteString(styleCard.Render(m.agentsPanelView()))
		b.WriteString("\n")
	} else {
		b.WriteString("> " + m.input)
		b.WriteString("\n")
	}
	b.WriteString(styleStatus.Render(m.statusLine()))
	return b.String()
}

// frame 帧合成：转写视口（高 = H − 底部固定区行数，下限 1）+ 底部固定区。
// 视口按当前宽高同步（全量重渲染 + 克隆喂给 + 跟随尾部——Task 5 起
// wrapcache 增量）。帧总行数恰为 H（alt-screen 满屏接管）。
func (m *tuiModel) frame() string {
	bottom := m.bottomView()
	vpH := m.height - lipgloss.Height(bottom)
	if vpH < 1 {
		vpH = 1
	}
	m.transcriptState.syncViewport(m.width, vpH)
	return m.transcriptState.vp.View() + "\n" + bottom
}
