package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"mulix/internal/cache"
	"mulix/internal/event"
)

// ---- 右栏面板骨架与 F2/F4 守卫（2026-10-17-dsh-tui-visual-redesign Task 12）----
//
// 钉点用 deepseek dark 出厂 256 层字面（TestThemePresets 同源）：accent 111、
// dim 244、border 238。本任务各页签内容区为空态占位（Task 13–16 落内容）。

// TestPanelViewTabRow 钉（brief Step 1）：页签行 active 页签 accent + SGR 4
// 下划线（单条合成 SGR）、其余页签 dim 槽；分隔线 border 槽满列宽；变更页签
// 空态指引在场。整段断言（SGR 包裹到 reset 的完整段）保证判别力——错槽/漏
// 下划线/全 accent 实现必红。
func TestPanelViewTabRow(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m.panel.tab = tabChanges
	v := m.panelView(32)

	// active：accent(38;5;111) + 下划线(4) 合成单条 SGR；inactive：dim(38;5;244)
	// 整段（无 accent、无下划线参数）。
	if !strings.Contains(v, "\x1b[38;5;111;4m变更\x1b[0m") {
		t.Fatalf("active tab must take the accent+underline slot:\n%q", v)
	}
	for _, label := range []string{"代理", "诊断", "缓存"} {
		seg := "\x1b[38;5;244m" + label + "\x1b[0m"
		if !strings.Contains(v, seg) {
			t.Fatalf("inactive tab %s must be a plain dim segment (no accent/underline):\n%q", label, v)
		}
	}
	// 分隔线：border 槽（238）满列宽 32。
	sep := "\x1b[38;5;238m" + strings.Repeat("─", 32) + "\x1b[0m"
	if !strings.Contains(v, sep) {
		t.Fatalf("panel view must render a border-slot separator spanning the column width:\n%q", v)
	}
	if !strings.Contains(v, "暂无变更文件") {
		t.Fatalf("changes tab must show its empty-state guidance:\n%q", v)
	}
}

// TestPanelViewEmptyStates 钉（brief Interfaces：各 tab 空态指引——Task 13–16
// 落内容前占位；spec Edge Cases：空态给文案不渲染空白框）：逐页签切换 active
// 段随动、指引文案在场（doctor 页签含「/doctor 命令运行」指引字面）。agents
// 页签自 Task 15 起为内容视图——未接线子代理的直渲口径给「子代理未启用」空态
// （接线空表态「暂无子代理」由 agents_test TestTUIAgentsTabEmpty 钉）。
func TestPanelViewEmptyStates(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	for _, tt := range []struct {
		tab       panelTab
		activeSeg string
		want      []string
	}{
		{tabChanges, "\x1b[38;5;111;4m变更\x1b[0m", []string{"暂无变更文件"}},
		{tabAgents, "\x1b[38;5;111;4m代理\x1b[0m", []string{"子代理未启用"}},
		{tabDoctor, "\x1b[38;5;111;4m诊断\x1b[0m", []string{"/doctor 命令运行"}},
		{tabCache, "\x1b[38;5;111;4m缓存\x1b[0m", []string{"/cache"}},
	} {
		m.panel.tab = tt.tab
		v := m.panelView(32)
		if !strings.Contains(v, tt.activeSeg) {
			t.Fatalf("tab %d must render its label as the active segment:\n%q", tt.tab, v)
		}
		for _, w := range tt.want {
			if !strings.Contains(v, w) {
				t.Fatalf("tab %d empty state must contain %q:\n%q", tt.tab, w, v)
			}
		}
	}
}

// TestTogglePanelNarrowGuard 钉（brief Step 1）：W=120（中档）F2 不进入开态并
// 经 appendLine 留痕「面板需 ≥130 列」（档位判定用 W 直接比较——Task 9 契约，
// 同 sidebar 守卫口径）；F4 同守卫留痕且页签不轮换。窄/中档几何不随守卫位移。
func TestTogglePanelNarrowGuard(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m.panel.open = false // 从关态按 F2——守卫必须拦下开态
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.panel.open {
		t.Fatal("F2 at W=120 must not open the panel")
	}
	if !hasTraceLine(m, "面板需 ≥130 列") {
		t.Fatalf("narrow F2 must leave the guard trace line in the transcript")
	}
	if got := m.mainWidth(); got != 104 {
		t.Fatalf("narrow mainWidth = %d, want 104（中档 120−16：侧栏让位、面板恒 0，开合无关）", got)
	}
	// F4 同守卫：留痕且页签不轮换。
	before := m.panel.tab
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF4})
	if m.panel.tab != before {
		t.Fatalf("F4 at W=120 must not cycle the tab")
	}
	if !hasTraceLine(m, "面板需 ≥130 列") {
		t.Fatalf("narrow F4 must leave the guard trace line in the transcript")
	}
}

