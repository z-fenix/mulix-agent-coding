package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestResizeAnchorPreservesReadingPosition（024 FR-001）：溢出转写、上翻至
// 中部、宽度变更——视口顶行仍对准锚定块。块文本设计为「80 列单行 / 50 列
// 双行」，重排后绝对行号漂移一倍：无锚点时顶行必落到更早的块（RED 判别力）。
func TestResizeAnchorPreservesReadingPosition(t *testing.T) {
	m, _, _ := newTestModel(t)
	update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := range 40 {
		m.appendLine(fmt.Sprintf("BLOCK%02d %s", i, strings.Repeat("x", 55)))
	}
	m.View() // 触发 frame：按 80 列 sync + 喂给

	ts := &m.transcriptState
	m.markUserScrolled()
	ts.vp.SetYOffset(15) // 块 15 首行（80 列下每块 1 行）
	anchorText := ts.wrappedLines[ts.vp.YOffset()]
	anchorBlock := strings.Fields(anchorText)[0]
	if anchorBlock != "BLOCK15" {
		t.Fatalf("前置：顶行应为 BLOCK15, got %q", anchorText)
	}

	update(t, m, tea.WindowSizeMsg{Width: 50, Height: 24})
	m.View() // 新宽全量重排 + 喂给 + 锚点恢复

	newTop := strings.Fields(ts.wrappedLines[ts.vp.YOffset()])[0]
	if newTop != anchorBlock {
		t.Fatalf("重排后视口顶行应仍对准锚定块 %s, got %q（wrappedLines[%d]）",
			anchorBlock, newTop, ts.vp.YOffset())
	}
}

// TestResizeAnchorFollowTailUnchanged：尾随态对照——宽度变更后仍钉底，
// 锚点路径不得把尾随用户拉离底部。
func TestResizeAnchorFollowTailUnchanged(t *testing.T) {
	m, _, _ := newTestModel(t)
	update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := range 40 {
		m.appendLine(fmt.Sprintf("BLOCK%02d %s", i, strings.Repeat("x", 55)))
	}
	m.View()
	if !m.transcriptState.vp.AtBottom() {
		t.Fatalf("前置：尾随态应在底部")
	}
	update(t, m, tea.WindowSizeMsg{Width: 50, Height: 24})
	m.View()
	if !m.transcriptState.vp.AtBottom() {
		t.Fatalf("尾随态宽度变更后应在底部（got YOffset=%d / total=%d）",
			m.transcriptState.vp.YOffset(), len(m.transcriptState.wrappedLines))
	}
}
