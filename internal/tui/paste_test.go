package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
)

// ---- 粘贴折叠测试（023 FR-008）----
//
// brief Step 1 四组钉测试 + 边界补钉：
//   TestPasteFolds              大粘贴折叠为记号、folds 记录原文（含字符规则）
//   TestFoldDeleteRemovesAll    删除记号（含相邻字符）后正文不残留
//   TestSubmitExpandsFolds      提交还原全文（含多 fold 有序展开）
//   TestPasteBelowThreshold     低于阈值原文直插不折叠
//   TestFoldDeletedNoResurrect  删除记号后再粘贴提交——旧正文不复活、id 单调
//   TestFoldEditedLabelLiteral  记号标签被改坏——按字面文本对待不展开

// pasteFoldBody 10 行折叠样例正文（每行互异，便于残留断言）。
func pasteFoldBody() string {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("paste line %d", i)
	}
	return strings.Join(lines, "\n")
}

// foldLabel 折叠记号串（与 pasteFold.label 同形——测试不依赖实现方法）。
func foldLabel(id, lines int) string {
	return fmt.Sprintf("[粘贴 #%d · %d 行]", id, lines)
}

// deleteBackspace 从编辑器尾部连发 n 次退格（光标置尾语义下逐字符删除）。
func deleteBackspace(t *testing.T, m *tuiModel, n int) *tuiModel {
	t.Helper()
	for i := 0; i < n; i++ {
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	return m
}

// TestPasteFolds 钉（brief (a)）：bracketed paste 10 行 → 记号入编辑器、
// folds 记录原文；单行但 ≥200 字符的粘贴按字符规则同样折叠。
func TestPasteFolds(t *testing.T) {
	m, _, _ := newTestModel(t)
	body := pasteFoldBody()
	m = update(t, m, tea.PasteMsg{Content: body})
	if got := m.composerValue(); got != foldLabel(1, 10)+" " {
		t.Fatalf("folded paste must leave the token in the editor: %q", got)
	}
	if len(m.composerState.folds) != 1 || m.composerState.folds[0].body != body {
		t.Fatalf("folds must record the original body: %+v", m.composerState.folds)
	}
	// 字符规则：单行 ≥200 字符也折叠
	m2, _, _ := newTestModel(t)
	long := strings.Repeat("x", pasteFoldThresholdChars)
	m2 = update(t, m2, tea.PasteMsg{Content: long})
	if len(m2.composerState.folds) != 1 {
		t.Fatalf("single-line paste of %d chars must fold by the char rule", pasteFoldThresholdChars)
	}
}

// TestFoldDeleteRemovesAll 钉（brief (b)）：删除记号（含相邻字符）后正文
// 不残留——删除扫过记号与相邻字符后编辑器无正文残段，fold 账目同步剪除
// （提交不复活的前提）。
func TestFoldDeleteRemovesAll(t *testing.T) {
	m, _, _ := newTestModel(t)
	body := pasteFoldBody()
	m = update(t, m, tea.PasteMsg{Content: body})
	// 记号后追加相邻字符，随后连同记号一并删除（删除扫过 "xx" + 尾随空格 +
	// 整个记号）
	m = update(t, m, typeStr(m, "xx"))
	sweep := "xx " + foldLabel(1, 10)
	m = deleteBackspace(t, m, len([]rune(sweep)))
	if got := m.composerValue(); got != "" {
		t.Fatalf("token and neighbours must be deletable: %q", got)
	}
	if strings.Contains(m.composerValue(), "paste line") {
		t.Fatalf("paste body must not linger in the editor: %q", m.composerValue())
	}
	if len(m.composerState.folds) != 0 {
		t.Fatalf("deleted fold must drop the body from the store: %+v", m.composerState.folds)
	}
}

// TestSubmitExpandsFolds 钉（brief (c)）：提交文本含原文全文而非记号；多
// fold 按记号位置有序展开；提交后 fold 账目清场。
func TestSubmitExpandsFolds(t *testing.T) {
	m, stub, _ := newTestModel(t)
	body := pasteFoldBody()
	m = update(t, m, typeStr(m, "before "))
	m = update(t, m, tea.PasteMsg{Content: body})
	m = update(t, m, typeStr(m, "after"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	want := "before " + body + " after"
	if len(stub.submits) != 1 {
		t.Fatalf("submit expected once: %v", stub.submits)
	}
	if stub.submits[0] != want {
		t.Fatalf("submit must expand the token back to the full body:\n got %q\nwant %q", stub.submits[0], want)
	}
	if len(m.composerState.folds) != 0 {
		t.Fatalf("submit must clear the fold store: %+v", m.composerState.folds)
	}
	// 多 fold：两段大粘贴依次折叠，提交按记号位置有序展开（记号自带尾随
	// 空格——前导侧不加空格，键入 "mid " 后粘贴即隔词）
	m2, stub2, _ := newTestModel(t)
	bodyA := pasteFoldBody()
	bodyB := "B0\nB1\nB2\nB3"
	m2 = update(t, m2, tea.PasteMsg{Content: bodyA})
	m2 = update(t, m2, typeStr(m2, "mid "))
	m2 = update(t, m2, tea.PasteMsg{Content: bodyB})
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	want2 := bodyA + " mid " + bodyB
	if len(stub2.submits) != 1 {
		t.Fatalf("multi-fold submit expected once: %v", stub2.submits)
	}
	if stub2.submits[0] != want2 {
		t.Fatalf("multi-fold submit must expand in token order:\n got %q\nwant %q", stub2.submits[0], want2)
	}
}

// TestPasteBelowThreshold 钉（brief (d)）：2 行且 <200 字符——双阈值都不
// 触发，粘贴原文直插、不记账。
func TestPasteBelowThreshold(t *testing.T) {
	m, _, _ := newTestModel(t)
	small := "l1\nl2"
	m = update(t, m, tea.PasteMsg{Content: small})
	if got := m.composerValue(); got != small {
		t.Fatalf("below-threshold paste must insert verbatim: %q", got)
	}
	if len(m.composerState.folds) != 0 {
		t.Fatalf("below-threshold paste must not fold: %+v", m.composerState.folds)
	}
}

// TestFoldDeletedNoResurrect 补钉（边界：删除后提交不复活）：删除记号后
// 再粘贴提交——新折叠 id 单调递增（不复用旧号），旧正文不随提交复活。
func TestFoldDeletedNoResurrect(t *testing.T) {
	m, stub, _ := newTestModel(t)
	body1 := pasteFoldBody()
	m = update(t, m, tea.PasteMsg{Content: body1})
	m = deleteBackspace(t, m, len([]rune(foldLabel(1, 10)+" ")))
	if got := m.composerValue(); got != "" {
		t.Fatalf("precondition: token deleted: %q", got)
	}
	// 删除后再粘贴：id 单调递增不回收（记号串无歧义）
	body2 := "x0\nx1\nx2"
	m = update(t, m, tea.PasteMsg{Content: body2})
	if got := m.composerValue(); got != foldLabel(2, 3)+" " {
		t.Fatalf("fold ids must increase monotonically: %q", got)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 {
		t.Fatalf("submit expected once: %v", stub.submits)
	}
	if stub.submits[0] != body2 {
		t.Fatalf("deleted fold must not resurrect on submit:\n got %q\nwant %q", stub.submits[0], body2)
	}
}

// TestFoldEditedLabelStaysLiteral 补钉（边界：记号被改坏）：在标签串中间
// 打入字符后提交——记号不再匹配，按字面文本对待（正文不展开、不复活）。
func TestFoldEditedLabelStaysLiteral(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, tea.PasteMsg{Content: pasteFoldBody()})
	// 光标左移两格（落在 ] 之前）打入字符——标签串改坏
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = update(t, m, keyPress('X'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(stub.submits) != 1 {
		t.Fatalf("submit expected once: %v", stub.submits)
	}
	got := stub.submits[0]
	if strings.Contains(got, "paste line 0") {
		t.Fatalf("edited label must not expand: %q", got)
	}
	if !strings.Contains(got, "行X]") {
		t.Fatalf("edited label must stay literal text: %q", got)
	}
}