// TestTogglePanelWide 钉（brief）：W=130 开态默认（设计裁定：默认三栏常开）→
// F2 关：main 让位收宽（130−16=114，仅面板让位）、面板页签行离场、composer 跟
// mainWidth 重排（同宽开合不走 WindowSizeMsg——开合同步，Task 11 sidebar 同
// 款）；再按 F2 开回：main 复原 130−16−32=82、页签行回场。
func TestTogglePanelWide(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	if !m.panel.open {
		t.Fatal("panel.open must default true（默认三栏常开）")
	}
	if got := m.mainWidth(); got != 82 {
		t.Fatalf("open mainWidth = %d, want 82（130−16−32）", got)
	}
	if !strings.Contains(m.frame(), "\x1b[38;5;111;4m变更") {
		t.Fatal("open panel must render the tab row into the frame")
	}

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.panel.open {
		t.Fatal("F2 must close the panel at W=130")
	}
	if got := m.mainWidth(); got != 114 {
		t.Fatalf("main width after close = %d, want 114（130−16，仅面板让位）", got)
	}
	if strings.Contains(m.frame(), "\x1b[38;5;111;4m变更") {
		t.Fatal("closed panel must leave the frame（面板列随开合消失）")
	}
	if got := m.composerState.ta.Width(); got != 114-composerPromptWidth {
		t.Fatalf("composer width after close = %d, want %d（开合同步重排）",
			got, 114-composerPromptWidth)
	}

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if !m.panel.open {
		t.Fatal("second F2 must reopen the panel")
	}
	if got := m.mainWidth(); got != 82 {
		t.Fatalf("main width after reopen = %d, want 82", got)
	}
	if !strings.Contains(m.frame(), "\x1b[38;5;111;4m变更") {
		t.Fatal("reopened panel must render the tab row again")
	}
}

// TestCyclePanelTab 钉（brief Step 1）：W=150 宽档 F4 页签环绕
// tabChanges→tabAgents→tabDoctor→tabCache→tabChanges；开合态不受轮换影响。
func TestCyclePanelTab(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 150, Height: 24})
	want := []panelTab{tabAgents, tabDoctor, tabCache, tabChanges}
	for i, wt := range want {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF4})
		if m.panel.tab != wt {
			t.Fatalf("F4 #%d: tab = %d, want %d（环绕顺序 changes→agents→doctor→cache）", i+1, m.panel.tab, wt)
		}
	}
	if !m.panel.open {
		t.Fatal("tab cycling must not toggle the panel open state")
	}
}

// TestAnchorSurvivesPanelToggle 钉（brief 评审 Issue 4 移入）：溢出转写上翻
// 中部、同宽（150→150）面板开合——同宽开合不走 WindowSizeMsg，开合路径必须
// 显式捕获 pendingAnchor，重排后视口锚回同块（±1 行）。块文本设计为「117 列
// 单行 / 101 列双行」，重排后绝对行号漂移：无显式捕获时顶行必落到更晚的块。
func TestAnchorSurvivesPanelToggle(t *testing.T) {
	m, _, _ := newTestModel(t)
	update(t, m, tea.WindowSizeMsg{Width: 150, Height: 24}) // main=102（全开让位）
	for i := range 40 {
		// 110 字符：101 列内容宽折 2 行、117 列（面板收合后）单行。
		m.appendLine(fmt.Sprintf("BLOCK%02d %s", i, strings.Repeat("x", 101)))
	}
	m.View() // 触发 frame：按当前列宽 sync + 喂给

	ts := &m.transcriptState
	m.markUserScrolled()
	ts.vp.SetYOffset(20) // 块 10 首行（101 列下每块 2 行）
	anchorBlock := strings.Fields(ts.wrappedLines[ts.vp.YOffset()])[0]
	if anchorBlock != "BLOCK10" {
		t.Fatalf("前置：顶行应为 BLOCK10, got %q", ts.wrappedLines[ts.vp.YOffset()])
	}

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2}) // 面板开合（150→150 同宽）
	m.View()                                           // 新宽全量重排 + 喂给 + 锚点恢复

	newTop := strings.Fields(ts.wrappedLines[ts.vp.YOffset()])[0]
	if newTop != anchorBlock {
		t.Fatalf("面板开合重排后视口顶行应仍对准锚定块 %s, got %q（wrappedLines[%d]）",
			anchorBlock, newTop, ts.vp.YOffset())
	}
}

// hasTraceLine 断言转写块流中存在含 s 的系统留痕行（appendLine plainBlock）。
func hasTraceLine(m *tuiModel, s string) bool {
	for _, b := range m.transcriptState.blocks {
		if pb, ok := b.(plainBlock); ok && strings.Contains(pb.text, s) {
			return true
		}
	}
	return false
}

// ---- 变更聚合 + 变更 tab（2026-10-17-dsh-tui-visual-redesign Task 14）----
//
// 聚合缓存制（评审 Issue 3 修正）：工具卡收口点（ToolResultEvent 关卡）对写类
// done 卡按 path upsert 累计 +/−（计数与 renderDiff 同源——diffHunks+
// countDiffLines，不出第二计数器）；换绑收口清空聚合缓存，转写块流不动（023
// 钉定语义——TestSwapResetsChangesPanel 钉点）。SGR 钉点用 deepseek dark 出厂
// 256 层字面（文件头注记同源）：green 114、red 203、sel 行 surface2 236 + fg 231。

