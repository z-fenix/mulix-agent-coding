package tui

import (
	"fmt"
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

// ---- 侧栏会话清单接线 + 聚焦恢复（Task 13）----
//
// 数据源 = Options.ListSessions（与 /resume 选择器同源同口径：打开时刻快照，
// 不随 Scan 变化）；聚焦最小形 = focusZone{focusMain,focusSidebar}（F6 轮换、
// Esc 取焦；focusPanel 与聚焦可见性归 Task 17——控制裁定，见台账 Ruling）。

// sidebarSessionsStub 3 项桩清单（ScanSessions 口径：新在前；首项为当前会话）。
func sidebarSessionsStub() []SessionSummary {
	return []SessionSummary{
		{ID: "sess-cur", EngineID: "e-1", Model: "m", Workspace: "proj-a"},
		{ID: "sess-b-0002", EngineID: "e-2", Model: "m", Workspace: "proj-b"},
		{ID: "sess-c-0003", EngineID: "e-3", Model: "m"},
	}
}

// transcriptHas 转写块流中是否已有含 substr 的系统行（守卫留痕断言面）。
func transcriptHas(t *testing.T, m *tuiModel, substr string) bool {
	t.Helper()
	for _, b := range m.transcriptState.blocks {
		if pb, ok := b.(plainBlock); ok && strings.Contains(pb.text, substr) {
			return true
		}
	}
	return false
}

// TestSidebarSessionsFromListSessions 钉（brief Step 1）：桩 ListSessions 3 项
// 经 F3 开沿快照——清单渲染同谓词输出（Task 11 行渲染）；当前会话高亮保持；
// sel 行「› 」指示可见（Task 11 deferred minor——› 与 symFold ▸ 不冲突），
// 非 sel 行两空格前缀；清单进帧（三列合成消费）。
func TestSidebarSessionsFromListSessions(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m.selfID = "sess-cur"
	m.opts.ListSessions = func() []SessionSummary { return sidebarSessionsStub() }
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24}) // 宽档（F3/F6 守卫放行）
	// 开态缺省（设计裁定）——构造时回调未注入快照为空，真源随 F3 开沿接入。
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3}) // 关
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3}) // 开 → 快照
	v := m.sidebarView(16)
	for _, want := range []string{"sess-cur", "sess-b-0", "sess-c-0"} {
		if !strings.Contains(v, want) {
			t.Fatalf("sidebar must render stub sessions from ListSessions (missing %q):\n%q", want, v)
		}
	}
	var curRow, plainRow string
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "sess-cur") {
			curRow = line
		}
		if strings.Contains(line, "sess-c-0") {
			plainRow = line
		}
	}
	// 当前会话行高亮（selfID 匹配——Task 11 谓词不变）。
	if !strings.Contains(curRow, "48;5;236") || !strings.Contains(curRow, "38;5;231") {
		t.Fatalf("current session row must keep the selfID highlight:\n%q", curRow)
	}
	// sel 指示：sel 行「› 」前缀；非 sel 行不带。
	if !strings.Contains(ansi.Strip(curRow), "›") {
		t.Fatalf("sel row must carry the › marker:\n%q", curRow)
	}
	if strings.Contains(ansi.Strip(plainRow), "›") {
		t.Fatalf("non-sel row must not carry the › marker:\n%q", plainRow)
	}
	if !strings.Contains(m.frame(), "sess-c-0") {
		t.Fatal("sidebar list must reach the frame")
	}
}

// TestSidebarSnapshotOnOpen 钉（brief）：打开时刻快照纪律——开沿恰调一次
// ListSessions；其后帧渲染/导航击键不重扫（改后的回调结果不进入已开清单）；
// 重开沿再刷新（新打开时刻新快照）。
func TestSidebarSnapshotOnOpen(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	m.selfID = "sess-cur"
	calls := 0
	m.opts.ListSessions = func() []SessionSummary {
		calls++
		if calls == 1 {
			return sidebarSessionsStub()
		}
		return []SessionSummary{{ID: "later-0001", EngineID: "e-9", Model: "m"}}
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24}) // 宽档（F3/F6 守卫放行）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})          // 关
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3})          // 开 → 快照 #1
	if calls != 1 {
		t.Fatalf("ListSessions calls = %d, want 1（打开时刻快照）", calls)
	}
	m = update(t, m, statusTick{})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6}) // 聚焦侧栏
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if calls != 1 {
		t.Fatalf("ListSessions re-called during frames/nav: %d（快照不随 Scan 变化）", calls)
	}
	if v := m.sidebarView(16); !strings.Contains(v, "sess-c-0") || strings.Contains(v, "later-0") {
		t.Fatalf("rendered list must stay the open-time snapshot:\n%q", v)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3}) // 关
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3}) // 开 → 快照 #2
	if calls != 2 {
		t.Fatalf("ListSessions calls after reopen = %d, want 2", calls)
	}
	if v := m.sidebarView(16); !strings.Contains(v, "later-0") {
		t.Fatalf("reopen must refresh the snapshot:\n%q", v)
	}
}

