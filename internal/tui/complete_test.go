package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

// ---- 斜杠补全菜单（023 FR-009）----
//
// 语义钉（对照参考 DeepSeek-Reasonix complete.go/chat_tui.go:1424-1459）：
// `/` 起行弹菜单 → 输入过滤 → ↑/↓ 选择 → Tab/Enter 补全 → Esc 关闭；
// Enter 消歧：选中项与输入完全一致 → 落常规 Enter 直接提交（不重复补全）。

// TestCompleteNav pins：`/` 弹菜单（目录全量、选中首项、视图含条目与键位提示）
// → 过滤（/re → /resume /rewind，目录序）→ ↓ 选中第二项 → Enter 补全为
// 「/rewind 」且菜单关闭、不提交；↑ 环绕（首项上翻到末项）；Esc 关闭保文；
// 空过滤收口。
func TestCompleteNav(t *testing.T) {
	m, stub, _ := newTestModel(t)

	// `/` 弹菜单：全目录、sel=0、视图含条目与键位提示
	m = update(t, m, keyPress('/'))
	if !m.completeOpen() {
		t.Fatal("slash must open the completion menu")
	}
	if len(m.complete.items) != len(slashCatalog) || m.complete.sel != 0 {
		t.Fatalf("items=%v sel=%d, want full catalog sel=0", m.complete.items, m.complete.sel)
	}
	for _, cmd := range []string{"/new", "/resume", "/cache", "/doctor", "/agents", "/quit"} {
		if !containsItem(m.complete.items, cmd) {
			t.Fatalf("catalog missing %s: %v", cmd, m.complete.items)
		}
		if !strings.Contains(m.View().Content, cmd) {
			t.Fatalf("menu view missing %s:\n%s", cmd, m.View().Content)
		}
	}
	if !strings.Contains(m.View().Content, "esc") {
		t.Fatal("menu view must render the key hint footer")
	}

	// 过滤：/re → /resume /rewind（目录序保持）
	m = update(t, m, keyPress('r'))
	m = update(t, m, keyPress('e'))
	if got := m.complete.items; len(got) != 2 || got[0] != "/resume" || got[1] != "/rewind" {
		t.Fatalf("filtered = %v, want [/resume /rewind]", got)
	}

	// ↓ 选中第二项；Enter 补全（非精确命中 → 只补全不提交）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.complete.sel != 1 {
		t.Fatalf("sel = %d, want 1", m.complete.sel)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.composerValue() != "/rewind " {
		t.Fatalf("value = %q, want %q", m.composerValue(), "/rewind ")
	}
	if m.completeOpen() {
		t.Fatal("menu must close after accept（尾随空格出词范围）")
	}
	if len(stub.submits) != 0 {
		t.Fatalf("accept must not submit: %v", stub.submits)
	}

	// 第二次 Enter 走常规提交 → /rewind 留痕（菜单已关，不再拦截）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View().Content, "[rewind]") {
		t.Fatal("second Enter must submit the completed command")
	}

	// ↑ 环绕：/r 三项（前缀 /resume /rewind 优先 + 子序列 /doctor——参考
	// fuzzyFilterSlash 分组序），首项上翻到末项，再下翻回首项
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, typedInput("/r"))
	if got := m2.complete.items; len(got) != 3 || got[0] != "/resume" || got[1] != "/rewind" || got[2] != "/doctor" {
		t.Fatalf("filtered = %v, want [/resume /rewind /doctor]", got)
	}
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyUp})
	if m2.complete.sel != 2 {
		t.Fatalf("wrap-up sel = %d, want 2", m2.complete.sel)
	}
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyDown})
	if m2.complete.sel != 0 {
		t.Fatalf("wrap-down sel = %d, want 0", m2.complete.sel)
	}

	// Esc 关闭保文；关闭后 Enter 提交原文（未知命令留痕）
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m2.completeOpen() {
		t.Fatal("esc must close the menu")
	}
	if m2.composerValue() != "/r" {
		t.Fatalf("esc must keep the text: %q", m2.composerValue())
	}

	// 空过滤收口：/z 无命中 → 菜单不开
	m3, _, _ := newTestModel(t)
	m3 = update(t, m3, typedInput("/z"))
	if m3.completeOpen() {
		t.Fatal("empty filter must close the menu")
	}
}

