package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"mulix/internal/event"
)

// ---- 025 交谈 chrome 对齐（基线：baseline-reasonix-frames.log）----

func TestChromeBanner(t *testing.T) {
	b := bannerBlock{model: "claude-x"}
	lines := b.render(80)
	if len(lines) != 2 {
		t.Fatalf("banner lines = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "◆ mulix · claude-x") {
		t.Fatalf("banner line1: %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) == "" {
		t.Fatalf("banner hint line empty")
	}
}

// TestChromeBannerBG（Task 5）：横幅 accent 着色 + 满帧铺底合成——帧出口
// paintBG 整行铺 bg 槽、重置点重铺，accent 前景不被冲掉（设计 §4.3/§6 横幅
// 「accent 着色，bg 铺底」——铺底由帧级 paintBG 统一承载，结构不变）。
func TestChromeBannerBG(t *testing.T) {
	resetTheme(t)
	line := bannerBlock{model: "claude-x"}.render(80)[0]
	if !strings.Contains(line, "\x1b[38;5;173m") { // accent 槽（graphite 暗 256 出厂初值）
		t.Fatalf("banner must take the accent slot:\n%q", line)
	}
	painted := paintBG(line, 80)
	if !strings.Contains(painted, "\x1b[48;5;234m") { // bg 槽整行铺底
		t.Fatalf("banner line must be bg-painted at frame exit:\n%q", painted)
	}
	if !strings.Contains(painted, "\x1b[38;5;173m") { // accent 在重铺后存活
		t.Fatalf("frame bg repaint must not clobber the accent fg:\n%q", painted)
	}
}

func TestChromeUserBubble(t *testing.T) {
	got := userBlock{text: "你好"}.render(80)
	// dsh 气泡换装（Task 5）：styleBubble 包裹（SGR 在槽位前缀之外）——剥样式
	// 后仍钉「槽 2 + 内衬 1 + › 」前缀字面（› 前缀保留，符号表 symUserMsg）。
	if !strings.HasPrefix(strings.TrimRight(ansi.Strip(got[0]), " "), "   › 你好") {
		t.Fatalf("user bubble: %q", got[0])
	}
}

func TestChromeAssistantGutter(t *testing.T) {
	got := assistantBlock{text: "你好"}.render(80)
	if !strings.HasPrefix(got[0], "  ") {
		t.Fatalf("assistant gutter: %q", got[0])
	}
	if !strings.Contains(got[0], "你好") {
		t.Fatalf("assistant content: %q", got[0])
	}
}

func TestChromeToolCardGutter(t *testing.T) {
	c := &toolCard{toolUseID: "t1", tool: "Bash", arg: "sleep", state: toolDone}
	for i, l := range c.render(80) {
		if !strings.HasPrefix(l, "  ") {
			t.Fatalf("tool card line %d not in gutter: %q", i, l)
		}
	}
}

func TestChromeThinkingFold(t *testing.T) {
	b := thinkingBlock{
		tail:      "x",
		startedAt: time.Unix(0, 0),
		endedAt:   time.Unix(3, 0),
		collapsed: true,
	}
	got := b.render(80)[0]
	if !strings.Contains(got, "▸ 思考了 3 秒 (Ctrl+O)") {
		t.Fatalf("thinking fold: %q", got)
	}
	if strings.Contains(got, "tok") {
		t.Fatalf("fold line must not carry tok slot: %q", got)
	}
}

func TestChromeThinkingStreamMarker(t *testing.T) {
	b := thinkingBlock{tail: "x", startedAt: time.Unix(0, 0), endedAt: time.Unix(1, 0)}
	got := b.render(80)
	if !strings.Contains(got[0], "▎ thinking…") {
		t.Fatalf("streaming marker: %q", got[0])
	}
}

func TestChromeTurnHeader(t *testing.T) {
	m, _, _ := newTestModel(t)
	update(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	update(t, m, tuiEvent{ev: event.TurnStartedEvent{Seq: 1}})
	update(t, m, tuiEvent{ev: event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "# 标题\n\n正文段落"}})
	if len(m.transcriptState.blocks) < 2 {
		t.Fatalf("blocks = %d, want ≥2（头行 + 答文）", len(m.transcriptState.blocks))
	}
	hb, ok := m.transcriptState.blocks[0].(assistantHeaderBlock)
	if !ok {
		t.Fatalf("blocks[0] 应为助手头行块, got %T", m.transcriptState.blocks[0])
	}
	line := hb.render(80)[0]
	if !strings.HasPrefix(line, "  ") || !strings.Contains(line, "◆ mulix") {
		t.Fatalf("header must be slotted per baseline (SGR 在 ◆ 之前): %q", line)
	}
}