// TestSidebarFocusNav 钉（brief Step 1）：F6 聚焦侧栏后 ↑/↓ 环绕移动 sel
// （picker 同公式）；回主列后 ↑/↓ 不再动侧栏游标（路由只在 focusSidebar）。
func TestSidebarFocusNav(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.opts.ListSessions = func() []SessionSummary { return sidebarSessionsStub() }
	m.refreshSidebarSessions() // 快照直配（导航单测不经开合沿——构造器回调未注入）
	if m.focus != focusMain {
		t.Fatal("focus must default to main")
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24}) // 宽档（F6 守卫放行）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	if m.focus != focusSidebar {
		t.Fatalf("F6 must focus the sidebar, got %d", m.focus)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.sidebar.sel != 2 {
		t.Fatalf("↑ must wrap to last, sel = %d", m.sidebar.sel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.sidebar.sel != 0 {
		t.Fatalf("↓ must wrap back to first, sel = %d", m.sidebar.sel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6}) // 回主列
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.sidebar.sel != 0 {
		t.Fatalf("main-focus ↑ must not move sidebar sel: %d", m.sidebar.sel)
	}
}

// TestSidebarFocusKeysLeaveComposerAlone 钉：聚焦侧栏时数字/文本键照常进
// composer 且不动游标（1–9 永远进输入）；补全菜单在场（聚焦期间输入 "/c"）
// 其导航优先——↑ 动菜单游标不动侧栏游标（composer 邻接浮层先行消费）。
func TestSidebarFocusKeysLeaveComposerAlone(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.opts.ListSessions = func() []SessionSummary { return sidebarSessionsStub() }
	m.refreshSidebarSessions()
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24}) // 宽档（F6 守卫放行）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	m = update(t, m, keyPress('1'))
	m = update(t, m, keyPress('2'))
	if got := m.composerValue(); got != "12" {
		t.Fatalf("digits must reach the composer while sidebar focused: %q", got)
	}
	if m.sidebar.sel != 0 {
		t.Fatalf("text keys must not move sidebar sel: %d", m.sidebar.sel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}) // 取焦（顺带验 Esc 档）
	if m.focus != focusMain {
		t.Fatal("Esc must refocus main before clearing")
	}
	if got := m.composerValue(); got != "12" {
		t.Fatalf("refocus Esc must not consume the draft: %q", got)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}) // 再按清空（原空闲档语义不动）
	if got := m.composerValue(); got != "" {
		t.Fatalf("next Esc must clear as before: %q", got)
	}
	// 补全菜单在场时其导航优先：聚焦侧栏 + 输入 "/c" → ↑ 动菜单不动侧栏。
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	m = update(t, m, keyPress('/'))
	m = update(t, m, keyPress('c'))
	if !m.completeOpen() {
		t.Fatal("precondition: typing /c must open the completion menu")
	}
	menuSel := m.complete.sel
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.sidebar.sel != 0 {
		t.Fatalf("completion-open ↑ must not move sidebar sel: %d", m.sidebar.sel)
	}
	if m.complete.sel == menuSel && len(m.complete.items) > 1 {
		t.Fatalf("completion-open ↑ must navigate the menu (sel %d→%d)", menuSel, m.complete.sel)
	}
}

// TestSidebarEnterFailClosed 钉（brief）：空 EngineID 条目 Enter = appendLine
// 留痕拒绝、不调 swap（picker resume_picker.go:69-74 同款 fail-closed）、不置
// swapping、焦点与游标原地保留（用户可改选他项）。
func TestSidebarEnterFailClosed(t *testing.T) {
	m, _, _ := newTestModel(t)
	sw := &fakeSwapper{sink: m.sink}
	m.swapper = sw
	m.opts.ListSessions = func() []SessionSummary {
		return []SessionSummary{{ID: "no-chain-1", EngineID: "", Model: "m"}}
	}
	m.refreshSidebarSessions()
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24}) // 宽档（F6 守卫放行）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(sw.opens) != 0 {
		t.Fatalf("empty EngineID must not swap (fail-closed): %v", sw.opens)
	}
	if m.swapping {
		t.Fatal("swapping must stay false on fail-closed refusal")
	}
	if m.focus != focusSidebar || m.sidebar.sel != 0 {
		t.Fatalf("refusal must keep focus and sel (focus=%d sel=%d)", m.focus, m.sidebar.sel)
	}
	if !transcriptHas(t, m, "没有引擎 session id") {
		t.Fatal("fail-closed refusal must leave a trace line")
	}
}

