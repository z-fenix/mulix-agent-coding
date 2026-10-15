package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"mulix/internal/event"
)

// ---- markdown 基础渲染 + 流式段落提交（Task 7：D13/D17）----

// TestSplitFlushable pins（对照参考 chat_tui.go:3078 flushableMarkdownPrefix）：
// 「围栏外空行」才界定完整段落——完整段落出 stable，尾段滞留 tail；半开围栏
// 整体留 tail（半写的代码块绝不前进而撕裂）；围栏内的空行不是段落界。
func TestSplitFlushable(t *testing.T) {
	// 完整段落出 stable；后段留 tail
	stable, tail := splitFlushablePrefix("para one\n\npara two")
	if stable != "para one" {
		t.Fatalf("stable = %q, want %q", stable, "para one")
	}
	if !strings.Contains(tail, "para two") {
		t.Fatalf("tail = %q, want the pending paragraph held back", tail)
	}
	// 半开围栏整体留 tail（stable 为空）
	stable, tail = splitFlushablePrefix("```go\nx := 1")
	if stable != "" || tail != "```go\nx := 1" {
		t.Fatalf("unclosed fence must hold everything back: stable=%q tail=%q", stable, tail)
	}
	// 围栏内的空行不算段落界
	stable, tail = splitFlushablePrefix("```go\n\ninside\n```\n\nafter fence")
	if stable != "```go\n\ninside\n```" {
		t.Fatalf("blank line inside fence is not a boundary: stable=%q", stable)
	}
	if !strings.Contains(tail, "after fence") {
		t.Fatalf("tail = %q, want post-fence text held back", tail)
	}
	// 起始即空行：无可提交前缀（boundary<=0 → 全量滞留）
	if s, tl := splitFlushablePrefix("\n\nlead blank"); s != "" || !strings.Contains(tl, "lead blank") {
		t.Fatalf("leading blank must not flush: stable=%q tail=%q", s, tl)
	}
}