// seedDoneEdit 桩收口路径（brief Step 1）：Edit 请求开卡 + 完成回执关卡——
// 聚合缓存的唯一写入沿（收口点增量维护，无帧时扫描）。
func seedDoneEdit(t *testing.T, m *tuiModel, seq int64, id, path, oldS, newS string) *tuiModel {
	t.Helper()
	raw, err := json.Marshal(struct {
		FilePath string `json:"file_path"`
		Old      string `json:"old_string"`
		New      string `json:"new_string"`
	}{path, oldS, newS})
	if err != nil {
		t.Fatalf("seed input marshal: %v", err)
	}
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: seq, ToolUseID: id, Tool: "Edit", Input: raw}})
	return update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: seq + 1, ToolUseID: id, Content: "ok"}})
}

// TestCollectChangesAggregation 钉（brief Step 1）：收口路径聚合——2 个不同
// path 的 done Edit 卡恰 2 条目且计数正确（单行替换 = +1 −1）；运行中卡（未
// 收口）与失败卡（IsError）不聚合。
func TestCollectChangesAggregation(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = seedDoneEdit(t, m, 1, "c1", "a.go", "x", "y")
	m = seedDoneEdit(t, m, 3, "c2", "b.go", "p", "q")
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 5, ToolUseID: "c3", Tool: "Edit",
		Input: json.RawMessage(`{"file_path":"c.go","old_string":"r","new_string":"s"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 7, ToolUseID: "c4", Tool: "Edit",
		Input: json.RawMessage(`{"file_path":"d.go","old_string":"t","new_string":"u"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: 8, ToolUseID: "c4", IsError: true, Content: "boom"}})

	entries := m.collectChanges()
	if len(entries) != 2 {
		t.Fatalf("collectChanges must hold exactly the 2 done cards, got %d: %+v", len(entries), entries)
	}
	if entries[0].path != "a.go" || entries[0].added != 1 || entries[0].deleted != 1 || entries[0].card == nil {
		t.Fatalf("a.go entry = %+v, want +1 -1 with card backref", entries[0])
	}
	if entries[1].path != "b.go" || entries[1].added != 1 || entries[1].deleted != 1 {
		t.Fatalf("b.go entry = %+v, want +1 -1", entries[1])
	}
}

