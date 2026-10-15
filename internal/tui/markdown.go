package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ---- markdown 基础渲染 + 流式段落提交（023 D13/D17，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）：
//   - internal/cli/md.go：goldmark AST→ANSI 渲染思路与 fixCJKEmphasis
//     （md.go:107——closer ** 紧贴 CJK 全角标点时右翼判定失败，收尾侧补空格
//     修正；只补 closer 侧，opener 侧补空格反而破坏左翼判定）。adopted：标题/
//     段落/列表/围栏/分隔线与 strong/em/codespan/link 行内渲染、
//     fixCJKEmphasis / isCJKPunct / wrapAnsi。adapted：参考的 GFM 表格扩展、
//     引用轨、数学解析、copy 模式与 colorprofile 门控不在本任务范围（表格/
//     引用降级为子节点平铺；围栏中性单一样式，chroma 着色随 Task 19；主题
//     槽已随 Task 18 落地（theme.go，见下方注记））；围栏内容按可用宽换行
//    （参考放行长行——本仓 viewport 不软换，溢出行会破帧）。
//   - internal/cli/chat_tui.go:3078 flushableMarkdownPrefix：段落提交语义——
//     「围栏外空行」才界定完整段落，半开围栏整体滞留。adapted：参考只返回可
//     提交前缀，本仓 splitFlushablePrefix 同时返回 (stable, tail)。
//
// 主题（Task 18）：theme 语义槽类型落地于 theme.go（023 FR-014/D8）——
// renderMarkdown 的 th 参数即真实主题（nil 仍取中性缺省，见 defaultTheme），
// 块渲染线程化见 transcript.go assistantBlock（&activeTheme，换肤后重排换色）。

// bold/italic 文本装饰（no-color.org：NO_COLOR 只禁色不禁装饰——属性恒开）。
func bold(s string) string   { return sgrWrap("1", s) }
func italic(s string) string { return sgrWrap("3", s) }

// renderMarkdown 把 markdown 源按宽渲染为 ANSI 样式行（goldmark 解析 +
// AST 遍历渲染）。支持聊天式模型稳定产出的构造：标题/段落/列表/围栏/分隔线
// 与 strong/em/codespan/link 行内元素，其余降级为子节点平铺（对照参考
// md.go Render）。CJK 强调修正必经（先修源再解析）。空源返回 nil；
// width<=0 取 80；th 为 nil 取中性主题。
func renderMarkdown(src string, width int, th *theme) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	if width <= 0 {
		width = 80
	}
	if th == nil {
		th = defaultTheme()
	}
	r := &mdRenderer{md: goldmark.New(), width: width, th: th}
	s := []byte(fixCJKEmphasis(src))
	doc := r.md.Parser().Parse(text.NewReader(s))
	var buf strings.Builder
	r.renderBlocks(&buf, doc, s, 0)
	out := strings.TrimRight(buf.String(), "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// mdRenderer markdown→ANSI 渲染器（对照参考 md.go mdRenderer——裁剪到本任务
// 范围：无表格/引用轨/数学/copy 模式；宽换行走 ansi.Wrap，SGR 零宽计列）。
type mdRenderer struct {
	md    goldmark.Markdown
	width int
	th    *theme
}

func (r *mdRenderer) renderBlocks(buf *strings.Builder, parent ast.Node, src []byte, indent int) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		r.renderBlock(buf, c, src, indent)
	}
}

func (r *mdRenderer) renderBlock(buf *strings.Builder, node ast.Node, src []byte, indent int) {
	switch n := node.(type) {
	case *ast.Heading:
		r.renderHeading(buf, n, src, indent)
	case *ast.Paragraph:
		r.renderInlineBlock(buf, n, src, indent, true)
	case *ast.TextBlock:
		// TextBlock 是 goldmark 为紧凑列表项行内容设的容器（无尾随空行）。
		r.renderInlineBlock(buf, n, src, indent, false)
	case *ast.List:
		r.renderList(buf, n, src, indent)
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		r.renderFenced(buf, n, src, indent)
	case *ast.ThematicBreak:
		w := max(r.width-indent, 8)
		buf.WriteString(strings.Repeat(" ", indent))
		buf.WriteString(r.th.dim(strings.Repeat("─", w)))
		buf.WriteString("\n\n")
	default:
		// 本任务范围外的块（引用/表格等）降级为子节点平铺，不丢内容。
		r.renderBlocks(buf, node, src, indent)
	}
}

