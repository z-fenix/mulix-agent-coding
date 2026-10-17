package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// ---- 帧合成（FR-001：alt-screen 全接管布局）----
//
// 帧 = 转写视口（高 = H − 底部固定区行数；末列滚动条）+ 底部固定区。底部固定
// 区自上而下：模态面板（/agents，有则）→ 授权卡/编辑器（互斥）→ 状态行。
// 模态面板渲染沿用迁移前形态（023 Task 2 逐字节迁移件）；授权卡键位与参数
// 摘要见 approval.go（023 D11）。本文件负责把它们与视口合成满屏帧；末列滚动
// 条拖拽与选区高亮随 Task 21 落地（023 Story4——transcript.go transcriptView
// 绘制可见窗口）。

// transcriptContentWidth 转写内容宽度：末列保留给滚动条（对照参考
// chat_tui.go:693-698 transcriptContentWidth），下限 1。换行缓存与 viewport
// 宽度必须同用此宽（finalize/frame 两处收口一致）。
func transcriptContentWidth(termW int) int {
	if w := termW - 1; w > 0 {
		return w
	}
	return 1
}

// bottomView 底部固定区：模态面板（有则）→ 授权卡/选择器/编辑器（互斥）→
// 状态行。编辑器为多行 textarea（023 D12/FR-007）——动态高度 1→8 行随内容
// 收口，frame 的 lipgloss.Height 量测自动跟随（❯ 提示符仅首行，见 composer.go）；
// 补全菜单浮在编辑器上方（023 FR-009——无框行列表紧贴输入行，对照参考
// renderCompletion；菜单是邻接浮层，编辑器保持活跃）。
func (m *tuiModel) bottomView() string {
	var b strings.Builder
	switch {
	case m.approval != nil:
		// 卡片文本（键位 + 参数摘要）见 approval.go approvalCardView（023 D11）；
		// surface1 卡底整块铺面（设计 §6 授权卡——accent 边框 + surface1 底 +
		// 键帽，Task 7）。
		b.WriteString(paintSurface(styleCard.Render(m.approvalCardView()), activeTheme.surface1Color))
		b.WriteString("\n")
	case m.agentsOpen:
		b.WriteString(styleCard.Render(m.agentsPanelView()))
		b.WriteString("\n")
	case m.picker != nil:
		// resume 选择器 surface1 卡底（设计 §6——与授权卡同法；选中行 accent
		// 在 pickerView 内）。
		b.WriteString(paintSurface(styleCard.Render(m.pickerView()), activeTheme.surface1Color))
		b.WriteString("\n")
	default:
		if menu := m.completeMenuView(); menu != "" {
			b.WriteString(menu)
			b.WriteString("\n")
		}
		b.WriteString(m.composerView())
		b.WriteString("\n")
	}
	b.WriteString(styleStatus.Render(m.statusView()))
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
	// 阅读锚点恢复（024 FR-001）：新宽重排完成后按块对位置回顶行，一次性
	// 消费；尾随态不恢复（钉底接管，锚点作废）。
	if m.pendingAnchor != nil {
		if !m.shouldFollowTail() {
			m.transcriptState.vp.SetYOffset(m.transcriptState.offsetForAnchor(*m.pendingAnchor))
		}
		m.pendingAnchor = nil
	}
	if m.scrollState.forceGotoBottom || (m.shouldFollowTail() && (fed || vpH != prevH)) {
		m.transcriptState.vp.GotoBottom()
		m.markFollowTail() // force 消费即恢复跟随（对照参考 chat_tui.go:998-1012）
	}
	m.scrollState.forceGotoBottom = false
	// 可见窗口经 transcriptView 绘制（选区高亮内建）——viewport 只持滚动状态，
	// 帧面按 h 行绘制（对照参考 renderTranscript；023 Story4 Task 21）。
	// 满帧铺底（设计 §4.3）：出口统一收口——帧每行（转写视口 + 滚动条列 +
	// 底部固定区）过 paintBG 整行铺 bg 槽；无色层原样返回，结构不变。
	rendered := lipgloss.JoinHorizontal(lipgloss.Top, m.transcriptState.transcriptView(), m.scrollbarView()) + "\n" + bottom
	return paintBG(rendered, m.width)
}

