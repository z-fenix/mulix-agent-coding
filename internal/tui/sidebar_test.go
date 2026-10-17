package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---- 侧栏视图与 F3 开合（2026-10-17-dsh-tui-visual-redesign Task 11）----
//
// 钉点用 deepseek dark 出厂 256 层字面（TestThemePresets 同源）：accent 111、
// fg 231、fgSecondary 252、surface2 236、dim 244。清单数据本任务为桩
//（sidebarState.sessions 测试直配；Task 13 接 Options.ListSessions 打开时快照）。

// TestSidebarViewStubList 钉（brief Step 1）：桩清单 3 项——顶品牌行 ◆ mulix
// （accent）、「会话」目、当前会话行 surface2 整行高亮 + fg-primary、其余行
// fg-secondary 且超宽省略号截断、底部 /new 提示。
func TestSidebarViewStubList(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme()) // 确定性 256 层（槽位字面见文件头注记）
	m, _, _ := newTestModel(t)
	m.sidebar.sessions = []SessionSummary{
		{ID: "sess-cur", EngineID: "e-1", Model: "m", Workspace: "proj-a"},
		{ID: "sess-b-longer-id", EngineID: "e-2", Model: "m", Workspace: "workplace-project-mulix-agent-coding"},
		{ID: "sess-ccc", EngineID: "e-3", Model: "m"},
	}
	m.selfID = "sess-cur"
	const w = 16
	v := m.sidebarView(w)

	if !strings.Contains(v, "◆ mulix") || !strings.Contains(v, "\x1b[38;5;111m") {
		t.Fatalf("sidebar must open with the brand row ◆ mulix in accent:\n%q", v)
	}
	if !strings.Contains(v, "会话") {
		t.Fatalf("sidebar missing the sessions section label:\n%q", v)
	}
	if !strings.Contains(v, "/new 新会话") {
		t.Fatalf("sidebar missing the bottom hint:\n%q", v)
	}

	var curRow, longRow, plainRow string
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "sess-cur") {
			curRow = line
		}
		if strings.Contains(line, "sess-b-l") { // 截断后的长行（workspace 只剩前缀）
			longRow = line
		}
		if strings.Contains(line, "sess-ccc") {
			plainRow = line
		}
	}
	if curRow == "" || longRow == "" || plainRow == "" {
		t.Fatalf("stub rows missing from sidebar view (cur=%q long=%q plain=%q):\n%q", curRow, longRow, plainRow, v)
	}
	// 当前会话行：surface2 底（48;5;236）+ fg-primary（38;5;231）——槽位参数
	// 断言（合成序无关：单条合并 SGR 或分段序列皆可）；整行铺满列宽（截断后
	// padToWidth——底色覆盖 16 列）。
	if !strings.Contains(curRow, "48;5;236") {
		t.Fatalf("current session row must take the surface2 slot:\n%q", curRow)
	}
	if !strings.Contains(curRow, "38;5;231") {
		t.Fatalf("current session row must take the fg-primary slot:\n%q", curRow)
	}
	if got := ansi.StringWidth(ansi.Strip(curRow)); got != w {
		t.Fatalf("current session row width = %d, want %d（整行高亮铺满列宽）", got, w)
	}
	// 其余行：fg-secondary（38;5;252）、无 surface2 底；超宽行省略号截断且
	// 显示宽不超列宽。
	for name, row := range map[string]string{"long": longRow, "plain": plainRow} {
		if strings.Contains(row, "48;5;236") {
			t.Fatalf("%s row must not take the surface2 slot:\n%q", name, row)
		}
		if !strings.Contains(row, "\x1b[38;5;252m") {
			t.Fatalf("%s row must take the fg-secondary slot:\n%q", name, row)
		}
		if got := ansi.StringWidth(ansi.Strip(row)); got > w {
			t.Fatalf("%s row width %d exceeds column width %d", name, got, w)
		}
	}
	if !strings.Contains(longRow, "…") {
		t.Fatalf("overlong row must be truncated with an ellipsis:\n%q", longRow)
	}
}

// TestSidebarViewEmpty 钉（brief Step 1）：空清单给行动指引文案而非空白
// （spec User Story 4——空态指引）；品牌行与底部提示照常在场。
func TestSidebarViewEmpty(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	v := m.sidebarView(16)
	if !strings.Contains(v, "暂无历史会话") {
		t.Fatalf("empty sidebar must show the empty-state guidance:\n%q", v)
	}
	if !strings.Contains(v, "◆ mulix") || !strings.Contains(v, "/new 新会话") {
		t.Fatalf("empty sidebar must keep the brand row and bottom hint:\n%q", v)
	}
}

// TestToggleSidebarNarrowGuard 钉（brief Step 1）：W=90 窄档 F3 经 appendLine
// 留痕「侧栏需 ≥100 列」不进入开态（档位判定用 W 直接比较——Task 9 契约：
// sidebar==0 亦可能来自 open=false，不是窄档判据）。窄档几何不随开合位移：
// mainWidth 恒等于 W（单列退化）。
func TestToggleSidebarNarrowGuard(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	m.sidebarOpen = false // 从关态按 F3——守卫必须拦下开态
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	if m.sidebarOpen {
		t.Fatal("F3 at W=90 must not open the sidebar")
	}
	found := false
	for _, b := range m.transcriptState.blocks {
		if pb, ok := b.(plainBlock); ok && strings.Contains(pb.text, "侧栏需 ≥100 列") {
			found = true
		}
	}
	if !found {
		t.Fatalf("narrow F3 must leave the guard trace line in the transcript")
	}
	if got := m.mainWidth(); got != 90 {
		t.Fatalf("narrow mainWidth = %d, want 90（窄档开合无关——单列退化）", got)
	}
}

// TestToggleSidebarWide 钉（brief）：W=130 开态默认（设计裁定：默认三栏常开）
// → F3 关：main 让位收宽（columnWidths 经 mainWidth 断言：130−32=98，仅面板
// 让位）、侧栏列视图离场、composer 跟 mainWidth 重排（同宽开合不走
// WindowSizeMsg——model.go Update 注记的开合同步）；再按 F3 开回：main 复原
// 130−16−32=82、侧栏视图回场。
func TestToggleSidebarWide(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	if !m.sidebarOpen {
		t.Fatal("sidebarOpen must default true（默认三栏常开）")
	}
	if got := m.mainWidth(); got != 82 {
		t.Fatalf("open mainWidth = %d, want 82（130−16−32）", got)
	}
	if !strings.Contains(m.frame(), "◆ mulix") {
		t.Fatal("open sidebar must render the brand row into the frame")
	}

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	if m.sidebarOpen {
		t.Fatal("F3 must close the sidebar at W=130")
	}
	if got := m.mainWidth(); got != 98 {
		t.Fatalf("main width after close = %d, want 98（130−32，仅面板让位）", got)
	}
	if strings.Contains(m.frame(), "◆ mulix") {
		t.Fatal("closed sidebar must leave the frame（空白列随开合消失）")
	}
	if got := m.composerState.ta.Width(); got != 98-composerPromptWidth {
		t.Fatalf("composer width after close = %d, want %d（开合同步重排）",
			got, 98-composerPromptWidth)
	}

	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})
	if !m.sidebarOpen {
		t.Fatal("second F3 must reopen the sidebar")
	}
	if got := m.mainWidth(); got != 82 {
		t.Fatalf("main width after reopen = %d, want 82", got)
	}
	if !strings.Contains(m.frame(), "◆ mulix") {
		t.Fatal("reopened sidebar must render the brand row again")
	}
}