func (r *mdRenderer) renderHeading(buf *strings.Builder, n *ast.Heading, src []byte, indent int) {
	inline := r.collectInline(n, src)
	buf.WriteString(strings.Repeat(" ", indent))
	buf.WriteString(bold(r.th.accent(inline)))
	buf.WriteString("\n")
	// 一级标题补强调色下划线（对照参考 renderHeading——层级一眼可辨，深层级
	// 只靠加粗+着色不再叠视觉重量）。
	if n.Level == 1 {
		buf.WriteString(strings.Repeat(" ", indent))
		buf.WriteString(r.th.accent(strings.Repeat("─", max(ansi.StringWidth(inline), 1))))
		buf.WriteString("\n")
	}
	buf.WriteString("\n")
}

func (r *mdRenderer) renderInlineBlock(buf *strings.Builder, n ast.Node, src []byte, indent int, trailingBlank bool) {
	inline := r.collectInline(n, src)
	prefix := strings.Repeat(" ", indent)
	wrapped := wrapAnsi(inline, r.width-indent)
	for line := range strings.SplitSeq(wrapped, "\n") {
		buf.WriteString(prefix)
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	if trailingBlank {
		buf.WriteString("\n")
	}
}

func (r *mdRenderer) renderList(buf *strings.Builder, n *ast.List, src []byte, indent int) {
	idx := 1
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		item, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		var marker string
		if n.IsOrdered() {
			marker = fmt.Sprintf("%d.", idx)
			idx++
		} else {
			marker = "•"
		}
		buf.WriteString(strings.Repeat(" ", indent))
		buf.WriteString(r.th.accent(marker) + " ")
		markerW := ansi.StringWidth(marker) + 1

		first := item.FirstChild()
		// goldmark 以 TextBlock 承载紧凑列表项行内容、Paragraph 承载松列表项
		//——两者统一作为标记行载体（对照参考 inlineCarrier）。
		inlineHost := inlineCarrier(first)
		if inlineHost != nil {
			inline := r.collectInline(inlineHost, src)
			wrapped := wrapAnsi(inline, r.width-indent-markerW)
			lines := strings.Split(wrapped, "\n")
			buf.WriteString(lines[0] + "\n")
			for _, l := range lines[1:] {
				buf.WriteString(strings.Repeat(" ", indent+markerW))
				buf.WriteString(l + "\n")
			}
			for s := first.NextSibling(); s != nil; s = s.NextSibling() {
				r.renderBlock(buf, s, src, indent+markerW)
			}
		} else {
			buf.WriteString("\n")
			r.renderBlocks(buf, item, src, indent+2)
		}
	}
	buf.WriteString("\n")
}

func (r *mdRenderer) renderFenced(buf *strings.Builder, n ast.Node, src []byte, indent int) {
	// 中性单一样式（chroma 语法着色随 Task 19）：轨前缀 + 强调色内容。内容按
	// 可用宽换行——本仓 viewport 不软换，放行长行会溢出破帧（参考放行，本仓
	// 收口，见文件头 adapted 注记）。
	prefix := strings.Repeat(" ", indent) + r.th.dim("│ ")
	avail := max(r.width-indent-ansi.StringWidth("│ "), 4)
	for i := range n.Lines().Len() {
		l := n.Lines().At(i)
		line := strings.TrimRight(string(l.Value(src)), "\n")
		for _, wl := range strings.Split(wrapAnsi(line, avail), "\n") {
			buf.WriteString(prefix)
			buf.WriteString(r.th.accent(wl))
			buf.WriteString("\n")
		}
	}
	buf.WriteString("\n")
}

// collectInline 遍历行内子树返回 ANSI 样式平文本。
func (r *mdRenderer) collectInline(n ast.Node, src []byte) string {
	var b strings.Builder
	r.appendInline(&b, n, src)
	return b.String()
}

func (r *mdRenderer) appendInline(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *ast.Text:
			b.Write(v.Segment.Value(src))
			switch {
			case v.HardLineBreak():
				b.WriteByte('\n')
			case v.SoftLineBreak():
				b.WriteByte(' ') // CommonMark：软换行合并为空格
			}
		case *ast.Emphasis:
			var inner strings.Builder
			r.appendInline(&inner, v, src)
			if v.Level == 2 {
				b.WriteString(bold(inner.String()))
			} else {
				b.WriteString(italic(inner.String()))
			}
		case *ast.CodeSpan:
			var inner strings.Builder
			r.appendInline(&inner, v, src)
			b.WriteString(r.th.accent(inner.String()))
		case *ast.Link:
			var inner strings.Builder
			r.appendInline(&inner, v, src)
			b.WriteString(inner.String())
			b.WriteString(r.th.dim(" (" + string(v.Destination) + ")"))
		case *ast.AutoLink:
			b.WriteString(string(v.URL(src)))
		case *ast.RawHTML:
			// drop——聊天输出罕见，打印字面转义反而脏
		case *ast.String:
			b.Write(v.Value)
		default:
			r.appendInline(b, c, src)
		}
	}
}