// TestRenderMarkdownBasics pins（D13）：标题/加粗/行内代码/围栏/列表渲染出
// 对应 ANSI（断言含 SGR 序列与内容）；长段落按宽换行不溢出。
func TestRenderMarkdownBasics(t *testing.T) {
	// 标题：内容保留 + 加粗/着色 SGR；正文段落随后
	out := strings.Join(renderMarkdown("# 标题一\n\n正文段落", 80, nil), "\n")
	if !strings.Contains(out, "标题一") || !strings.Contains(out, "正文段落") {
		t.Fatalf("heading/paragraph content lost:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[1m") || !strings.Contains(out, "\x1b[36m") {
		t.Fatalf("heading must carry bold+accent SGR:\n%q", out)
	}
	// 加粗：**x** → SGR 包裹
	out = strings.Join(renderMarkdown("**bold** tail", 80, nil), "\n")
	if !strings.Contains(out, "\x1b[1mbold\x1b[0m") {
		t.Fatalf("bold SGR missing:\n%q", out)
	}
	// 行内代码：着色 + 内容原样
	out = strings.Join(renderMarkdown("use `foo()` here", 80, nil), "\n")
	if !strings.Contains(out, "foo()") || !strings.Contains(out, "\x1b[36m") {
		t.Fatalf("inline code accent missing:\n%q", out)
	}
	// 围栏：轨前缀 + 着色内容（中性单一样式；chroma 随 Task 19）
	out = strings.Join(renderMarkdown("```go\nfmt.Println(1)\n```", 80, nil), "\n")
	if !strings.Contains(out, "fmt.Println(1)") || !strings.Contains(out, "│ ") || !strings.Contains(out, "\x1b[36m") {
		t.Fatalf("fence rail/accent missing:\n%q", out)
	}
	// 列表：无序 • 与有序 1. 2. 标记
	out = strings.Join(renderMarkdown("- alpha\n- beta\n\n1. one\n2. two", 80, nil), "\n")
	if !strings.Contains(out, "•") || !strings.Contains(out, "alpha") || !strings.Contains(out, "1.") || !strings.Contains(out, "one") {
		t.Fatalf("list markers missing:\n%q", out)
	}
	// 按宽换行：长 CJK 段落在窄宽下逐行不溢出（SGR 零宽计列）
	long := strings.Repeat("这是一段很长的中文文本需要按宽度换行", 6)
	for _, l := range renderMarkdown(long, 20, nil) {
		if w := ansi.StringWidth(l); w > 20 {
			t.Fatalf("line overflows width 20: %d cols (%q)", w, l)
		}
	}
}

// TestCJKEmphasis pins（对照参考 md.go:107 fixCJKEmphasis）：closer ** 紧贴
// CJK 全角标点时 CommonMark 右翼判定失败 → 强调被吞；修正后必须加粗且不漏
// 字面 **。opener 侧（标点在前）不得插空格破坏左翼判定；行内代码内的字面
// ** 逐字放行。
func TestCJKEmphasis(t *testing.T) {
	// 收尾侧：**注意，**后面的字 → 「注意，」加粗
	out := strings.Join(renderMarkdown("**注意，**后面的字", 80, nil), "\n")
	if strings.Contains(out, "**") {
		t.Fatalf("CJK closer must not leak literal **:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[1m注意，\x1b[0m") {
		t.Fatalf("CJK emphasis hugging punctuation must render bold:\n%q", out)
	}
	// 开头侧：前置，**强调**字 → 「强调」加粗（opener 后不插空格）
	out = strings.Join(renderMarkdown("前置，**强调**字", 80, nil), "\n")
	if !strings.Contains(out, "\x1b[1m强调\x1b[0m") || strings.Contains(out, "**") {
		t.Fatalf("opener side must not be broken by the fix:\n%q", out)
	}
	// 行内代码：字面 ** 原样保留
	out = strings.Join(renderMarkdown("code `**raw**` end", 80, nil), "\n")
	if !strings.Contains(out, "**raw**") {
		t.Fatalf("literal ** inside code span must survive verbatim:\n%q", out)
	}
}

// TestStreamingParagraphCommit pins（D17 消息级粒度——不按 token 校准）：
// AssistantTextEvent 分片入 pending，只有完整段落成为块；流式期间活块经
// rewriteLast 原地改写不增块；半开围栏整体滞留；TurnDone 冲刷 tail 残留并
// 复位游标（半开围栏一并落块）。
func TestStreamingParagraphCommit(t *testing.T) {
	m, _, _ := newTestModel(t)
	// 首片：完整段落立刻成块，尾段滞留 pending
	m.handleEvent(event.AssistantTextEvent{Text: "第一段完整。\n\n第二段"})
	if got := len(m.transcriptState.blocks); got != 1 {
		t.Fatalf("complete paragraph must commit as one block, blocks = %d", got)
	}
	if b, ok := m.transcriptState.blocks[0].(assistantBlock); !ok || strings.Contains(b.text, "第二段") {
		t.Fatalf("incomplete tail must be held in pending: %v / %q", ok, m.transcriptState.blocks[0])
	}
	// 同消息续片（无新空行界）：活块原地保持，不增块
	m.handleEvent(event.AssistantTextEvent{Text: " 续写"})
	if got := len(m.transcriptState.blocks); got != 1 {
		t.Fatalf("streaming must not grow blocks, blocks = %d", got)
	}
	// 空行界推进：活块原地改写纳入第二段（rewriteLast——块数不变）
	m.handleEvent(event.AssistantTextEvent{Text: "\n\n第三段开场"})
	if got := len(m.transcriptState.blocks); got != 1 {
		t.Fatalf("paragraph commit must rewrite the live block in place, blocks = %d", got)
	}
	if b := m.transcriptState.blocks[0].(assistantBlock); !strings.Contains(b.text, "第二段 续写") {
		t.Fatalf("paragraph must commit after blank-line boundary: %q", b.text)
	}
	// 半开围栏整体滞留（不进块）
	m.handleEvent(event.AssistantTextEvent{Text: "\n\n```go\nfmt.Println()"})
	if b := m.transcriptState.blocks[0].(assistantBlock); strings.Contains(b.text, "fmt.Println()") {
		t.Fatalf("unclosed fence must stay in pending: %q", b.text)
	}
	if got := len(m.transcriptState.blocks); got != 1 {
		t.Fatalf("block count must stay 1 during streaming, got %d", got)
	}
	// TurnDone 冲刷残留（半开围栏一并落块）+ 复位游标
	m.handleEvent(event.TurnDoneEvent{Turn: 1})
	if m.pendingMD != "" || m.answerIdx != -1 || m.answerFlushed != 0 {
		t.Fatalf("TurnDone must flush pending and reset cursor: pending=%q idx=%d flushed=%d",
			m.pendingMD, m.answerIdx, m.answerFlushed)
	}
	// 答案块 + [回合完成] 行
	if got := len(m.transcriptState.blocks); got != 2 {
		t.Fatalf("after TurnDone want answer block + done line, blocks = %d", got)
	}
	joined := strings.Join(m.transcriptState.lines(80), "\n")
	if !strings.Contains(joined, "fmt.Println()") {
		t.Fatalf("tail must be flushed into the block on TurnDone:\n%q", joined)
	}
	// 下一条消息从新块开始（游标复位后 append 而非续写旧块）
	m.handleEvent(event.AssistantTextEvent{Text: "第二条回答\n\n"})
	m.handleEvent(event.TurnDoneEvent{Turn: 2})
	if got := len(m.transcriptState.blocks); got != 4 {
		t.Fatalf("second message must open a fresh block (answer + done), blocks = %d", got)
	}
}