// TestCollectChangesSamePathAccumulates 钉（brief Step 1）：同 path 两卡累计
// ——按 path 去重单条目，+/− 求和。
func TestCollectChangesSamePathAccumulates(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = seedDoneEdit(t, m, 1, "c1", "a.go", "x", "y")
	m = seedDoneEdit(t, m, 3, "c2", "a.go", "p", "q")
	entries := m.collectChanges()
	if len(entries) != 1 {
		t.Fatalf("same-path cards must dedupe into one entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].path != "a.go" || entries[0].added != 2 || entries[0].deleted != 2 {
		t.Fatalf("accumulated entry = %+v, want a.go +2 -2", entries[0])
	}
}

// TestChangesTabRows 钉（brief Step 1）：条目行 = path + `+n`（green 114 槽）
// `-m`（red 203 槽；零侧省略与 diffHead 同口径——b.go 双行替换 +2 −1）；sel
// 行 surface2（236）+ fg（231）整行高亮。
func TestChangesTabRows(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m = seedDoneEdit(t, m, 1, "c1", "a.go", "x", "y")
	m = seedDoneEdit(t, m, 3, "c2", "b.go", "p\nq", "p\nr\ns")
	v := m.panelView(32)
	if !strings.Contains(v, "a.go") || !strings.Contains(v, "b.go") {
		t.Fatalf("changes tab must render entry paths:\n%q", v)
	}
	for _, seg := range []string{"\x1b[38;5;114m+1\x1b[0m", "\x1b[38;5;114m+2\x1b[0m", "\x1b[38;5;203m-1\x1b[0m"} {
		if !strings.Contains(v, seg) {
			t.Fatalf("counts must take the green/red slots, missing %q:\n%q", seg, v)
		}
	}
	m.panel.sel = 1
	v = m.panelView(32)
	if !strings.Contains(v, "\x1b[38;5;231;48;5;236m") {
		t.Fatalf("selected row must take the surface2 highlight:\n%q", v)
	}
}

// TestPanelPreviewToggle 钉（brief Step 1）：聚焦面板 Enter 展开 renderDiff
// 预览（行内容与工具卡 diff 同源——同源即同一 diffInput 的 unified 视图）、
// 再按收起；非聚焦 Enter 不开预览（键路由聚焦门禁——主列 Enter 仍是提交档）。
func TestPanelPreviewToggle(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m = seedDoneEdit(t, m, 1, "c1", "a.go", "old line", "new line")
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.panel.previewPath != "a.go" {
		t.Fatalf("Enter must expand the selected entry, previewPath = %q", m.panel.previewPath)
	}
	if v := ansi.Strip(m.panelView(32)); !strings.Contains(v, "- old line") || !strings.Contains(v, "+ new line") {
		t.Fatalf("expanded entry must render the renderDiff-sourced preview:\n%s", v)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.panel.previewPath != "" {
		t.Fatalf("second Enter must collapse, previewPath = %q", m.panel.previewPath)
	}
	if strings.Contains(ansi.Strip(m.panelView(32)), "+ new line") {
		t.Fatal("collapsed entry must drop the preview")
	}

	// 非聚焦 Enter 不开预览（focus 路由门禁——主列 Enter 走提交档，空稿 no-op）。
	m2, _, _ := newTestModel(t)
	m2 = seedDoneEdit(t, m2, 1, "c1", "a.go", "x", "y")
	m2 = update(t, m2, tea.WindowSizeMsg{Width: 130, Height: 24})
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m2.panel.previewPath != "" {
		t.Fatalf("unfocused Enter must not expand, previewPath = %q", m2.panel.previewPath)
	}
}

// TestPanelPreviewScrollClamped 钉（brief：预览经 panelState.scroll 取窗、钳
// 位面板列高——scroll 推进窗首离场、窗尾不出预览）。
func TestPanelPreviewScrollClamped(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "c1", Tool: "Write",
		Input: json.RawMessage(`{"file_path":"big.txt","content":"l00\nl01\nl02\nl03\nl04\nl05\nl06\nl07\nl08\nl09\nl10\nl11"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: 2, ToolUseID: "c1", Content: "ok"}})
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 8}) // 内容预算 6 行（8−2 chrome）
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansi.Strip(m.panelView(32)), "l00") {
		t.Fatal("scroll 0 must show the head of the preview")
	}
	m.panel.scroll = 6 // 窗内推进（未达上界）：窗首行离场
	v := ansi.Strip(m.panelView(32))
	if strings.Contains(v, "l00") {
		t.Fatalf("advanced window must drop the head:\n%s", v)
	}
	if !strings.Contains(v, "l05") {
		t.Fatalf("advanced window must start at the scroll offset:\n%s", v)
	}
	m.panel.scroll = 99 // 超上界——钳位窗尾对齐（窗尾不出预览、不出面板列）
	v = ansi.Strip(m.panelView(32))
	if strings.Contains(v, "l00") || !strings.Contains(v, "l11") {
		t.Fatalf("over-range scroll must clamp to the tail:\n%s", v)
	}
}

// TestPanelPreviewScrollKeys 钉（终审 Finding 1）：面板聚焦 + 变更预览展开态
// PgDn/PgUp 推进/回退 panel.scroll——渲染窗口随键位移（步长 = 内容区行预算的
// 一半，panelScrollStep 裁定见 panel.go）；连按钳位上界（窗尾不出预览——
// changesTabView 既有取窗钳位复用）、下界 0（回退到窗首）。
func TestPanelPreviewScrollKeys(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "c1", Tool: "Write",
		Input: json.RawMessage(`{"file_path":"big.txt","content":"l00\nl01\nl02\nl03\nl04\nl05\nl06\nl07\nl08\nl09\nl10\nl11"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: 2, ToolUseID: "c1", Content: "ok"}})
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 8}) // 内容预算 6 行（8−2 chrome）
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // 展开预览
	if m.panel.previewPath != "big.txt" {
		t.Fatalf("precondition: preview expanded, got %q", m.panel.previewPath)
	}
	// PgDn 推进：步长 = (8−2)/2 = 3 → 窗首 l00/l01 离场（窗含预览头行 3 行
	// 位移——preview[0]=头行、preview[1..12]=l00..l11）、l03 进窗。
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.panel.scroll != 3 {
		t.Fatalf("preview PgDn must advance panel.scroll by the half-budget step (3), got %d", m.panel.scroll)
	}
	v := ansi.Strip(m.panelView(32))
	if strings.Contains(v, "l00") || strings.Contains(v, "l01") {
		t.Fatalf("advanced window must drop the head rows:\n%s", v)
	}
	if !strings.Contains(v, "l03") {
		t.Fatalf("advanced window must start at the scroll offset:\n%s", v)
	}
	// 钳位上界：连按 PgDn——窗尾对齐预览尾（l11 恒在场、l00 不回流）。
	for range 10 {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	v = ansi.Strip(m.panelView(32))
	if !strings.Contains(v, "l11") || strings.Contains(v, "l00") {
		t.Fatalf("over-range PgDn must clamp to the preview tail:\n%s", v)
	}
	// 回退：连按 PgUp——下界 0，窗首行回场。
	for range 20 {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	if m.panel.scroll != 0 {
		t.Fatalf("PgUp must floor panel.scroll at 0, got %d", m.panel.scroll)
	}
	if v := ansi.Strip(m.panelView(32)); !strings.Contains(v, "l00") {
		t.Fatalf("PgUp back to top must show the preview head:\n%s", v)
	}
}

// TestPanelDoctorCacheScrollKeys 钉（终审 Finding 1）：doctor/cache 页签聚焦态
// PgUp/PgDn 滚内容窗（panelLinesWindow 取窗——上界钳位到内容行数、下界 0）；
// 短内容（cache 汇总）钳位吸收不丢失。
func TestPanelDoctorCacheScrollKeys(t *testing.T) {
	m, _, _ := newTestModel(t)
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("d%02d", i)
	}
	m = update(t, m, doctorDoneMsg{lines: lines})
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 8}) // 内容预算 6 行
	m.panel.tab = tabDoctor
	m.focus = focusPanel
	// PgDn：步长 3 = (8−2)/2 → 窗首 d00..d02 离场、d03 进窗。
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.panel.scroll != 3 {
		t.Fatalf("doctor tab PgDn must advance panel.scroll, got %d", m.panel.scroll)
	}
	v := ansi.Strip(m.panelView(32))
	if strings.Contains(v, "d00") || strings.Contains(v, "d02") || !strings.Contains(v, "d03") {
		t.Fatalf("doctor window must shift with the scroll:\n%s", v)
	}
	// 钳位上界：连按到底（窗尾对齐内容尾 d09）。
	for range 10 {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	v = ansi.Strip(m.panelView(32))
	if !strings.Contains(v, "d09") || strings.Contains(v, "d00") {
		t.Fatalf("over-range PgDn must clamp to the content tail:\n%s", v)
	}
	// 回退到顶。
	for range 20 {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	if v := ansi.Strip(m.panelView(32)); !strings.Contains(v, "d00") {
		t.Fatalf("PgUp back to top must show the content head:\n%s", v)
	}
	// cache 页签：路由在场（状态推进）且空态指引照常呈现（钳位吸收不丢失）。
	m.panel.tab = tabCache
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.panel.scroll == 0 {
		t.Fatal("cache tab PgDn must route the scroll state")
	}
	if v := ansi.Strip(m.panelView(32)); !strings.Contains(v, "暂无用量数据") {
		t.Fatalf("cache tab copy must survive the clamp:\n%s", v)
	}
}

// TestPanelScrollUnfocusedNoop 钉（终审 Finding 1——现状只增不改）：非聚焦
// PgUp/PgDn 不触 panel.scroll（含预览展开残态），转写滚动照常（现状行为）。
func TestPanelScrollUnfocusedNoop(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "c1", Tool: "Edit",
		Input: json.RawMessage(`{"file_path":"a.go","old_string":"x","new_string":"y"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: 2, ToolUseID: "c1", Content: "ok"}})
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // 预览展开 + scroll 残态
	m.panel.scroll = 4
	m.focus = focusMain // 失焦（预览残态保留——Esc 取焦不关预览口径）
	// 转写溢出 → 非聚焦 PgUp 走转写滚动（现状）。
	for i := range 40 {
		m.appendLine(fmt.Sprintf("BLOCK%02d %s", i, strings.Repeat("x", 101)))
	}
	m.View() // frame：换行缓存 sync + viewport 喂给
	m.transcriptState.vp.SetYOffset(20)
	m.markFollowTail()
	before := m.transcriptState.vp.YOffset()
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.panel.scroll != 4 {
		t.Fatalf("unfocused PgUp must not touch panel.scroll, got %d", m.panel.scroll)
	}
	if got := m.transcriptState.vp.YOffset(); got >= before {
		t.Fatalf("unfocused PgUp must keep scrolling the transcript: %d → %d", before, got)
	}
	if m.scrollState.mode != scrollUserScrolled {
		t.Fatal("unfocused PgUp must keep the transcript gesture semantics (userScrolled)")
	}
}

// TestSwapResetsChangesPanel 钉（Review Focus 5——brief Produces 指定测试名）：
// 换绑回执清空聚合缓存与面板游标/预览；转写块流不动（023 钉定语义——聚合是
// 面板态非转写，仅追加切换留痕行）。
func TestSwapResetsChangesPanel(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = seedDoneEdit(t, m, 1, "c1", "a.go", "x", "y")
	m = seedDoneEdit(t, m, 3, "c2", "b.go", "p", "q")
	blocksBefore := len(m.transcriptState.blocks)
	if len(m.collectChanges()) != 2 {
		t.Fatal("precondition: two aggregated entries")
	}
	newCtrl := &stubController{}
	sw := &fakeSwapper{sink: m.sink, ctrl: newCtrl, id: "s-new"}
	m.swapper = sw
	m.panel.sel = 1
	m.panel.previewPath = "a.go"
	m.panel.scroll = 3
	m = update(t, m, typeStr(m, "/new"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.collectChanges()) != 0 {
		t.Fatalf("swap receipt must clear the changes cache, got %d", len(m.collectChanges()))
	}
	if len(m.transcriptState.blocks) != blocksBefore+1 {
		t.Fatalf("swap must not reset the transcript block stream（023 钉定）: %d → %d（仅切换留痕行 +1）",
			blocksBefore, len(m.transcriptState.blocks))
	}
	if _, ok := m.transcriptState.blocks[0].(*toolCard); !ok {
		t.Fatal("pre-swap tool cards must persist in the block stream")
	}
	if m.panel.sel != 0 || m.panel.previewPath != "" || m.panel.scroll != 0 {
		t.Fatalf("panel cursors must reset with the aggregate: %+v", m.panel)
	}
}

// TestChangesTabSelWindow 钉（终审 Finding 2）：变更条目取景窗——sel 居中
// （listWindow 同公式，侧栏 Task 13 同源）：条目超窗时 sel 行恒可见（现状按
// 预算 break 截断，sel 可落进渲染不出的条目）；页签行位置计数缀场（双案齐做
// 之二——「变更 3/7」式，侧栏「会话 3/7」同形态）；展开预览吃预算后 sel 行仍
// 可见（非 sel 预览截断不偷 sel 行预算）。
func TestChangesTabSelWindow(t *testing.T) {
	m, _, _ := newTestModel(t)
	// 7 条目：a.go 为 Write 大文件（预览 13 行——吃预算主力），b..g 为单行 Edit。
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "c0", Tool: "Write",
		Input: json.RawMessage(`{"file_path":"a.go","content":"l00\nl01\nl02\nl03\nl04\nl05\nl06\nl07\nl08\nl09\nl10\nl11"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: 2, ToolUseID: "c0", Content: "ok"}})
	for i, p := range []string{"b.go", "c.go", "d.go", "e.go", "f.go", "g.go"} {
		m = seedDoneEdit(t, m, int64(3+2*i), fmt.Sprintf("c%d", i+1), p, "x", "y")
	}
	// (1) 条目超窗（H=8 → 预算 6 < 7 条目）：sel 环绕到末条目——sel 行恒可见。
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 8})
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp}) // 0 → 6（环绕）
	if m.panel.sel != 6 {
		t.Fatalf("precondition: sel wrapped to the last entry, got %d", m.panel.sel)
	}
	if v := ansi.Strip(m.panelView(32)); !strings.Contains(v, "g.go") {
		t.Fatalf("sel row must stay visible when entries overflow the window:\n%s", v)
	}
	// (2) 位置计数：溢出时 active 页签名缀「变更 3/7」式计数（sel+1/总数）。
	m.panel.sel = 2
	if v := m.panelView(32); !strings.Contains(v, "变更 3/7") {
		t.Fatalf("overflowing tab row must carry the position counter:\n%q", v)
	}
	// (3) 展开预览吃预算后 sel 行仍可见：预览驻 a.go、sel 移至 d.go（预览随
	// sel 移动留守原条目——togglePanelPreview 语义）；a.go 预览截断让位。
	m.panel.sel = 0
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // 展开 a.go 预览
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})  // sel → 3（d.go）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.panel.previewPath != "a.go" || m.panel.sel != 3 {
		t.Fatalf("precondition: preview on a.go, sel on d.go: %q %d", m.panel.previewPath, m.panel.sel)
	}
	if v := ansi.Strip(m.panelView(32)); !strings.Contains(v, "d.go") {
		t.Fatalf("sel row must stay visible when an expanded preview eats the budget:\n%s", v)
	}
}

