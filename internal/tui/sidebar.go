package tui

import (
	"strings"

	"charm.land/bubbletea/v2"
)

// ---- 侧栏（2026-10-17-dsh-tui-visual-redesign Task 11/13；设计 §6 侧栏/§7 符号）----
//
// 左栏会话清单：顶品牌行 ◆ mulix（accent）、「会话」目、会话行（当前会话
// surface2 整行高亮 + fg-primary，sel 行「› 」指示 + accent，其余
// fg-secondary，超宽行省略号截断）、底部 /new 提示；空清单给行动指引文案。
// 数据源 Task 13 接线：Options.ListSessions 与 /resume 选择器同源——打开时刻
// 快照（refreshSidebarSessions），溢出走取景窗（listWindow——Task 11 deferred
// minor 闭环）。列合成与 F3 开合守卫见 sidebarColumn/toggleSidebar。

// sidebarState 侧栏状态：sel 选中游标（聚焦侧栏时 ↑/↓ 环绕——Task 13 接线）；
// sessions 会话清单快照（Task 13 起打开时刷新、不随 Scan 变化——picker 同口径）。
type sidebarState struct {
	sel      int
	sessions []SessionSummary
}

// sidebarChromeRows 侧栏固定 chrome 行数（品牌行 + 「会话」目 + 底部 /new 提示
// ——取景窗之外的行预算）；sidebarSelMark sel 行指示（「› 」——设计 §7 符号
// 表外新增，与 symFold ▸ 折叠标记不冲突；Task 11 deferred minor「sel 无可见
// 游标」闭环）。
const (
	sidebarChromeRows = 3
	sidebarSelMark    = "› "
)

// listWindow 清单取景窗（Task 13——Task 11 deferred minor「侧栏清单溢出无滚动」
// 闭环）：返回 [lo,hi) 行窗。n≤avail 全量；否则 sel 居中锚定取景、两端截断
// 收口；avail 下限 1（sel 行恒可见）。无偏移状态——窗纯由 sel 派生（快照刷新
// 零同步负担；环绕导航可达全部行，无静默行丢失）。
func listWindow(n, avail, sel int) (lo, hi int) {
	if n <= 0 {
		return 0, 0
	}
	if sel >= n {
		sel = n - 1
	}
	if sel < 0 {
		sel = 0
	}
	if avail < 1 {
		avail = 1
	}
	if n <= avail {
		return 0, n
	}
	lo = sel - (avail-1)/2
	if lo < 0 {
		lo = 0
	}
	if maxLo := n - avail; lo > maxLo {
		lo = maxLo
	}
	return lo, lo + avail
}

// refreshSidebarSessions 侧栏清单快照刷新（Task 13——picker 同口径：打开时刻
// 快照不随 Scan 变化）。三个刷新点：构造（newTuiModel——缺省开态的首个「打开
// 时刻」）、F3 开启沿（toggleSidebar）、换绑收口（swapDoneMsg 成功路径——新
// 会话身份入清单）。回调未注入 → 清单保持空（空态文案即反馈，不 appendLine
// ——侧栏是常驻面板非命令入口，与 picker 的显式调用留痕不同口径）。快照刷新
// 即游标复位（清单身份随快照变化，旧游标无指向）。
func (m *tuiModel) refreshSidebarSessions() {
	m.sidebar.sel = 0
	if m.opts.ListSessions == nil {
		m.sidebar.sessions = nil
		return
	}
	m.sidebar.sessions = m.opts.ListSessions()
}

// moveSidebar 环绕移动侧栏选中游标（movePicker 同公式——补全菜单/选择器同
// 环绕口径）。
func (m *tuiModel) moveSidebar(delta int) {
	n := len(m.sidebar.sessions)
	if n == 0 {
		return
	}
	m.sidebar.sel = ((m.sidebar.sel+delta)%n + n) % n
}

// handleSidebarEnter 聚焦侧栏 Enter：换绑选中项（与 /resume <engine-id> 同一
// 接缝 swapSession——异步，swapping 忙门禁随置位接管）。fail-closed 守卫镜像
// picker（resume_picker.go：越界防御 + 空 EngineID → appendLine 留痕拒绝，
// 不调 swap——空链不得静默退化为 /new 语义）；拒绝保焦点保游标（用户可改选）。
// 成功触发即游标复位 + 焦点回主列（触发时刻语义；换绑回执 swapDoneMsg 另行
// 收口重绑与清单刷新）。
func (m *tuiModel) handleSidebarEnter() (tea.Model, tea.Cmd) {
	if m.sidebar.sel >= len(m.sidebar.sessions) || m.sidebar.sel < 0 {
		return m, nil
	}
	engineID := m.sidebar.sessions[m.sidebar.sel].EngineID
	if engineID == "" {
		m.appendLine("[侧栏] 该会话没有引擎 session id，无法续链")
		return m, nil
	}
	m.sidebar.sel = 0
	m.focus = focusMain
	return m, m.swapSession(engineID)
}