// sgrResetRe 整段重置型 SGR（参数空或全零——\x1b[m / \x1b[0m / \x1b[0;0m）：
// 铺底重铺的锚点。非全零 SGR（如 38;5;N）只改属性不清底，无需重铺；组合
// reset+属性 序（\x1b[0;38;5;Nm）不在本仓产出面（sgrWrap 与 lipgloss 均
// 分序发出），出现时需回访。
var sgrResetRe = regexp.MustCompile(`\x1b\[(0+(;0+)*)?m`)

// paintBG 满帧铺底（设计 §4.3）：rendered 逐行右补空格到 width 显示宽
// （padToWidth——ansi.StringWidth 计量，CJK 安全）并整行铺 bg 槽（lipgloss
// Background+Width——styleFrame 由 refreshThemeStyles 按 activeTheme 重铸）；
// bg 槽空（无色层）原样返回，不补空格不铺底。行内既有 SGR 的整段重置会把
// 底色一并清掉——重置点即重铺 bg（SGR 叠加语义：fg 序不清底），否则样式段
// 之后的正文落回终端默认底，「整行铺底」不成立。
func paintBG(rendered string, width int) string {
	bg := sgrBg(activeTheme.bgColor)
	if bg == "" {
		return rendered
	}
	seq := "\x1b[" + bg + "m"
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		p := padToWidth(line, width)
		if strings.Contains(p, "\x1b[") {
			p = sgrResetRe.ReplaceAllString(p, "$0"+seq)
		}
		lines[i] = styleFrame.Width(width).Render(p)
	}
	return strings.Join(lines, "\n")
}

// paintSurface 面层级铺底（设计 §4 表面槽消费——授权卡/resume 选择器 surface1
// 卡底含边框、补全菜单 surface1 浮层底；Task 7）：整行铺 slot 槽底；行内整段
// 重置型 SGR 的重置点即重铺（与 paintBG/fenceBG 同锚点同法——合成序 重置 →
// 帧底（帧出口 paintBG 插序在前）→ 面底重铺在后，后写生效，与
// TestFenceBGSurvivesFramePaint 钉的三元序同构）；行尾恒补 reset——重铺不悬置
// 到行尾 padding（pad 归帧底）。空槽（无色层）原样返回，不铺底。
func paintSurface(s string, slot themeColor) string {
	bg := sgrBg(slot)
	if bg == "" {
		return s
	}
	seq := "\x1b[" + bg + "m"
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\x1b[") {
			line = sgrResetRe.ReplaceAllString(line, "$0"+seq)
		}
		lines[i] = seq + line + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// scrollbarView 末列滚动条（023 D5；拖拽随 Task 21 落地——transcript.go
// inScrollbar/dragScrollbar 接线）：无溢出给空白列；溢出时 thumb=█（随
// offset 移动）/track=│。行数与视口一致，经 JoinHorizontal 拼在内容右列
// （对照参考 transcript.go:429-455 renderTranscript 的滚动条列——参考经
// theme 着色，T18 接入主题槽：thumb=accent / track=dim，对照参考
// scrollThumbStyle/scrollTrackStyle）。
func (m *tuiModel) scrollbarView() string {
	h := m.transcriptState.vp.Height()
	total := len(m.transcriptState.wrappedLines)
	yoff := m.transcriptState.vp.YOffset()
	thumbStart, thumbSize := scrollbarThumb(h, yoff, total)
	thumb, track := activeTheme.accent("█"), activeTheme.dim("│")
	cells := make([]string, h)
	for r := range cells {
		switch scrollbarCell(r, total, h, thumbStart, thumbSize) {
		case "█":
			cells[r] = thumb
		case "│":
			cells[r] = track
		default:
			cells[r] = " "
		}
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
