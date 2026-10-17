package tui

import (
	"regexp"
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
	// 标题：内容保留 + 加粗；dsh 文字层级（Task 5）——标题行 fg-primary 加粗
	//（设计 §6）：中性缺省无 fg 槽仅加粗；确定性 256 层主题下钉 fg SGR（231）。
	out := strings.Join(renderMarkdown("# 标题一\n\n正文段落", 80, nil), "\n")
	if !strings.Contains(out, "标题一") || !strings.Contains(out, "正文段落") {
		t.Fatalf("heading/paragraph content lost:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[1m标题一\x1b[0m") {
		t.Fatalf("heading must be bold (neutral default has no fg slot):\n%q", out)
	}
	th := defaultActiveTheme()
	outH := strings.Join(renderMarkdown("# 标题", 80, &th), "\n")
	if !strings.Contains(outH, "\x1b[1m\x1b[38;5;231m标题\x1b[0m") {
		t.Fatalf("heading must take the fg-primary slot (dsh 文字层级):\n%q", outH)
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
	// 答案块（025 FR-005：正常完成无收口行——回执另由 UsageEvent 驱动）
	if got := len(m.transcriptState.blocks); got != 1 {
		t.Fatalf("after TurnDone want answer block only, blocks = %d", got)
	}
	joined := strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
	// Task 19 起 go 围栏经 chroma 高亮——token 间夹 SGR，内容比对先剥样式
	//（chroma 不改文本，见 TestFenceChromaHighlight 的 strip 钉点）。
	if !strings.Contains(ansi.Strip(joined), "fmt.Println()") {
		t.Fatalf("tail must be flushed into the block on TurnDone:\n%q", joined)
	}
	// 下一条消息从新块开始（游标复位后 append 而非续写旧块）
	m.handleEvent(event.AssistantTextEvent{Text: "第二条回答\n\n"})
	m.handleEvent(event.TurnDoneEvent{Turn: 2})
	if got := len(m.transcriptState.blocks); got != 2 {
		t.Fatalf("second message must open a fresh answer block, blocks = %d", got)
	}
}

// TestFenceChromaHighlight pins（Task 19：FR-014/Story4）：
//
//	(a) go 围栏经 chroma 高亮——terminal256 SGR 直出，关键字 func 以独立
//	    token 着色（SGR+reset 包裹），内容混 ≥3 种色（轨 245 + ≥2 token
//	    类），SGR 剥离后源码逐字无损；暗/亮主题输出不同（暗族 github-dark /
//	    亮族 github——映射裁定见 markdown.go fenceChromaStyle 注记）；
//	(b) 未知语言降级 accent 铺色——内容色只允许 accent(173)/轨(245)；
//	(c) nil 中性缺省保持 Task 7 单一样式契约（go 围栏同为 accent 铺色）；
//	(d) 无色层（tierNone）围栏零 SGR——与 NO_COLOR 退避同路径，内容仍在。
func TestFenceChromaHighlight(t *testing.T) {
	goFence := "```go\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```"
	codeRE := regexp.MustCompile(`\x1b\[38;5;(\d+)m`)
	distinct := func(s string) map[string]bool {
		set := map[string]bool{}
		for _, m := range codeRE.FindAllStringSubmatch(s, -1) {
			set[m[1]] = true
		}
		return set
	}

	// (a) 暗底主题（graphite 暗 · 确定性 256 层，与包级初值同源）
	dark := defaultActiveTheme()
	out := strings.Join(renderMarkdown(goFence, 80, &dark), "\n")
	if !strings.Contains(out, "\x1b[38;5;") {
		t.Fatalf("go fence must carry chroma 256-color SGR:\n%q", out)
	}
	if !regexp.MustCompile(`\x1b\[[0-9;]*mfunc\x1b\[0m`).MatchString(out) {
		t.Fatalf("keyword func must be chroma-colored as its own token:\n%q", out)
	}
	if got := distinct(out); len(got) < 3 {
		t.Fatalf("fence must mix >=3 distinct colors (rail + >=2 token classes), got %v:\n%q", got, out)
	}
	if plain := ansi.Strip(out); !strings.Contains(plain, "func main() {") || !strings.Contains(plain, `fmt.Println("hi")`) {
		t.Fatalf("chroma must not alter code text:\n%q", plain)
	}
	// 亮底变体：同样 chroma 高亮，但样式族不同 → 与暗底输出不同
	light, ok := buildTheme("graphite", false, tierANSI256)
	if !ok {
		t.Fatal("graphite-light must resolve")
	}
	outL := strings.Join(renderMarkdown(goFence, 80, &light), "\n")
	if !strings.Contains(outL, "\x1b[38;5;") || outL == out {
		t.Fatalf("light theme must chroma-highlight with a different style family:\ndark=%q\nlight=%q", out, outL)
	}

	// (b) 未知语言：降级 accent 铺色，不携带 chroma 内容色
	outU := strings.Join(renderMarkdown("```notalang\nplain text\n```", 80, &dark), "\n")
	if !strings.Contains(outU, "\x1b[38;5;173mplain text\x1b[0m") {
		t.Fatalf("unknown language must degrade to accent fill:\n%q", outU)
	}
	for c := range distinct(outU) {
		if c != "173" && c != "244" && c != "235" { // 235 = 围栏底 codeBlock 槽（Task 5）
			t.Fatalf("unknown-language fence must not carry chroma colors, got %s:\n%q", c, outU)
		}
	}

	// (c) nil 中性缺省：Task 7 契约——accent 铺色（36）+ 轨（90）之外无色
	outN := strings.Join(renderMarkdown(goFence, 80, nil), "\n")
	for c := range distinct(outN) {
		if c != "36" && c != "90" {
			t.Fatalf("neutral default must keep single-style fence, got %s:\n%q", c, outN)
		}
	}

	// (d) 无色层：围栏零 SGR（NO_COLOR 退避同路径），内容与轨仍在
	nc, ok := buildTheme("graphite", true, tierNone)
	if !ok {
		t.Fatal("graphite no-color theme must resolve")
	}
	outC := strings.Join(renderMarkdown(goFence, 80, &nc), "\n")
	if strings.Contains(outC, "\x1b[") || !strings.Contains(outC, "func") || !strings.Contains(outC, "│ ") {
		t.Fatalf("no-color tier must render fences unstyled:\n%q", outC)
	}
}

// TestFenceCodeBlockBG（Task 5 Step 1）：围栏内容行整行铺 codeBlockColor 槽
// （48;5;235——graphite 暗 256 出厂初值），chroma 高亮路径与降级铺色路径同装；
// 行内 token SGR 的整段重置由围栏底 repaint 收口（重置点重铺，复用 layout.go
// paintBG 同款锚点）——token 色分之后的正文不落回帧底；无色层零 bg。
func TestFenceCodeBlockBG(t *testing.T) {
	th := defaultActiveTheme()
	// chroma 路径（go 围栏）：内容行含 codeBlock bg，且重置点后有 repaint
	out := strings.Join(renderMarkdown("```go\nfmt.Println(1)\n```", 80, &th), "\n")
	if !strings.Contains(out, "\x1b[48;5;235m") {
		t.Fatalf("chroma fence lines must carry the codeBlock bg:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[0m\x1b[48;5;235m") {
		t.Fatalf("fence bg must repaint after in-line SGR resets:\n%q", out)
	}
	// 降级铺色路径（未知语言）同样整行铺底
	outU := strings.Join(renderMarkdown("```notalang\nplain text\n```", 80, &th), "\n")
	if !strings.Contains(outU, "\x1b[48;5;235m") {
		t.Fatalf("plain fence lines must carry the codeBlock bg:\n%q", outU)
	}
	// 无色层：围栏零 bg（槽位全空——NO_COLOR 退避同路径）
	nc, ok := buildTheme("graphite", true, tierNone)
	if !ok {
		t.Fatal("graphite no-color theme must resolve")
	}
	if outC := strings.Join(renderMarkdown("```go\nfmt.Println(1)\n```", 80, &nc), "\n"); strings.Contains(outC, "\x1b[48;") {
		t.Fatalf("no-color tier must render fences without bg:\n%q", outC)
	}
}

// TestTableRender pins（Task 19：FR-014/Story4）：goldmark 表格 AST → 对齐
// 表格——列宽取单元格显示宽（CJK 计两列），dim 竖轨分隔、表头加粗、表头下
// ─┼─ 规则线；各行（含规则线）显示宽相等 ⇒ 轨不因 CJK 错位（若按 rune 数
// 计宽，含 CJK 的行会宽出 2 列）；窄宽下列宽按比例封顶、长单元格换行不截断
// 且整表不超可用宽；对齐冒号（:---:）读入不生效（对照参考——左对齐）。
func TestTableRender(t *testing.T) {
	src := "| Name | 数量 | Note |\n" +
		"|---|---|---|\n" +
		"| alpha | 3 | ok |\n" +
		"| beta | 42 | 中文注释 |"
	out := renderMarkdown(src, 80, nil)
	joined := strings.Join(out, "\n")
	for _, cell := range []string{"Name", "数量", "Note", "alpha", "beta", "中文注释"} {
		if !strings.Contains(joined, cell) {
			t.Fatalf("table cell %q lost:\n%q", cell, joined)
		}
	}
	if !strings.Contains(joined, "\x1b[1mName") {
		t.Fatalf("header must be bold:\n%q", joined)
	}
	if !strings.Contains(joined, "─┼─") {
		t.Fatalf("header rule missing:\n%q", joined)
	}
	var widths []int
	for _, l := range out {
		if strings.Contains(l, "│") || strings.Contains(l, "┼") {
			widths = append(widths, ansi.StringWidth(l))
		}
	}
	if len(widths) != 4 { // 表头 + 规则线 + 2 数据行
		t.Fatalf("want 4 aligned table lines, got %d:\n%q", len(widths), joined)
	}
	for _, w := range widths {
		if w != widths[0] {
			t.Fatalf("table rows misaligned (CJK width?): widths=%v\n%q", widths, joined)
		}
	}

	// 窄宽封顶：整表不超可用宽，长 CJK 单元格换行不截断
	outN := renderMarkdown(src, 22, nil)
	for _, l := range outN {
		if w := ansi.StringWidth(l); (strings.Contains(l, "│") || strings.Contains(l, "┼")) && w > 22 {
			t.Fatalf("narrow table overflows width 22: %d cols (%q)", w, l)
		}
	}
	joinedN := strings.Join(outN, "\n")
	if !strings.Contains(joinedN, "中文注") || !strings.Contains(joinedN, "释") {
		t.Fatalf("narrow table must wrap long cells, not truncate:\n%q", joinedN)
	}
}