// TestPanelFocusKeys 钉（brief：↑/↓ 移 sel——聚焦面板且变更页签才路由；文本
// 键永不劫持照常进 composer；Esc 取焦回主列且预览态保留（取焦不关面板）；
// F2 关面板沿焦点失效重置；窄档 <130 残值焦点不路由）。
func TestPanelFocusKeys(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = seedDoneEdit(t, m, 1, "c1", "a.go", "x", "y")
	m = seedDoneEdit(t, m, 3, "c2", "b.go", "p", "q")
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.panel.sel != 1 {
		t.Fatalf("focused ↓ must move the changes cursor, sel = %d", m.panel.sel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.panel.sel != 0 {
		t.Fatalf("focused ↑ must move back, sel = %d", m.panel.sel)
	}
	m = update(t, m, keyPress('x'))
	if m.composerValue() != "x" {
		t.Fatalf("text keys must reach the composer while panel focused: %q", m.composerValue())
	}
	if m.panel.sel != 0 {
		t.Fatalf("text keys must not move the cursor: %d", m.panel.sel)
	}
	// Esc 取焦回主列；预览态保留（Task 17「Esc 取焦不关面板」同口径先行）。
	m.panel.previewPath = "a.go"
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.focus != focusMain {
		t.Fatal("idle Esc with panel focus must refocus main")
	}
	if m.panel.previewPath != "a.go" {
		t.Fatal("refocus Esc must not collapse the preview")
	}
	// F2 关面板沿焦点失效重置（侧栏关沿同款——不可见面板不得持焦）。
	m.focus = focusPanel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF2})
	if m.panel.open || m.focus != focusMain {
		t.Fatalf("closing the panel must reset focus, open=%v focus=%d", m.panel.open, m.focus)
	}
	// 窄档路由门禁（<130 面板不可见——路由前置守卫兜底残值焦点）。
	m.panel.open = true
	m.focus = focusPanel
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.panel.sel != 0 {
		t.Fatalf("narrow width with stale focus must not route panel keys: %d", m.panel.sel)
	}
}

