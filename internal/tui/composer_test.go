package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ---- 编辑器测试（023 D12 / FR-007：多行编辑器 + 历史召回）----
//
// brief Step 1 四组钉测试 + 滚轮嵌套补钉：
//   TestHistoryRecallKeepsDraft  ↑↓ 历史召回（保草稿、↓ 到底还原）
//   TestNewlineKeys              Alt+Enter/Ctrl+J 换行，Enter 提交
//   TestComposerDynamicHeight    编辑器动态高度 1→8（底部固定区随行数收口）
//   TestWordOps                  Ctrl+Backspace 删词 + Alt|Ctrl+方向键词移动
//   TestComposerWheelNesting     编辑器溢出时滚轮归编辑器，未溢出落转写视口

func keyUpKey() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyUp} }
func keyDownKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyDown} }

func keyCtrlKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
func keyAltEnter() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt} }

// composerRows 编辑器渲染行数（动态高度 1→8 的量测口径——textarea View 实际行）。
func composerRows(m *tuiModel) int {
	return lipgloss.Height(m.composerState.ta.View())
}

// TestHistoryRecallKeepsDraft 钉（brief (a)）：输入草稿 → 提交 → 输新草稿 →
// ↑ 两次召回 → ↓ 到底还原草稿。提交成功派发（Enter → pendingSubmit 路径）
// 即入历史；首 ↑ 存草稿跳最新一条；↑ 到最早驻留；↓ 越过最新一条还原召回前
// 的草稿并回到草稿态（histIdx=-1）。
func TestHistoryRecallKeepsDraft(t *testing.T) {
	m, stub, _ := newTestModel(t)
	// 输入草稿 → 提交（成功派发入历史）
	m = update(t, m, typeStr(m, "hello"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 || stub.submits[0] != "hello" {
		t.Fatalf("submits = %v", stub.submits)
	}
	if len(m.composerState.history) != 1 || m.composerState.history[0] != "hello" {
		t.Fatalf("history = %v, want [hello]", m.composerState.history)
	}
	// 输新草稿 → ↑ 两次召回
	m = update(t, m, typeStr(m, "draft two"))
	m = update(t, m, keyUpKey())
	if got := m.composerValue(); got != "hello" {
		t.Fatalf("first Up must recall the last submitted input: %q", got)
	}
	if m.composerState.histIdx != 0 {
		t.Fatalf("histIdx = %d, want 0", m.composerState.histIdx)
	}
	m = update(t, m, keyUpKey()) // 已在最早一条——驻留
	if got := m.composerValue(); got != "hello" {
		t.Fatalf("Up at the oldest entry must stay: %q", got)
	}
	// ↓ 到底还原草稿
	m = update(t, m, keyDownKey())
	if got := m.composerValue(); got != "draft two" {
		t.Fatalf("Down past the newest entry must restore the draft: %q", got)
	}
	if m.composerState.histIdx != -1 {
		t.Fatalf("histIdx = %d, want -1 (back on draft)", m.composerState.histIdx)
	}
}

// TestNewlineKeys 钉（brief (b)）：Alt+Enter / Ctrl+J 换行而裸 Enter 提交
// 整段多行草稿（FR-007 键位语义）。
func TestNewlineKeys(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, typeStr(m, "one"))
	m = update(t, m, keyAltEnter())
	if got := m.composerValue(); got != "one\n" {
		t.Fatalf("Alt+Enter must insert a newline: %q", got)
	}
	m = update(t, m, typeStr(m, "two"))
	m = update(t, m, keyCtrlKey('j'))
	m = update(t, m, typeStr(m, "three"))
	if got := m.composerValue(); got != "one\ntwo\nthree" {
		t.Fatalf("Ctrl+J must insert a newline: %q", got)
	}
	// 裸 Enter 不换行——提交整段
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 || stub.submits[0] != "one\ntwo\nthree" {
		t.Fatalf("Enter must submit the multi-line draft: %v", stub.submits)
	}
}

// TestComposerDynamicHeight 钉（brief (c)）：1 行文本 → 1 行高；9 行文本 →
// 8 行高截断（MaxHeight 上限，超出部分编辑器内滚）；底部固定区行数随编辑器
// 实际行数收口（frame 的 lipgloss.Height 量测路径）。
func TestComposerDynamicHeight(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	m = update(t, m, typeStr(m, "single"))
	if got := composerRows(m); got != 1 {
		t.Fatalf("single line composer rows = %d, want 1", got)
	}
	// 9 行文本（"single" + 8 个换行）→ 8 行高截断
	for i := 0; i < 8; i++ {
		m = update(t, m, keyAltEnter())
	}
	if got := composerRows(m); got != composerMaxRows {
		t.Fatalf("9-line content must cap at %d rows: %d", composerMaxRows, got)
	}
	// 底部固定区 = 编辑器 8 行 + 状态栏 2 行（FR-012 双行状态栏，Task 17）
	if got := lipgloss.Height(m.bottomView()); got != composerMaxRows+2 {
		t.Fatalf("bottom region height = %d, want %d (editor %d + status 2)",
			got, composerMaxRows+2, composerMaxRows)
	}
}

// TestWordOps 钉（brief (d)）：Ctrl+Backspace 删词；Ctrl/Alt+方向键词移动
// （键位对照参考 chat_tui.go:700 configureChatTextarea——Alt 与 Ctrl 双轨）。
func TestWordOps(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, typeStr(m, "hello world"))
	m = update(t, m, keyCtrlKey(tea.KeyBackspace))
	if got := m.composerValue(); got != "hello " {
		t.Fatalf("Ctrl+Backspace must delete the word behind the cursor: %q", got)
	}
	// 词移动：Ctrl+Left 到词首，Alt+Right 越词尾（同义双轨抽一验证方向语义）
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, typeStr(m2, "foo bar"))
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
	if col := m2.composerState.ta.Column(); col != 4 {
		t.Fatalf("Ctrl+Left must jump to the word start: col=%d, want 4", col)
	}
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt})
	if col := m2.composerState.ta.Column(); col != 7 {
		t.Fatalf("Alt+Right must jump past the word: col=%d, want 7", col)
	}
}

