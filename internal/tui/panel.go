package tui

import (
	"strings"

	"charm.land/bubbletea/v2"
)

// ---- 右栏面板（2026-10-17-dsh-tui-visual-redesign Task 12；设计 §6 面板页签）----
//
// 右栏信息面板骨架：页签行（active accent + SGR 4 下划线、其余 dim）→ border
// 色分隔线 → 当前页签内容区。本任务各页签为空态占位（spec FR-007：无真实数据
// 源不进面板——内容随 Task 14 变更聚合 / Task 15 agents 收编 / Task 16
// doctor+cache 双呈现逐页签落位；spec Edge Cases：空态给指引文案不渲染空白框）。
// 列合成（panelColumn）与 F2/F4 开合守卫（togglePanel/cyclePanelTab）同址。

// panelTab 面板页签（Task 12 骨架——内容区 Task 14–16 逐签落位）。
type panelTab uint8

const (
	tabChanges panelTab = iota // 变更文件汇总（Task 14）
	tabAgents                  // agents 收编（Task 15）
	tabDoctor                  // doctor 双呈现（Task 16）
	tabCache                   // cache 双呈现（Task 16）
	// panelTabCount 页签总数（cyclePanelTab 环绕取模）。
	panelTabCount
)

// panelTabLabels 页签显示名（下标即 panelTab 值；32 列面板列内四签并排——
// 短名收纳；显示面钉点归 Task 18 矩阵）。
var panelTabLabels = []string{"变更", "代理", "诊断", "缓存"}

// panelState 面板状态：open 开合（**newTuiModel 显式置 true**——Go 零值 false
// 不可用；Task 11 的平铺 panelOpen 已收编此字段，单源无双源）；focus 聚焦
// （Task 15/17 接线）；tab 当前页签；sel 页签内选中游标（Task 14/15）；
// previewPath diff 预览路径与 scroll 预览滚动（Task 14）。
type panelState struct {
	open        bool
	focus       bool
	tab         panelTab
	sel         int
	previewPath string
	scroll      int
}

// capturePendingAnchor 开合重排前的显式锚点捕获（评审 Issue 4）：同宽开合不
// 触发 WindowSizeMsg，pendingAnchor 只挂 resize 分支会漏——重排前
// anchorForOffset 记录视口顶行块位，frame 在新宽重排后按 offsetForAnchor 恢复
// 一次即清。尾随态不捕获（钉底接管，锚点作废——与 model.go WindowSizeMsg 分支
// 同判据）。侧栏（toggleSidebar）/面板（togglePanel）两条开合路径共用。
func (m *tuiModel) capturePendingAnchor() {
	if m.shouldFollowTail() {
		return
	}
	a := m.transcriptState.anchorForOffset(m.transcriptState.vp.YOffset())
	m.pendingAnchor = &a
}

// togglePanel F2 开合（brief）：W<130 窄/中档不进入开态并经 appendLine 留痕
// 「面板需 ≥130 列」（档位判定用 W 直接比较——Task 9 契约：columnWidths 的
// panel==0 亦可能来自 open=false，不是窄/中档判据；提示走 appendLine 非
// appendNotice，与 sidebar 守卫同口径）。开合生效时：重排前显式捕获阅读锚点
// （评审 Issue 4——同宽开合不走 WindowSizeMsg，见 capturePendingAnchor），
// composer 跟 mainWidth 重排（触发编辑器高度重算——Task 11 开合同步同款）。
func (m *tuiModel) togglePanel() tea.Cmd {
	if m.width < 130 {
		m.appendLine("[面板] 面板需 ≥130 列")
		return nil
	}
	m.capturePendingAnchor()
	m.panel.open = !m.panel.open
	m.composerState.ta.SetWidth(m.mainWidth())
	return nil
}

// cyclePanelTab F4 页签轮换（brief）：W<130 留痕（同 togglePanel 守卫——页签
// 行不可见时轮换无意义，静默会让人以为已切换）；否则 tab 环绕
// tabChanges→tabAgents→tabDoctor→tabCache→tabChanges。页签轮换不改列几何，
// 无需锚点/重排。
func (m *tuiModel) cyclePanelTab() {
	if m.width < 130 {
		m.appendLine("[面板] 面板需 ≥130 列")
		return
	}
	m.panel.tab = (m.panel.tab + 1) % panelTabCount
}

// panelView 面板列视图（宽 w = columnWidths 的 panel 档，32）：页签行 →
// border 色分隔线 → 当前页签空态指引（Task 14–16 换内容）。页签行四签并排
// （22 显示列 ≤ 32）；指引行 clampWidth 收口列宽——超宽行不得撑破面板列。
func (m *tuiModel) panelView(width int) string {
	var b strings.Builder
	// 页签行：active 段 = accent fg + SGR 4 下划线合成单条 SGR（panelTabActive
	// ——空 accent 槽退化为纯下划线，NO_COLOR 层结构保留仅去色）；其余 dim。
	for i, label := range panelTabLabels {
		if i > 0 {
			b.WriteString("  ")
		}
		if panelTab(i) == m.panel.tab {
			b.WriteString(panelTabActive(label))
			continue
		}
		b.WriteString(activeTheme.dim(label))
	}
	b.WriteString("\n")
	b.WriteString(sgrWrap(sgrFg(activeTheme.borderColor), strings.Repeat("─", width)))
	b.WriteString("\n")
	b.WriteString(panelEmptyState(m.panel.tab, width))
	return b.String()
}

// panelTabActive active 页签槽合成：accent fg + SGR 4 下划线（单条 SGR——
// 参数子串断言对合成形态鲁棒）；空 accent 槽（NO_COLOR/中性缺省）退化纯
// 下划线——面板结构保留仅去色（spec FR：无色层不因去色丢信息）。
func panelTabActive(s string) string {
	if fg := sgrFg(activeTheme.accentColor); fg != "" {
		return sgrWrap(fg+";4", s)
	}
	return sgrWrap("4", s)
}

// panelEmptyState 页签空态指引（spec Edge Cases：空态给文案不渲染空白框）：
// 首行状态、次行行动指引（doctor/cache 指引含命令字面——「/doctor 命令运行」
// 口径）。两行显式拆分不走自动换行——CJK 换行点不可预测会劈开断言字面；行宽
// 均 ≤ 面板列 32，clampWidth 兜底。各页签内容 Task 14–16 接线后替换。
func panelEmptyState(tab panelTab, width int) string {
	var lines []string
	switch tab {
	case tabChanges:
		lines = []string{"暂无变更文件", "写类工具变更后在此汇总"}
	case tabAgents:
		lines = []string{"暂无子代理", "委派启动后在此呈现"}
	case tabDoctor:
		lines = []string{"暂无诊断结果", "/doctor 命令运行检查"}
	case tabCache:
		lines = []string{"暂无用量数据", "/cache 查看缓存汇总"}
	default:
		return ""
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, activeTheme.dim(clampWidth(l, width)))
	}
	return strings.Join(out, "\n")
}

// panelColumn 面板列合成（frame 消费，Task 12——取代 Task 10 的空白占位）：
// panelView 逐行右补空格收口列宽、不足帧高补空白行——与 sidebarColumn 同契约
// （列高 = m.height、列宽 = panel 档），保证 JoinHorizontal 列位与帧宽
// （列和 = W）不变。w≤0 防御性返回空串（调用方已按档位让位过滤）。
func (m *tuiModel) panelColumn(w int) string {
	if w <= 0 {
		return ""
	}
	lines := strings.Split(m.panelView(w), "\n")
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