// TestSidebarF6Guard 钉：侧栏关着 F6 不可聚焦（留痕——与 F2/F3/F4 守卫同
// 口径：静默会让人以为已聚焦）；开后 F6 两态轮换（主列↔侧栏）；窄档 <100
// 侧栏不渲染（几何退化）——开态残值同样拦截并留痕。
func TestSidebarF6Guard(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3}) // 关侧栏
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	if m.focus != focusMain {
		t.Fatal("F6 with closed sidebar must not focus it")
	}
	if !transcriptHas(t, m, "侧栏未开") {
		t.Fatal("closed-sidebar F6 must leave a trace")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF3}) // 重开
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	if m.focus != focusSidebar {
		t.Fatal("F6 with open sidebar must focus it")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	if m.focus != focusMain {
		t.Fatal("second F6 must cycle back to main")
	}
	m = update(t, m, tea.WindowSizeMsg{Width: 90, Height: 24}) // 窄档（开态残值）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	if m.focus != focusMain {
		t.Fatal("narrow F6 must not focus the sidebar")
	}
	if !transcriptHas(t, m, "侧栏需 ≥100 列") {
		t.Fatal("narrow F6 must leave the width-guard trace")
	}
}

// TestEscapeRefocusSidebar 钉：聚焦侧栏时空闲 Esc = 取焦回主列（消费本次
// Esc，不连清输入）；再按 Esc 才走原清空档（阶梯扩展只加取焦一档，既有档
// 语义不动）。
func TestEscapeRefocusSidebar(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	if m.focus != focusSidebar {
		t.Fatal("precondition: F6 focuses the sidebar")
	}
	m = update(t, m, keyPress('a'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.focus != focusMain {
		t.Fatal("idle Esc with sidebar focus must refocus main")
	}
	if got := m.composerValue(); got != "a" {
		t.Fatalf("refocus Esc must not clear the composer: %q", got)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if got := m.composerValue(); got != "" {
		t.Fatalf("next Esc must clear as before: %q", got)
	}
}

// TestSidebarListWindow 钉（Task 11 deferred minor——清单溢出取景窗）：纯函数
// 口径——n≤avail 全量；sel 居中锚定；两端截断收口；窗高下限 1（sel 行恒可见
// ——无静默行丢失，环绕导航可达全部行）。
func TestSidebarListWindow(t *testing.T) {
	if lo, hi := listWindow(3, 21, 1); lo != 0 || hi != 3 {
		t.Fatalf("listWindow(3,21,1) = [%d,%d), want [0,3)", lo, hi)
	}
	if lo, hi := listWindow(10, 7, 0); lo != 0 || hi != 7 {
		t.Fatalf("listWindow(10,7,0) = [%d,%d), want [0,7)", lo, hi)
	}
	if lo, hi := listWindow(10, 7, 9); lo != 3 || hi != 10 {
		t.Fatalf("listWindow(10,7,9) = [%d,%d), want [3,10)", lo, hi)
	}
	if lo, hi := listWindow(10, 7, 5); lo != 2 || hi != 9 {
		t.Fatalf("listWindow(10,7,5) = [%d,%d), want [2,9)（sel 居中）", lo, hi)
	}
	if lo, hi := listWindow(5, 0, 2); lo != 2 || hi != 3 {
		t.Fatalf("listWindow(5,0,2) = [%d,%d), want [2,3)（窗高下限 1）", lo, hi)
	}
}

// TestSidebarWindowScrolls 钉：窗随 sel 移动——sel=0 露前 7 行（尾 3 行不在
// 视图）；↑ 环绕至末项后窗跟到尾部（首行离场）；sel 行「› 」恒在窗内。
func TestSidebarWindowScrolls(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme())
	m, _, _ := newTestModel(t)
	items := make([]SessionSummary, 10)
	for i := range items {
		items[i] = SessionSummary{ID: fmt.Sprintf("id-%05d", i), EngineID: fmt.Sprintf("e-%d", i), Model: "m"}
	}
	m.opts.ListSessions = func() []SessionSummary { return items }
	m.refreshSidebarSessions()
	m = update(t, m, tea.WindowSizeMsg{Width: 130, Height: 10}) // 窗高 = 10−3 chrome = 7
	v := m.sidebarView(16)
	if !strings.Contains(v, "id-00000") || strings.Contains(v, "id-00008") {
		t.Fatalf("window must show the first 7 of 10 rows at sel=0:\n%q", v)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyF6})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyUp}) // 环绕 → sel=9
	v = m.sidebarView(16)
	if !strings.Contains(v, "id-00009") || strings.Contains(v, "id-00000") {
		t.Fatalf("window must follow sel to the tail:\n%q", v)
	}
	if !strings.Contains(ansi.Strip(v), "›") {
		t.Fatal("sel row must stay visible in the window")
	}
}