// ---- doctor/cache 页签双呈现（2026-10-17-dsh-tui-visual-redesign Task 16）----
//
// 双呈现纪律（FR-012）：页签内容与转写行同源——doctor 腿同源 doctorDoneMsg
// .lines（转写 appendLine 照旧，本文件转写断言持矩阵面）；cache 腿经
// cacheSummaryLines 与 /cache 命令共用单一格式器（model.go——不出第二格式器，
// Task 14 计数同源纪律）。转写行为行不变（tui_test.go TestSlashCache 持有）。

// TestDoctorTabDualRender 钉（brief Step 1）：doctorDoneMsg 后转写行仍在
// （appendLine 照旧——矩阵面不动）+ doctor 页签渲染同 lines（无色层平文等值
// ——dim 槽去 SGR 即原文；断言行 ≤ 32 列不触钳位）；空态（未运行 /doctor）
// 指引字面在场（不造数）。
func TestDoctorTabDualRender(t *testing.T) {
	m, _, _ := newTestModel(t)
	lines := []string{"[doctor] 引擎 OK", "[doctor] 缓存 OK"}
	m = update(t, m, doctorDoneMsg{lines: lines})
	if !hasTraceLine(m, lines[0]) || !hasTraceLine(m, lines[1]) {
		t.Fatal("doctorDoneMsg must keep appending lines to the transcript（双呈现转写腿不变）")
	}
	m.panel.tab = tabDoctor
	v := ansi.Strip(m.panelView(32))
	for _, l := range lines {
		if !strings.Contains(v, l) {
			t.Fatalf("doctor tab must render the same-source lines, missing %q:\n%s", l, v)
		}
	}
	// 空态：doctorLines 未存储时指引字面在场。
	m2, _, _ := newTestModel(t)
	m2.panel.tab = tabDoctor
	if v := m2.panelView(32); !strings.Contains(v, "/doctor 命令运行") {
		t.Fatalf("empty doctor tab must keep the guidance copy:\n%q", v)
	}
}

