package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"mulix/internal/event"
	"mulix/internal/subagent"
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
// 不可用；Task 11 的平铺 panelOpen 已收编此字段，单源无双源）；tab 当前页签；
// sel 变更页签选中游标（Task 14）；agentsSel agents 页签选中游标（Task 15
// ——旧模态 agentsSel 收编：上界止步、下界渲染/取消钳位，旧语义）；previewPath
// diff 预览路径与 scroll 预览滚动（Task 14——取窗钳位见 changesTabView，按键
// 驱动归 Task 17 焦点全模型）。聚焦判定走 focusZone（m.focus==focusPanel，
// Task 14 最小焦点形——无面板侧 focus 位，单源无双源）。
type panelState struct {
	open        bool
	tab         panelTab
	sel         int
	agentsSel   int
	previewPath string
	scroll      int
}

// agentsTabInteractive agents 页签交互态（Task 15 收编口径——brief 钉定谓词，
// 门禁单源共用：handleKey 独占分支、Ctrl+C 逃生口、composerCursor、statusMode、
// modalOpen（滚轮/手势/点选拖选）与 handlePaste——一谓词不两写）。范围与旧
// /agents 模态一致：交互期 composer 冻结（键/粘贴不达编辑器、光标隐藏）、
// 转写手势冻结。无宽度项：进入交互的 /agents 路径设 W<130 守卫（进不去），
// 收窄后的残焦沿用旧模态全宽冻结语义（可见性对账归 Task 17）。
func (m *tuiModel) agentsTabInteractive() bool {
	return m.panel.open && m.focus == focusPanel && m.panel.tab == tabAgents
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
	if !m.panel.open && m.focus == focusPanel {
		// 关沿焦点失效重置（Task 14；侧栏 toggleSidebar 同款——评审
		// Important-1 口径）：不可见面板不得持焦。
		m.focus = focusMain
	}
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
// border 色分隔线 → 当前页签内容区（变更页签 = 聚合条目视图——Task 14；agents
// 页签 = 旧模态列表收编——Task 15；doctor/cache 空态指引，Task 16 逐签换内容）。
// 页签行四签并排（22 显示列 ≤ 32）；内容行 clampWidth 收口列宽——超宽行不得
// 撑破面板列。
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
	switch m.panel.tab {
	case tabChanges:
		b.WriteString(m.changesTabView(width))
	case tabAgents:
		b.WriteString(m.agentsTabView(width))
	default:
		b.WriteString(panelEmptyState(m.panel.tab, width))
	}
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
// 均 ≤ 面板列 32，clampWidth 兜底。变更/agents 页签自带内容视图（Task 14/15
// 各自的空态分支），不走本表；doctor/cache Task 16 接线后替换。
func panelEmptyState(tab panelTab, width int) string {
	var lines []string
	switch tab {
	case tabChanges:
		lines = []string{"暂无变更文件", "写类工具变更后在此汇总"}
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

// ---- 变更聚合 + 变更页签（2026-10-17-dsh-tui-visual-redesign Task 14）----
//
// 聚合缓存制（评审 Issue 3 修正）：工具卡收口点（ToolResultEvent 关卡）对写类
// done 卡按 path upsert 累计 +/−——缓存读口渲染，无帧时扫描；计数口径与
// renderDiff 同源（diffHunks+countDiffLines，不出第二计数器）。换绑收口清空
// 聚合（model.go swapDoneMsg 成功路径——面板聚合态归零，转写块流不动是 023
// 钉定语义）。

// panelChromeRows 面板列固定 chrome 行数（页签行 + 分隔线——内容区行预算的
// 扣除项；列高 = m.height，panelColumn）。
const panelChromeRows = 2

// changeEntry 变更聚合条目（Task 14）：path 聚合键（按 path 去重）、added/
// deleted 累计计数（renderDiff 同源）、card 回指最近一次写类卡（预览渲染源
// ——renderDiff(*card.diff) 与工具卡 diff 视图同源）。
type changeEntry struct {
	path    string
	added   int
	deleted int
	card    *toolCard
}

// noteChangeAggregate 变更聚合缓存维护（Task 14——工具卡收口点增量，调用点
// model.go ToolResultEvent 分支、closeToolCard 之前）：写类卡收口沿按 path
// upsert 累计。写类判据 = diff 位（openToolCard 只对 diffFromInput 命中的
// 写类工具挂 diff——Edit/Write/MultiEdit/NotebookEdit 同表覆盖，brief 三例
// 枚举为其子集）；计数经 diffHunks+countDiffLines（renderDiff 同源）。收口沿
// 判定 = pre-state 运行中（closeToolCard 原地改写前卡态仍为 running）：重复
// 回执（已 done）不双计、失败回执（IsError）不聚合、孤儿回执（请求缺席无卡
// 可考）不聚合；索引失配（换绑残渍）按 toolUseID 对账拦下。
func (m *tuiModel) noteChangeAggregate(e event.ToolResultEvent) {
	if e.IsError {
		return
	}
	idx, ok := m.cards[e.ToolUseID]
	if !ok {
		return
	}
	card, isCard := m.transcriptState.blocks[idx].(*toolCard)
	if !isCard || card.toolUseID != e.ToolUseID || card.state != toolRunning ||
		card.diff == nil || card.diff.path == "" {
		return
	}
	added, deleted := 0, 0
	if hunks, okH := diffHunks(*card.diff); okH {
		added, deleted = countDiffLines(hunks)
	}
	for i := range m.changes {
		if m.changes[i].path == card.diff.path {
			m.changes[i].added += added
			m.changes[i].deleted += deleted
			m.changes[i].card = card
			return
		}
	}
	m.changes = append(m.changes, changeEntry{path: card.diff.path, added: added, deleted: deleted, card: card})
}

// collectChanges 变更聚合读口（Task 14 Produces）：读缓存非现算——面板渲染
// 与键路由共用，不做帧时扫描。
func (m *tuiModel) collectChanges() []changeEntry {
	return m.changes
}

// changesTabView 变更页签内容（Task 14）：条目行（changeEntryRow）按插入序
// 排布，展开条目（previewPath 命中）行下内联 renderDiff 预览——preview 取窗
// 由 panelState.scroll 起始、余高收口钳位（窗尾不出预览、预览不出面板列；
// scroll 按键驱动归 Task 17 焦点全模型，本任务只落取窗机制）。内容区行预算 =
// m.height−panelChromeRows，超预算截断（panelColumn 同口径的显式收口——大清
// 单取景窗与侧栏 Task 13 同演进路径，后续任务接管）。空表回空态指引（不造数
// ——spec Edge Cases）。
func (m *tuiModel) changesTabView(width int) string {
	entries := m.collectChanges()
	if len(entries) == 0 {
		return panelEmptyState(tabChanges, width)
	}
	budget := max(m.height-panelChromeRows, 1)
	var rows []string
	for i, e := range entries {
		if budget <= 0 {
			break
		}
		rows = append(rows, changeEntryRow(width, e, i == m.panel.sel))
		budget--
		if e.path != m.panel.previewPath || e.card == nil || e.card.diff == nil {
			continue
		}
		preview := renderDiff(*e.card.diff, width, activeTheme)
		if take := min(budget, len(preview)); take > 0 {
			// scroll 取窗：上界钳位使窗尾不出预览（窗恒 ⊆ 面板列高内）。
			sc := min(m.panel.scroll, len(preview)-take)
			if sc < 0 {
				sc = 0
			}
			rows = append(rows, preview[sc:sc+take]...)
			budget -= take
		}
	}
	return strings.Join(rows, "\n")
}

// changeEntryRow 变更条目行（Task 14）：path + ` +n`（green 槽）` -m`（red
// 槽）——计数零侧省略（diffHead 同口径）；path 按计数余宽截断。selected 行
// 整行 surface2 高亮 + fg-primary（sidebarSelWrap 同槽）：内嵌计数段的 reset
// 会截断底色——diffRow「重置点即重铺」同法逐段重开 surface SGR，pad 在底色
// 区内补齐列宽后统一收 reset；无色层（NO_COLOR/中性缺省）退化为 padToWidth
// 平文——结构保留仅去色（sidebarSelWrap 同契约）。
func changeEntryRow(width int, e changeEntry, selected bool) string {
	var segs []string // 计数色段（内嵌 SGR——selected 行重铺 surface 用）
	if e.added > 0 {
		segs = append(segs, " "+activeTheme.green("+"+strconv.Itoa(e.added)))
	}
	if e.deleted > 0 {
		segs = append(segs, " "+activeTheme.red("-"+strconv.Itoa(e.deleted)))
	}
	stat := strings.Join(segs, "")
	path := clampWidth(e.path, max(width-ansi.StringWidth(stat), 1))
	row := path + stat
	if !selected {
		return row
	}
	params := sidebarSelParams()
	if params == "" {
		return padToWidth(row, width)
	}
	open := "\x1b[" + params + "m"
	var b strings.Builder
	b.WriteString(open)
	b.WriteString(path)
	for _, s := range segs {
		b.WriteString(s)
		b.WriteString(open) // 重置点即重铺（diffRow 同法）
	}
	b.WriteString(padToWidth("", width-ansi.StringWidth(row)))
	b.WriteString("\x1b[0m")
	return b.String()
}

// movePanelSel 变更条目游标环绕移动（Task 14——moveSidebar 同公式；空表
// no-op）。
func (m *tuiModel) movePanelSel(delta int) {
	n := len(m.collectChanges())
	if n == 0 {
		return
	}
	m.panel.sel = ((m.panel.sel+delta)%n + n) % n
}

// togglePanelPreview Enter 预览开合（Task 14）：选中条目展开 → previewPath =
// 条目 path、scroll 归零（新预览从头读）；展开态同条目再按收起；切换他条目
// 直接换预览。无条目/游标越界 no-op（brief：目标外不动作）。
func (m *tuiModel) togglePanelPreview() {
	entries := m.collectChanges()
	if len(entries) == 0 || m.panel.sel < 0 || m.panel.sel >= len(entries) {
		return
	}
	path := entries[m.panel.sel].path
	if m.panel.previewPath == path {
		m.panel.previewPath = ""
	} else {
		m.panel.previewPath = path
	}
	m.panel.scroll = 0
}

// ---- agents 页签（2026-10-17-dsh-tui-visual-redesign Task 15）----
//
// 旧 /agents 模态（M12/T12.4 bottomView 卡）收编进面板页签：列表（id | 状态 |
// 任务 | 深度）与 ↑/↓ 选择、c 取消语义平移（键路由见 model.go handleAgentsKey
// ——独占分支，收编口径 agentsTabInteractive）；「agentsOpen」独占模态字段退役
// （bottomView 卡随之移除——面板即列）。取消治理路径 agentsCancelCmd 不变。

// agentsTabHint agents 页签键位提示（旧模态文案平移；esc 由「关闭」改「返回」
// ——页签模型下 Esc 退出交互不关面板，关闭归 /agents toggle / F2）。
const agentsTabHint = "[↑/↓] 选择  [c] 取消  [esc] 返回"

// agentsTabView agents 页签内容（Task 15——旧 agentsPanelView 平移）：表头
// （ID | 状态 | 任务 | 深度——列格式保持，列名由表头承载、行载值：旧 46 显示
// 列行在 32 列钳位会劈掉深度列）+ 列表行 + 键位提示。数据源 = mgr.List() 实时
// 快照；游标渲染钳位与旧模态一致（↑ 止于 0、↓ 无界由渲染/取消钳位拉回）。
// 行预算 = m.height−panelChromeRows（表头/提示行恒保留，超出截断——面板列
// panelColumn 的高度收口再兜一道）。子代理未接线（ctrl/工厂缺席——/agents 开
// 启路径已拦，此处为换绑后防御与直渲口径）给「子代理未启用」空态。
func (m *tuiModel) agentsTabView(width int) string {
	var mgr *subagent.Manager
	if m.ctrl != nil {
		mgr = m.ctrl.Subagent()
	}
	if mgr == nil {
		return activeTheme.dim(clampWidth("子代理未启用", width)) + "\n" +
			activeTheme.dim(clampWidth("[esc] 返回", width))
	}
	infos := mgr.List()
	if len(infos) == 0 {
		return activeTheme.dim(clampWidth("暂无子代理", width)) + "\n" +
			activeTheme.dim(clampWidth(agentsTabHint, width))
	}
	if m.panel.agentsSel >= len(infos) {
		m.panel.agentsSel = len(infos) - 1
	}
	if m.panel.agentsSel < 0 {
		m.panel.agentsSel = 0
	}
	budget := max(m.height-panelChromeRows, 1)
	var b strings.Builder
	b.WriteString(activeTheme.dim(clampWidth("ID | 状态 | 任务 | 深度", width)) + "\n")
	budget--
	for i, in := range infos {
		if budget <= 1 { // 1 = 键位提示行预算（提示行恒可见）
			break
		}
		b.WriteString(m.agentsTabRow(width, in, i == m.panel.agentsSel) + "\n")
		budget--
	}
	b.WriteString(activeTheme.dim(clampWidth(agentsTabHint, width)))
	return b.String()
}

// agentsTabRow agents 列表行（Task 15）：id | 状态 | 任务 | 深度。selected
// 行 surface2 整行高亮 = sidebarSelWrap 同槽（pad 先行铺满列宽——Task 14
// changeEntryRow 同法；旧 ▸ 标记退役，sel 凭据 = 整行底色，无色层退化平文）。
// 任务列取窗钳位：行宽 = 前缀 + 任务(钳) + 尾列，深度列恒可见；任务为空给
// 「—」占位（列结构不塌）；行尾 clampWidth 兜底（深度位数异常等）。
func (m *tuiModel) agentsTabRow(width int, in subagent.Info, selected bool) string {
	prefix := shortID(in.SubSessionID) + " | " + string(in.Status) + " | "
	tail := " | " + strconv.Itoa(in.Depth)
	task := in.Task
	if task == "" {
		task = "—"
	}
	budget := width - ansi.StringWidth(prefix) - ansi.StringWidth(tail)
	row := clampWidth(prefix+clampWidth(task, budget)+tail, width)
	if !selected {
		return row
	}
	return sidebarSelWrap(padToWidth(row, width))
}
