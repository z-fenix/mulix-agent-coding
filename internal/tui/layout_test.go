package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// ---- 帧布局（FR-001：alt-screen 全接管——转写视口 + 底部固定区）----

// TestFrameLayout pins：给定 H=24，转写视口行数 = 24 − 底部固定区行数；
// 底部固定区含编辑器行与状态行；帧总行数恰为 H（alt-screen 满屏接管）。
func TestFrameLayout(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	bottomRows := lipgloss.Height(m.bottomView())
	frame := m.frame()
	if got := m.transcriptState.vp.Height(); got != 24-bottomRows {
		t.Fatalf("viewport height = %d, want %d (24 − bottom %d)", got, 24-bottomRows, bottomRows)
	}
	if got := lipgloss.Height(frame); got != 24 {
		t.Fatalf("frame height = %d, want 24（alt-screen 满屏）", got)
	}
	// 底部固定区构成：编辑器行（composer）+ 状态行
	if !strings.Contains(m.bottomView(), "> ") {
		t.Fatalf("bottom region missing composer:\n%s", m.bottomView())
	}
	if !strings.Contains(m.bottomView(), "空闲") {
		t.Fatalf("bottom region missing status line:\n%s", m.bottomView())
	}
	// 转写内容进视口区（不在底部固定区）
	m.appendLine("hello viewport")
	frame = m.frame()
	if !strings.Contains(frame, "hello viewport") {
		t.Fatalf("frame missing transcript line:\n%s", frame)
	}
	if got := m.transcriptState.vp.Height(); got != 24-bottomRows {
		t.Fatalf("viewport height changed after append: %d, want %d", got, 24-bottomRows)
	}
}