// seedUsageTurn 播种一个记账回合（TestSlashCache 同款直连 cache.NewMeter——
// tui 不 import cli 装配路径）：Close 释放句柄（Windows）后 ReadUsageReport
// 方可读；turnOffset 错开多轮播种的行号（ReconcileUsage 同行号后行覆盖——不
// 错开会折成一回合）。
func seedUsageTurn(t *testing.T, root, id string, turnOffset int64) {
	t.Helper()
	meter, err := cache.NewMeter(cache.MeterOptions{
		Root: root, SessionID: id, TurnOffset: turnOffset,
		Pricing: cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15},
	})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	if _, err := meter.Observe(cache.TurnUsage{InputTokens: 100, CacheReadTokens: 300, OutputTokens: 20}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := meter.Close(); err != nil { // 落盘并释放 usage 文件（Windows 句柄）
		t.Fatalf("meter close: %v", err)
	}
}

// TestCacheTabSameSource 钉（brief Step 1）：缓存页签与 /cache 输出同源——
// 单一格式器产物（cacheSummaryLines）：播种 1 回合后 /cache 转写行在场（行为
// 不变）且页签同段（无色层对账——32 列钳位以「回合 N」「命中率」可容首段断
// 言，超宽行不进面板列）；再播种一回合页签随现态刷新（/cache 输出为动态现态
// ——页签逐帧同口径，非 /cache 时刻冻结快照）；无记账（零值 Report = 会话尚
// 无记账，合法空态）页签回指引文案（不造数——零壳「回合 0」不当数据渲染）。
func TestCacheTabSameSource(t *testing.T) {
	root := t.TempDir()
	id := "tui-cache-tab-01"
	seedUsageTurn(t, root, id, 0)
	sink := NewSink()
	m := newTuiModel(Options{UsageRoot: root, Sink: sink}, &stubController{}, sink.Events(), id)
	m.testMode = true
	// 转写腿：/cache 行为不变（TestSlashCache 同款分流路径）。
	m = update(t, m, typeStr(m, "/cache"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !hasTraceLine(m, "回合") || !hasTraceLine(m, "命中率") {
		t.Fatal("precondition: /cache must append the summary line to the transcript")
	}
	// 页签腿：同源格式器产物（含数据段——非空态壳）。
	m.panel.tab = tabCache
	v := ansi.Strip(m.panelView(32))
	if !strings.Contains(v, "回合 1") || !strings.Contains(v, "命中率") {
		t.Fatalf("cache tab must render the same-source summary:\n%s", v)
	}
	if strings.Contains(v, "暂无用量数据") {
		t.Fatalf("cache tab must not show the empty state when data exists:\n%s", v)
	}
	// 动态现态：再播种一回合，页签随当前缓存投影刷新（回合 1 → 回合 2）。
	seedUsageTurn(t, root, id, 1)
	v = ansi.Strip(m.panelView(32))
	if !strings.Contains(v, "回合 2") || strings.Contains(v, "回合 1 |") {
		t.Fatalf("cache tab must track the current cache state (dynamic, not frozen):\n%s", v)
	}
	// 空态：无记账合法态 → 指引文案（不造数）。
	m2, _, _ := newTestModel(t)
	m2.panel.tab = tabCache
	if v := m2.panelView(32); !strings.Contains(v, "暂无用量数据") || !strings.Contains(v, "/cache 查看缓存汇总") {
		t.Fatalf("empty cache tab must show guidance, not a zero-value shell:\n%q", v)
	}
}

// ---- 聚焦可见性（2026-10-17-dsh-tui-visual-redesign Task 17；设计 §8 Q8）----

// TestPanelFocusEdge 钉（Task 17 聚焦可见性）：聚焦面板 → 分隔线（聚焦区边框
// 线）切 accent 槽（38;5;111）；未聚焦 → border 槽（38;5;238——Task 12 现状
// 逐字节不变，TestPanelViewTabRow 承此）。
func TestPanelFocusEdge(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 150, Height: 24})
	m.panel.tab = tabChanges
	sep := "\x1b[38;5;238m" + strings.Repeat("─", 32) + "\x1b[0m"
	if !strings.Contains(m.panelView(32), sep) {
		t.Fatalf("unfocused panel must keep the border-slot separator:\n%q", m.panelView(32))
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6}) // → 侧栏
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6}) // → 面板
	if m.focus != focusPanel {
		t.Fatalf("precondition: panel focused, got %d", m.focus)
	}
	focused := "\x1b[38;5;111m" + strings.Repeat("─", 32) + "\x1b[0m"
	v := m.panelView(32)
	if !strings.Contains(v, focused) {
		t.Fatalf("focused panel separator must take the accent slot:\n%q", v)
	}
	if strings.Contains(v, sep) {
		t.Fatalf("unfocused separator must not linger while focused:\n%q", v)
	}
}

