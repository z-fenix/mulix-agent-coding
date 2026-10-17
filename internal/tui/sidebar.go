package tui

import (
	"strings"

	"charm.land/bubbletea/v2"
)

// ---- 侧栏（2026-10-17-dsh-tui-visual-redesign Task 11；设计 §6 侧栏/§7 符号）----
//
// 左栏会话清单：顶品牌行 ◆ mulix（accent）、「会话」目、会话行（当前会话
// surface2 整行高亮 + fg-primary，其余 fg-secondary，超宽行省略号截断）、
// 底部 /new 提示；空清单给行动指引文案。清单数据本任务为桩（
// sidebarState.sessions 由测试直配；Task 13 接 Options.ListSessions 打开时
// 快照——同 picker 口径）。列合成与 F3 开合守卫见 sidebarColumn/toggleSidebar。

// sidebarState 侧栏状态：sel 选中游标（Task 13 聚焦导航接线 ↑/↓）；sessions
// 会话清单快照（Task 13 起打开时刷新、不随 Scan 变化——picker 同口径；本任务
// 为测试桩，生产组合根尚未接线数据源）。
type sidebarState struct {
	sel      int
	sessions []SessionSummary
}

// toggleSidebar F3 开合（brief）：W<100 窄档不进入开态并经 appendLine 留痕
// （档位判定用 W 直接比较——Task 9 契约：columnWidths 的 sidebar==0 亦可能
// 来自 open=false，不是窄档判据；提示走 appendLine 非 appendNotice）。开合
// 生效时：重排前显式捕获阅读锚点（评审 Issue 4——同宽开合不走 WindowSizeMsg，
// 见 panel.go capturePendingAnchor），composer 跟 mainWidth 重排（同宽开合
// 不走 WindowSizeMsg——model.go Update 注记的开合同步）。
func (m *tuiModel) toggleSidebar() tea.Cmd {
	if m.width < 100 {
		m.appendLine("[侧栏] 侧栏需 ≥100 列")
		return nil
	}
	m.capturePendingAnchor()
	m.sidebarOpen = !m.sidebarOpen
	m.composerState.ta.SetWidth(m.mainWidth())
	return nil
}

// sidebarView 侧栏列视图（宽 w = columnWidths 的 sidebar 档，16）：品牌行 →
// 「会话」目 →（空清单：指引文案）→ 清单行 → 底部提示。行内容 = shortID(ID)
// + 工作区（Task 13 数据源同谓词——新在前）；当前会话（selfID 匹配
// SessionSummary.ID）整行 surface2 高亮 + fg-primary（先截断再 padToWidth
// ——底色覆盖整列宽），其余 fg-secondary；超宽行 clampWidth 省略号截断。
func (m *tuiModel) sidebarView(width int) string {
	var b strings.Builder
	b.WriteString(activeTheme.accent(symBrand+" mulix") + "\n")
	b.WriteString(activeTheme.dim("会话") + "\n")
	if len(m.sidebar.sessions) == 0 {
		// 空态行动指引（spec User Story 4：给文案而非空白；/new 提示恒在底行）。
		b.WriteString(activeTheme.dim("暂无历史会话") + "\n")
	}
	for _, it := range m.sidebar.sessions {
		row := shortID(it.ID)
		if it.Workspace != "" {
			row += " " + it.Workspace
		}
		row = clampWidth(row, width)
		if it.ID == m.selfID {
			b.WriteString(sidebarSelWrap(padToWidth(row, width)) + "\n")
			continue
		}
		b.WriteString(sgrWrap(sgrFg(activeTheme.fgSecondaryColor), row) + "\n")
	}
	b.WriteString(activeTheme.dim("/new 新会话") + "\n")
	return b.String()
}

// sidebarSelWrap 当前会话行整行高亮（设计 §6：surface2 底 + fg-primary 文字）
// ——fg/bg 槽合成单条 SGR（空槽跳过；双空回落平文——NO_COLOR 层不着色不破
// 结构，TestNoColorFrameStructure 口径一致）。调用方先行 padToWidth，底色
// 覆盖整列宽；行尾 reset 由帧出口 paintBG 的重置点重铺锚接（layout.go）。
func sidebarSelWrap(s string) string {
	fg := sgrFg(activeTheme.fgColor)
	bg := sgrBg(activeTheme.surface2Color)
	switch {
	case fg == "" && bg == "":
		return s
	case fg == "":
		return sgrWrap(bg, s)
	case bg == "":
		return sgrWrap(fg, s)
	}
	return sgrWrap(fg+";"+bg, s)
}

// sidebarColumn 侧栏列合成（frame 消费，Task 11——取代 Task 10 的空白占位）：
// sidebarView 逐行右补空格收口列宽、不足帧高补空白行——与 panelColumn 同契约
// （Task 12；列高 = m.height、列宽 = sidebar 档），保证 JoinHorizontal 列位与
// 帧宽（列和 = W）不变。w≤0 防御性返回空串（调用方已按档位让位过滤）。
func (m *tuiModel) sidebarColumn(w int) string {
	if w <= 0 {
		return ""
	}
	lines := strings.Split(m.sidebarView(w), "\n")
	rows := make([]string, max(m.height, 1))
	for r := range rows {
		if r < len(lines) {
			rows[r] = padToWidth(lines[r], w)
			continue
		}
		rows[r] = strings.Repeat(" ", w)
	}
	return strings.Join(rows, "\n")
}
