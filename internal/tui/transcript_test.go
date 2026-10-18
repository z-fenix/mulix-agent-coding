package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// flattenTranscript 测试观测面（024 T3：生产 lines() 删除后，测试经缓存
// sync + 前缀展平等价观测——断言语义与原 lines() 返回逐字一致）。
func flattenTranscript(ts *transcriptState, width int) []string {
	ts.syncWrappedLines(width, false)
	return ts.wrappedLines
}

// ---- 转写块模型（FR-001：语义保留块 → 按宽渲染行）----

// TestBlockAppendRewrite pins：append 3 块 + rewriteLast 后 lines(80) 反映
// 改写且块数不变（运行中块原地改写——流式/思考/工具尾的块模型基础；块是
// 语义单元，渲染行按宽展平，lipgloss 行尾补齐至宽——对照参考 wrap_cache
// 的 lipgloss 换行法，plan 评审钉定）。
func TestBlockAppendRewrite(t *testing.T) {
	ts := newTranscriptState()
	ts.append(plainBlock{text: "alpha"})
	ts.append(userBlock{text: "beta"})
	ts.append(assistantBlock{text: "gamma"})
	ts.rewriteLast(plainBlock{text: "gamma-rewritten"})
	if len(ts.blocks) != 3 {
		t.Fatalf("blocks = %d, want 3（rewriteLast 必须原地改写不增块）", len(ts.blocks))
	}
	lines := flattenTranscript(&ts, 80)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	// 气泡换装（Task 5）：styleBubble 包裹（SGR 在槽位前缀之外）——剥样式后
	// 仍钉「› 文本」内容字面（› 前缀保留，符号表 symUserMsg）。
	if strings.TrimSpace(lines[0]) != "alpha" || strings.TrimSpace(ansi.Strip(lines[1])) != "› beta" {
		t.Fatalf("earlier blocks must be untouched: %q / %q", lines[0], lines[1])
	}
	if strings.TrimSpace(lines[2]) != "gamma-rewritten" {
		t.Fatalf("rewriteLast not reflected in lines: %q", lines[2])
	}

	// 多行语义块按 markdown 渲染：段内软换行合并为空格（CommonMark 语义，
	// Task 7 起 assistantBlock 走 renderMarkdown——对照参考 md.go appendInline
	// SoftLineBreak→' '；硬断行走 fixCJKEmphasis 前源不受影响）
	ts2 := newTranscriptState()
	ts2.append(assistantBlock{text: "l1\nl2"})
	if got := flattenTranscript(&ts2, 80); len(got) != 1 || strings.TrimSpace(got[0]) != "l1 l2" {
		t.Fatalf("soft line break must merge to a space (markdown): %q", got)
	}

	// 防御：空块流上 rewriteLast 视为追加（不 panic）
	ts3 := newTranscriptState()
	ts3.rewriteLast(plainBlock{text: "first"})
	if len(ts3.blocks) != 1 || strings.TrimSpace(flattenTranscript(&ts3, 80)[0]) != "first" {
		t.Fatalf("rewriteLast on empty transcript must append: %d blocks", len(ts3.blocks))
	}
}

// TestViewportCloneFeed pins（克隆契约，对照参考 wrap_cache.go:80-88）：
// viewport 持有喂给切片本体——喂给后改写/增长内部渲染缓存不得波及 viewport
// 已持有内容；再喂后新内容可见。
func TestViewportCloneFeed(t *testing.T) {
	ts := newTranscriptState()
	ts.append(plainBlock{text: "line-one"})
	ts.syncViewport(80, 6)
	fed := ts.wrappedLines
	if len(fed) == 0 {
		t.Fatal("render cache must hold the fed lines")
	}
	want := ts.vp.GetContent()
	fed[0] = "POLLUTED" // 原地改写内部缓存
	if got := ts.vp.GetContent(); got != want {
		t.Fatalf("viewport content mutated through internal cache:\n%q", got)
	}
	before := ts.vp.TotalLineCount()
	ts.wrappedLines = append(ts.wrappedLines, "grown") // 缓存增长
	if got := ts.vp.TotalLineCount(); got != before {
		t.Fatalf("viewport line count changed on cache growth: %d → %d", before, got)
	}
	if got := ts.vp.GetContent(); !strings.Contains(got, "line-one") || strings.Contains(got, "POLLUTED") {
		t.Fatalf("viewport must keep its own copy: %q", got)
	}
	// 追加新块后再喂：新内容可见（增量缓存 suffix 路径——只重排新块）
	ts.append(plainBlock{text: "line-two"})
	ts.syncViewport(80, 6)
	if got := ts.vp.GetContent(); !strings.Contains(got, "line-two") {
		t.Fatalf("viewport must reflect appended block after sync: %q", got)
	}
	if h := ts.vp.Height(); h != 6 {
		t.Fatalf("viewport height = %d, want 6", h)
	}
}