// TestChangeEntryRowZeroSideOmission 钉（minors 清理 T14 deferred）：计数零侧
// 省略——added>0 仅发 green 段、deleted>0 仅发 red 段，零侧不发 `+0`/`−0`
// （diffHead 同口径）；双侧齐发且槽色正确（green 114 / red 203，确定性 256 层）。
func TestChangeEntryRowZeroSideOmission(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	addOnly := changeEntryRow(40, changeEntry{path: "a.go", added: 2}, false)
	if !strings.Contains(addOnly, "\x1b[38;5;114m+2") {
		t.Fatalf("added side must carry the green slot:\n%q", addOnly)
	}
	if strings.Contains(addOnly, "-0") || strings.Contains(addOnly, "\x1b[38;5;203m-0") {
		t.Fatalf("zero deleted side must be omitted:\n%q", addOnly)
	}
	delOnly := changeEntryRow(40, changeEntry{path: "b.go", deleted: 3}, false)
	if !strings.Contains(delOnly, "\x1b[38;5;203m-3") {
		t.Fatalf("deleted side must carry the red slot:\n%q", delOnly)
	}
	if strings.Contains(delOnly, "+0") || strings.Contains(delOnly, "\x1b[38;5;114m+0") {
		t.Fatalf("zero added side must be omitted:\n%q", delOnly)
	}
	both := changeEntryRow(40, changeEntry{path: "c.go", added: 1, deleted: 2}, false)
	if !strings.Contains(both, "+1") || !strings.Contains(both, "-2") {
		t.Fatalf("both sides must render when nonzero:\n%q", both)
	}
}

// TestPanelScrollFocusedNoPreviewFallsThrough 钉（minors 清理 Final deferred）：
// 聚焦 + 变更页签 + 未展开预览 → PgUp/PgDn 不被面板消费——panel.scroll 不动、
// 落回转写滚动（Task 2 谓词收紧的判别面：现实现消费即红）。
func TestPanelScrollFocusedNoPreviewFallsThrough(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "c1", Tool: "Edit",
		Input: json.RawMessage(`{"file_path":"a.go","old_string":"x","new_string":"y"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{Seq: 2, ToolUseID: "c1", Content: "ok"}})
	// 转写溢出。
	for i := range 40 {
		m.appendLine(fmt.Sprintf("BLOCK%02d %s", i, strings.Repeat("x", 101)))
	}
	m.focus = focusPanel
	m.panel.tab = tabChanges
	m.panel.previewPath = "" // 未展开预览
	m.View()                 // frame：换行缓存 sync + viewport 喂给
	m.transcriptState.vp.SetYOffset(20)
	m.markFollowTail()
	before := m.transcriptState.vp.YOffset()
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.panel.scroll != 0 {
		t.Fatalf("no-preview PgDn must not touch panel.scroll, got %d", m.panel.scroll)
	}
	if m.transcriptState.vp.YOffset() == before {
		t.Fatalf("no-preview PgDn must fall through to transcript scroll (before %d)", before)
	}
}

// TestAgentsTabWidthTermUnfreezesAtNarrow 钉（minors 清理 T17 deferred）：
// agentsTabInteractive 的宽度项直接钉——W<130 残焦（聚焦 agents 页签后窗口
// 窄化）解冻：statusMode 不再呈现「agents 页签」、粘贴门禁解冻（handlePaste
// 与 modalOpen 同谓词单源——行为跟随）。谓词回退（删宽度项）即红。
func TestAgentsTabWidthTermUnfreezesAtNarrow(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m.panel.open = true
	m.panel.tab = tabAgents
	m.focus = focusPanel
	if !m.agentsTabInteractive() {
		t.Fatal("precondition: agents tab interactive at W=130")
	}
	if got := m.statusMode(); got != "agents 页签" {
		t.Fatalf("precondition: statusMode = %q, want agents 页签", got)
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	if m.agentsTabInteractive() {
		t.Fatal("narrowed residual focus must unfreeze the agents tab gate")
	}
	if got := m.statusMode(); got == "agents 页签" {
		t.Fatalf("narrowed statusMode must not claim agents tab, got %q", got)
	}
}