// TestRecallGatedWhenBusy 钉（评审 Important-1，brief「↑↓ 空闲时历史召回」）：
// 忙碌期（running/swapping）↑/↓ 不做召回——落 textarea 光标移动（单行首/末行
// no-op，多行草稿内移动光标）。危害向量：忙碌期在编辑器写多行草稿、光标第 0
// 行按 ↑（最常见场景）若执行召回，composer 内容被历史条目可见替换（草稿虽在
// draft 可经 ↓ 找回），回合结束后 Enter 会提交召回文本而非草稿。忙碌解除后
// 召回恢复。
func TestRecallGatedWhenBusy(t *testing.T) {
	m, stub, _ := newTestModel(t)
	// 种历史：提交一条
	m = update(t, m, typeStr(m, "hello"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.composerState.history) != 1 {
		t.Fatalf("precondition: history = %v", m.composerState.history)
	}
	// 忙碌期（running）：单行草稿、光标在第 0 行按 ↑——不得替换草稿
	m.running = true
	m = update(t, m, typeStr(m, "draft"))
	m = update(t, m, keyUpKey())
	if got := m.composerValue(); got != "draft" {
		t.Fatalf("busy Up must not replace the draft with history: %q", got)
	}
	if m.composerState.histIdx != -1 {
		t.Fatalf("busy Up must not enter recall: histIdx=%d", m.composerState.histIdx)
	}
	// 多行草稿内忙碌 ↑：光标移动（textarea 语义，非跨条召回）
	m = update(t, m, keyAltEnter())
	m = update(t, m, typeStr(m, "line2"))
	m = update(t, m, keyUpKey())
	if m.composerState.ta.Line() != 0 {
		t.Fatalf("busy Up must move the cursor within the draft: line=%d", m.composerState.ta.Line())
	}
	if got := m.composerValue(); got != "draft\nline2" {
		t.Fatalf("busy Up must keep the draft: %q", got)
	}
	// swapping 过渡期同门禁
	m.swapping = true
	m.setComposerValue("draft2")
	m = update(t, m, keyUpKey())
	if got := m.composerValue(); got != "draft2" {
		t.Fatalf("swapping Up must not recall: %q", got)
	}
	if m.composerState.histIdx != -1 {
		t.Fatalf("swapping Up must not latch recall: histIdx=%d", m.composerState.histIdx)
	}
	m.swapping = false
	m.running = false
	// 空闲恢复：↑ 召回生效
	m = update(t, m, keyUpKey())
	if got := m.composerValue(); got != "hello" {
		t.Fatalf("idle Up must recall after busy clears: %q", got)
	}
	if len(stub.submits) != 1 {
		t.Fatalf("submits = %v", stub.submits)
	}
}

// TestComposerWheelNesting 钉（Task 6 停车场路由至此的最小实现）：编辑器未
// 溢出时滚轮落转写视口（既有手势不变）；编辑器溢出（内容超过 MaxHeight 行）
// 时滚轮归编辑器内滚——textarea 自己裁决可滚性（内建 viewport 空间不足时
// no-op，ScrollYOffset 前后差即消费凭据），未消费才落转写。
func TestComposerWheelNesting(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	// 转写撑出溢出并同步视口（follow 态钉底）
	m.appendLine(strings.Repeat("t", 200))
	for i := 0; i < 30; i++ {
		m.appendLine("transcript row")
	}
	m.frame()
	// 未溢出：滚轮上落转写视口
	before := m.transcriptState.vp.YOffset()
	m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if got := m.transcriptState.vp.YOffset(); got >= before {
		t.Fatalf("wheel must fall through to the transcript when the composer fits: %d !< %d", got, before)
	}
	if off := m.composerState.ta.ScrollYOffset(); off != 0 {
		t.Fatalf("fitting composer must not scroll: offset=%d", off)
	}
	// 编辑器溢出：滚轮下归编辑器内滚，转写视口不动
	for i := 0; i < 10; i++ {
		m = update(t, m, typeStr(m, "line"))
		m = update(t, m, keyAltEnter())
	}
	if composerRows(m) != composerMaxRows {
		t.Fatalf("precondition: overflowing composer must cap at %d rows", composerMaxRows)
	}
	vpBefore := m.transcriptState.vp.YOffset()
	m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if off := m.composerState.ta.ScrollYOffset(); off == 0 {
		t.Fatal("overflowing composer must consume the wheel (inner scroll)")
	}
	if got := m.transcriptState.vp.YOffset(); got != vpBefore {
		t.Fatalf("transcript viewport must not move while the composer scrolls: %d != %d", got, vpBefore)
	}
}