// inlineCarrier 返回行内容器节点（段落或文本块——紧/松列表统一处理，
// 对照参考 md.go inlineCarrier）。
func inlineCarrier(n ast.Node) ast.Node {
	switch n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return n
	}
	return nil
}

// wrapAnsi 按宽词换行（超宽单词硬断——CJK 无词间空格即走此路）；SGR 零宽
// 计列、全角字符计两列（对照参考 md.go wrapAnsi——x/ansi Wrap 薄封装）。
func wrapAnsi(s string, width int) string {
	if width < 4 {
		width = 4
	}
	return ansi.Wrap(s, width, "")
}

// fixCJKEmphasis 绕过 goldmark/CommonMark 不把 CJK 全角标点识别为 Unicode
// 标点的问题：closer ** 仅在前邻字符是标点时才算右翼，故「**X，**Y」（，=
// U+FF0C）不会加粗。在该类 closer 后补一个空格即修复右翼判定；空格只能补
// 在 closer 侧——补在 opener 后（，**X** → ，** X**）反而破坏左翼判定，因此
// 以运行开关追踪强调开合。行内代码与围栏逐字放行，代码里的字面 ** 不受影响。
// 强调不跨硬换行——上一行未闭合的 ** 不能把下一行的 opener 误判成 closer。
// （对照参考 md.go:107 fixCJKEmphasis。）
func fixCJKEmphasis(s string) string {
	runes := []rune(s)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(s) + 16)

	inFenced := false   // ``` 围栏内
	inCode := false     // ` 行内代码内
	inEmphasis := false // opener ** 与其 closer 之间

	for i := 0; i < n; i++ {
		r := runes[i]

		// 围栏：``` 开合。
		if r == '`' && i+2 < n && runes[i+1] == '`' && runes[i+2] == '`' {
			inFenced = !inFenced
			b.WriteString("```")
			i += 2
			continue
		}
		// 行内代码：` 开合（围栏内除外）。
		if r == '`' && !inFenced {
			inCode = !inCode
			b.WriteRune(r)
			continue
		}
		// 代码内逐字放行。
		if inCode || inFenced {
			b.WriteRune(r)
			continue
		}
		if r == '\n' {
			inEmphasis = false
			b.WriteRune(r)
			continue
		}

		if r == '*' && i+1 < n && runes[i+1] == '*' {
			b.WriteString("**")
			i++
			inEmphasis = !inEmphasis

			// 仅 closer（强调刚闭合）紧贴 CJK 标点需要尾随空格。
			if !inEmphasis && i >= 2 && !isSpace(runes[i-2]) && isCJKPunct(runes[i-2]) {
				b.WriteByte(' ')
			}
			continue
		}

		b.WriteRune(r)
	}
	return b.String()
}

// isCJKPunct 报告 r 是否 CJK 全角标点。这类字符不被 CommonMark 归为 Unicode
// 标点，会破坏强调的右翼判定（对照参考 md.go isCJKPunct）。
func isCJKPunct(r rune) bool {
	if r <= 0x7F {
		return false // ASCII 标点 CommonMark 已正确处理
	}
	switch {
	case r >= 0x3000 && r <= 0x303F: // CJK 符号与标点（。、等）
		return true
	case r >= 0xFF01 && r <= 0xFF0F: // 全角 Forms I（！"#$ 等）
		return true
	case r >= 0xFF1A && r <= 0xFF20: // 全角 Forms II（:;<=> 等）
		return true
	case r >= 0xFF3B && r <= 0xFF3F: // 全角 Forms III（[\]^_）
		return true
	case r >= 0xFF5B && r <= 0xFF65: // 全角 Forms IV（{|}~ 等）
		return true
	}
	// 兜底：其余非 ASCII 标点（如藏文、亚美尼亚文）。
	return unicode.IsPunct(r)
}

// isSpace 报告 r 是否空白字符。
func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// splitFlushablePrefix 返回 buf 中由完整 markdown 块构成的最长前缀（stable）
// 与滞留余部（tail）：文本推进到围栏外最后一个空行为止；``` / ~~~ 围栏内的
// 空行不是段落界，半写的代码块整体滞留至闭合。（对照参考 chat_tui.go:3078
// flushableMarkdownPrefix——参考只取前缀，本仓同时返回余部。）
func splitFlushablePrefix(buf string) (stable, tail string) {
	lines := strings.Split(buf, "\n")
	inFence := false
	boundary := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if !inFence && t == "" {
			boundary = i
		}
	}
	if boundary <= 0 {
		return "", buf
	}
	return strings.Join(lines[:boundary], "\n"), strings.Join(lines[boundary:], "\n")
}
