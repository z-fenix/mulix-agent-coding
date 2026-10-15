package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// ---- 帧合成（FR-001：alt-screen 全接管布局）----
//
// 帧 = 转写视口（高 = H − 底部固定区行数；末列滚动条）+ 底部固定区。底部固定
// 区自上而下：模态面板（/agents，有则）→ 授权卡/编辑器（互斥）→ 状态行。
// 模态面板与授权卡的渲染沿用迁移前形态（023 Task 2 逐字节迁移件），本文件
// 负责把它们与视口合成满屏帧；末列滚动条为静态呈现（023 D5 布局面，拖拽随
// Task 21）。

// transcriptContentWidth 转写内容宽度：末列保留给滚动条（对照参考
// chat_tui.go:693-698 transcriptContentWidth），下限 1。换行缓存与 viewport
// 宽度必须同用此宽（finalize/frame 两处收口一致）。
func transcriptContentWidth(termW int) int {
	if w := termW - 1; w > 0 {
		return w
	}
	return 1
}

// bottomView 底部固定区：模态面板（有则）→ 授权卡/编辑器（互斥）→ 状态行。
// 编辑器为多行 textarea（023 D12/FR-007）——动态高度 1→8 行随内容收口，
// frame 的 lipgloss.Height 量测自动跟随（❯ 提示符仅首行，见 composer.go）。
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
		b.WriteString(m.composerState.ta.View())
		b.WriteString("\n")
	}
	b.WriteString(styleStatus.Render(m.statusLine()))
	return b.String()
}

// frame 帧合成：转写视口（高 = H − 底部固定区行数，下限 1；末列滚动条）+
// 底部固定区。视口按当前宽高同步（增量换行缓存，Task 5 起）；钉底由尾随
// 状态机裁决（023 D5）：跟随态下内容喂给或高度变化（模态开合收窄视口——
// #6430 的 height-only 场景）都钉底；上翻锁定下 offset 不动；forceGotoBottom
// 单帧豁免置底并恢复跟随（对照参考 chat_tui.go:989-1012 的 postUpdate 钉底）。
// 帧总行数恰为 H（alt-screen 满屏接管）。
func (m *tuiModel) frame() string {
	bottom := m.bottomView()
	vpH := m.height - lipgloss.Height(bottom)
	if vpH < 1 {
		vpH = 1
	}
	prevH := m.transcriptState.vp.Height()
	fed := m.transcriptState.syncViewport(transcriptContentWidth(m.width), vpH)
	if m.scrollState.forceGotoBottom || (m.shouldFollowTail() && (fed || vpH != prevH)) {
		m.transcriptState.vp.GotoBottom()
		m.markFollowTail() // force 消费即恢复跟随（对照参考 chat_tui.go:998-1012）
	}
	m.scrollState.forceGotoBottom = false
	return lipgloss.JoinHorizontal(lipgloss.Top, m.transcriptState.vp.View(), m.scrollbarView()) + "\n" + bottom
}

// scrollbarView 末列滚动条的静态呈现（023 D5；拖拽随 Task 21）：无溢出给
// 空白列；溢出时 thumb=█（随 offset 移动）/track=│。行数与视口一致，经
// JoinHorizontal 拼在内容右列（对照参考 transcript.go:429-455 renderTranscript
// 的滚动条列——参考经 theme 着色，配色随 Task 18 主题系统接入）。
func (m *tuiModel) scrollbarView() string {
	h := m.transcriptState.vp.Height()
	total := len(m.transcriptState.wrappedLines)
	yoff := m.transcriptState.vp.YOffset()
	thumbStart, thumbSize := scrollbarThumb(h, yoff, total)
	cells := make([]string, h)
	for r := range cells {
		cells[r] = scrollbarCell(r, total, h, thumbStart, thumbSize)
	}
	return strings.Join(cells, "\n")
}

// scrollbarThumb 返回视口 height 行、内容 total 行、滚动到 yoff 时 thumb 的
// [start, start+size) 行跨（对照参考 transcript.go:488-497）。
func scrollbarThumb(height, yoff, total int) (start, size int) {
	if total <= height {
		return 0, 0 // 无溢出 → 无 thumb
	}
	size = max(height*height/total, 1)
	maxYoff := total - height
	start = min(yoff*(height-size)/maxYoff, height-size)
	return start, size
}

// scrollbarCell 滚动条第 row 行的格子（对照参考 transcript.go:514-522）。
func scrollbarCell(row, total, height, thumbStart, thumbSize int) string {
	if total <= height {
		return " "
	}
	if row >= thumbStart && row < thumbStart+thumbSize {
		return "█"
	}
	return "│"
}