// toggleSidebar F3 开合（brief）：W<100 窄档不进入开态并经 appendLine 留痕
// （档位判定用 W 直接比较——Task 9 契约：columnWidths 的 sidebar==0 亦可能
// 来自 open=false，不是窄档判据；提示走 appendLine 非 appendNotice）。开合
// 生效时：重排前显式捕获阅读锚点（评审 Issue 4——同宽开合不走 WindowSizeMsg，
// 见 panel.go capturePendingAnchor），composer 跟 mainWidth 重排（同宽开合
// 不走 WindowSizeMsg——model.go Update 注记的开合同步）；开启沿刷新清单快照
// （Task 13——打开时刻口径）。
func (m *tuiModel) toggleSidebar() tea.Cmd {
	if m.width < 100 {
		m.appendLine("[侧栏] 侧栏需 ≥100 列")
		return nil
	}
	m.capturePendingAnchor()
	opening := !m.sidebarOpen
	m.sidebarOpen = !m.sidebarOpen
	if opening {
		m.refreshSidebarSessions()
	}
	m.composerState.ta.SetWidth(m.mainWidth())
	return nil
}

// cycleFocus F6 聚焦轮换（Task 13 最小焦点形）：主列↔侧栏两态循环。侧栏不可
// 见即不可聚焦——窄档 <100（几何退化不渲染）与未开启（F3 关态）都留痕拒绝
// （与 F2/F3/F4 守卫同口径：静默会让人以为已聚焦）；已聚焦侧栏则回主列。
// focusPanel 与聚焦可见性归 Task 17。
func (m *tuiModel) cycleFocus() tea.Cmd {
	if m.focus == focusSidebar {
		m.focus = focusMain
		return nil
	}
	if m.width < 100 {
		m.appendLine("[侧栏] 侧栏需 ≥100 列")
		return nil
	}
	if !m.sidebarOpen {
		m.appendLine("[侧栏] 侧栏未开，F3 开启后可 F6 聚焦")
		return nil
	}
	m.focus = focusSidebar
	return nil
}

// sidebarView 侧栏列视图（宽 w = columnWidths 的 sidebar 档，16）：品牌行 →
// 「会话」目 →（空清单：指引文案）→ 取景窗内清单行 → 底部提示。行内容 =
// sel 指示（sel 行「› 」、其余两空格）+ shortID(ID) + 工作区（数据源同谓词
// ——新在前）；当前会话（selfID 匹配 SessionSummary.ID）整行 surface2 高亮 +
// fg-primary（先截断再 padToWidth——底色覆盖整列宽），sel 行 accent（非
// selfID 行），其余 fg-secondary；超宽行 clampWidth 省略号截断。窗 = 行预算
// m.height−chrome（listWindow，sel 恒在窗内）。
func (m *tuiModel) sidebarView(width int) string {
	var b strings.Builder
	b.WriteString(activeTheme.accent(symBrand+" mulix") + "\n")
	b.WriteString(activeTheme.dim("会话") + "\n")
	n := len(m.sidebar.sessions)
	if n == 0 {
		// 空态行动指引（spec User Story 4：给文案而非空白；/new 提示恒在底行）。
		b.WriteString(activeTheme.dim("暂无历史会话") + "\n")
	} else {
		avail := m.height - sidebarChromeRows
		lo, hi := listWindow(n, avail, m.sidebar.sel)
		for i := lo; i < hi; i++ {
			it := m.sidebar.sessions[i]
			marker := "  "
			if i == m.sidebar.sel {
				marker = sidebarSelMark
			}
			row := marker + shortID(it.ID)
			if it.Workspace != "" {
				row += " " + it.Workspace
			}
			row = clampWidth(row, width)
			switch {
			case it.ID == m.selfID:
				b.WriteString(sidebarSelWrap(padToWidth(row, width)) + "\n")
			case i == m.sidebar.sel:
				b.WriteString(sgrWrap(sgrFg(activeTheme.accentColor), row) + "\n")
			default:
				b.WriteString(sgrWrap(sgrFg(activeTheme.fgSecondaryColor), row) + "\n")
			}
		}
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