// TestCompleteAlreadyPresentSubmits pins Enter 消歧（参考语义）：
//   - 唯一命中且输入已完整（/doctor）→ Enter 直接提交（菜单关闭、composer
//     清空、命令分流不进 Submit）；
//   - 唯一命中但未写全（/doct）→ Enter 只补全不提交；
//   - 忙门禁下精确命中的 Enter 保留原文无尾随空格（钉实现走提交路径而非
//     accept 路径——accept 会先补出尾随空格再被门禁）。
func TestCompleteAlreadyPresentSubmits(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, typedInput("/doctor"))
	if !m.completeOpen() || len(m.complete.items) != 1 || m.complete.items[0] != "/doctor" {
		t.Fatalf("menu = %v, want [/doctor]", m.complete.items)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 0 {
		t.Fatalf("slash command leaked to Submit: %v", stub.submits)
	}
	if m.completeOpen() {
		t.Fatal("menu must close on exact-match submit")
	}
	if m.composerValue() != "" {
		t.Fatalf("composer = %q, want cleared", m.composerValue())
	}

	// 对照：唯一命中但未写全 → Enter 只补全（第二次 Enter 才提交）
	m2, stub2, _ := newTestModel(t)
	m2 = update(t, m2, typedInput("/doct"))
	if !m2.completeOpen() {
		t.Fatal("/doct must keep the menu open (not yet complete)")
	}
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m2.composerValue() != "/doctor " {
		t.Fatalf("value = %q, want %q", m2.composerValue(), "/doctor ")
	}
	if len(stub2.submits) != 0 {
		t.Fatalf("incomplete match must not submit: %v", stub2.submits)
	}
	if m2.completeOpen() {
		t.Fatal("accept must close the menu")
	}
	// 消歧闭环：补全后已完整 → 第二次 Enter 直接提交（composer 清空、仍不走
	// Submit 通道）
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m2.composerValue() != "" {
		t.Fatalf("second Enter must submit the completed command: %q", m2.composerValue())
	}
	if len(stub2.submits) != 0 {
		t.Fatalf("slash still must not leak to Submit: %v", stub2.submits)
	}

	// 忙门禁形态：running 期精确命中 → 菜单关闭、原文保留（无尾随空格——
	// 若误走 accept 路径会先补出空格再被门禁吞掉）
	m3, _, _ := newTestModel(t)
	m3 = update(t, m3, typedInput("/doctor"))
	m3.running = true
	m3 = update(t, m3, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m3.composerValue() != "/doctor" {
		t.Fatalf("busy exact-match value = %q, want untouched %q", m3.composerValue(), "/doctor")
	}
	if m3.completeOpen() {
		t.Fatal("busy exact-match must still close the menu")
	}
}

// TestCompleteSyncEdges pins 补全态随编辑器内容全量同步（对照参考「Update 末尾
// 统一重过滤」）：提交清空收口；粘贴 "/" 起头短文本（低于折叠阈值）随值重开；
// 菜单开着粘入非斜杠内容（值不再是纯 "/词" 记号）收口。
func TestCompleteSyncEdges(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, typedInput("/cache"))
	if !m.completeOpen() {
		t.Fatal("menu must be open on /cache")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // 提交（清空 composer）→ 收口
	if m.completeOpen() || m.composerValue() != "" {
		t.Fatalf("submit must clear composer and menu: open=%v val=%q", m.completeOpen(), m.composerValue())
	}
	// 粘贴 "/" 起头文本 → 菜单随值重开
	m = update(t, m, tea.PasteMsg{Content: "/doctor"})
	if !m.completeOpen() || m.composerValue() != "/doctor" {
		t.Fatalf("paste must re-open menu: open=%v val=%q", m.completeOpen(), m.composerValue())
	}
	// 粘进带空格内容 → 值离开纯 "/词" 记号形态（词界即出范围）→ 收口
	m = update(t, m, tea.PasteMsg{Content: " note"})
	if m.completeOpen() {
		t.Fatalf("non-token value must close the menu: val=%q", m.composerValue())
	}
}

func containsItem(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}
