package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
// 段随动、指引文案在场（doctor 页签含「/doctor 命令运行」指引字面）。
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
		{tabAgents, "\x1b[38;5;111;4m代理\x1b[0m", []string{"暂无子代理"}},
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
		t.Fatal("panelOpen must default true（默认三栏常开）")
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