// ---- transcript 换装（2026-10-17-dsh-tui-visual-redesign Task 5）----

// TestUserBubbleSurface2 钉点（Task 5 Step 1）：userBlock 渲染行含 surface2
// bg SGR（48;5;236——graphite 暗 256 出厂初值）且无圆角边框字符（dsh 气泡 =
// 底色块，无边框——设计 §6，与 styleCard 的圆角边框形态区分）；`›` 前缀保留
// 在气泡内首行（符号表 symUserMsg）；多行气泡逐行同底（矩形连续）。
func TestUserBubbleSurface2(t *testing.T) {
	resetTheme(t)
	got := userBlock{text: "你好"}.render(80)
	if !strings.Contains(got[0], "\x1b[48;5;236m") {
		t.Fatalf("user bubble must carry the surface2 bg SGR:\n%q", got[0])
	}
	for i, l := range got {
		if strings.ContainsAny(l, "╭╮╰╯") {
			t.Fatalf("bubble must be borderless (dsh 底色块), line %d: %q", i, l)
		}
	}
	if !strings.Contains(ansi.Strip(got[0]), "› 你好") {
		t.Fatalf("› prefix must survive the bubble restyle:\n%q", got[0])
	}
	// minors 清理（T5 deferred）：气泡几何溢出钉——width−6 内衬算术在 CJK 混排下
	// 显示宽不超给定宽（算术回退 width−4 即溢出红；wrapcache 计量同口径）。
	cjk := userBlock{text: "这是一段较长的中文气泡内容用于验证宽度计量不溢出"}.render(40)
	for i, l := range cjk {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("bubble line %d display width %d exceeds 40:\n%q", i, w, l)
		}
	}
	// 多行输入：每个气泡行（含内衬空行）都带 surface2 bg
	multi := userBlock{text: "第一行\n\n第二行"}.render(80)
	for i, l := range multi {
		if !strings.Contains(l, "\x1b[48;5;236m") {
			t.Fatalf("bubble line %d must carry the surface2 bg:\n%q", i, l)
		}
	}
}

// TestCJKWrapUnchangedWithBG（Task 5 Review Focus 3 钉点）：同一含中文段落经
// 用户气泡（surface2 底）与满帧铺底（paintBG）后，wrappedLines 行数与剥 SGR
// 的逐行内容均与无 bg 时相等——SGR 零宽计列，铺底不得改变换行点（wrapcache
// 契约）。无 bg 参照 = tierNone 主题（槽位全空，同结构退平文）。
func TestCJKWrapUnchangedWithBG(t *testing.T) {
	resetTheme(t)
	para := "在长会话中保留语义块边界，中文段落按显示宽换行；全角字符计两列、SGR 零宽不计列——满帧铺底与气泡底色都不得改变换行点。"
	build := func() []string {
		ts := newTranscriptState()
		ts.append(userBlock{text: para})
		ts.append(assistantBlock{text: para})
		return flattenTranscript(&ts, 60)
	}
	withBG := build()
	thNone, ok := buildTheme("graphite", true, tierNone)
	if !ok {
		t.Fatal("graphite no-color theme must resolve")
	}
	applyTheme(thNone)
	noBG := build()
	applyTheme(defaultActiveTheme())
	if len(withBG) != len(noBG) {
		t.Fatalf("bg must not change wrappedLines count: with=%d without=%d", len(withBG), len(noBG))
	}
	for i := range noBG {
		if got, want := ansi.Strip(withBG[i]), noBG[i]; got != want {
			t.Fatalf("wrap point drifted at line %d:\nwithBG=%q\nnoBG=%q", i, got, want)
		}
	}
	// 帧出口 paintBG 整行包裹不增减行（有色/无色两路都过铺底出口）
	for name, lines := range map[string][]string{"withBG": withBG, "noBG": noBG} {
		painted := strings.Split(paintBG(strings.Join(lines, "\n"), 60), "\n")
		if len(painted) != len(lines) {
			t.Fatalf("paintBG must not change %s line count: %d → %d", name, len(lines), len(painted))
		}
	}
}
