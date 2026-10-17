package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
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
//     fixCJKEmphasis / isCJKPunct / wrapAnsi；GFM 表格扩展与表格渲染
//     （renderTable/renderTableRow/collectCells，对照参考 md.go:406-538——
//     列宽按显示宽、比例封顶、长格换行不截断；对齐冒号读入不生效）随
//     Task 19 采纳。adapted：参考的引用轨、数学解析、copy 模式与
//     colorprofile 门控不在本任务范围（引用降级为子节点平铺）；围栏内容按
//    可用宽换行（参考放行长行——本仓 viewport 不软换，溢出行会破帧）。
//     围栏 chroma 高亮为本仓新增（参考 md.go 围栏恒为 accent 铺色）——样式
//     族选型对照参考 diffview.go activeDiffChromaStyle，见 fenceChromaStyle
//     注记；主题槽已随 Task 18 落地（theme.go，见下方注记）。
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
// AST 遍历渲染）。支持聊天式模型稳定产出的构造：标题/段落/列表/围栏/表格/
// 分隔线与 strong/em/codespan/link 行内元素，其余降级为子节点平铺（对照参考
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
	// GFM 表格扩展：| … | 行解析为 Table AST 而非字面段落（对照参考
	// newMarkdownRenderer 注记）。
	r := &mdRenderer{
		md:    goldmark.New(goldmark.WithExtensions(extension.Table)),
		width: width,
		th:    th,
	}
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
// 范围：无引用轨/数学/copy 模式，表格自 Task 19 接入；宽换行走 ansi.Wrap，
// SGR 零宽计列）。
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
	case *extast.Table:
		r.renderTable(buf, n, src, indent)
	case *ast.ThematicBreak:
		w := max(r.width-indent, 8)
		buf.WriteString(strings.Repeat(" ", indent))
		buf.WriteString(r.th.dim(strings.Repeat("─", w)))
		buf.WriteString("\n\n")
	default:
		// 本任务范围外的块（引用等）降级为子节点平铺，不丢内容。
		r.renderBlocks(buf, node, src, indent)
	}
}

func (r *mdRenderer) renderHeading(buf *strings.Builder, n *ast.Heading, src []byte, indent int) {
	inline := r.collectInline(n, src)
	buf.WriteString(strings.Repeat(" ", indent))
	// dsh 文字层级（换装 Task 5，设计 §6）：标题行 fg-primary 加粗——旧
	// accent 标题随强调槽语义收敛（accent 留给品牌/状态/交互点）；一级标题
	// 下划线仍 accent（层级一眼可辨，深层级只靠加粗不再叠视觉重量）。
	buf.WriteString(bold(r.th.fg(inline)))
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
	prefix := strings.Repeat(" ", indent) + r.th.dim("│ ")
	avail := max(r.width-indent-ansi.StringWidth("│ "), 4)
	if hl, ok := r.chromaFence(n, src); ok {
		// chroma 内容按可用宽换行（本仓 viewport 不软换，见文件头注记）。
		// 硬断可能落在 token SGR 态中——续行到下一转义前短暂失色（终端
		// 跨行 SGR 态被轨前缀收尾的 reset 清除），仅超宽代码行观感问题，
		// 内容无损。围栏底（dsh 换装 Task 5）整行铺 codeBlockColor 槽，
		// 行内 token reset 的重铺由 th.fenceBG 收口；与帧出口 paintBG 的
		// 合成序（reset → 帧底插序在前 → 围栏底重铺在后，后写生效）由
		// layout_test.go TestFenceBGSurvivesFramePaint 钉死。
		for _, line := range strings.Split(hl, "\n") {
			for _, wl := range strings.Split(wrapAnsi(line, avail), "\n") {
				buf.WriteString(r.th.fenceBG(prefix + wl))
				buf.WriteString("\n")
			}
		}
		buf.WriteString("\n")
		return
	}
	r.renderFencedPlain(buf, n, src, prefix, avail)
	buf.WriteString("\n")
}

// renderFencedPlain 中性铺色路径（无语言/无色层/中性缺省/降级——Task 7
// 形态保持）：轨前缀 + accent 内容，按可用宽换行——本仓 viewport 不软换，
// 放行长行会溢出破帧（参考放行，本仓收口，见文件头 adapted 注记）。围栏底
// 随 th.fenceBG 按槽位在场（中性缺省无 codeBlock 槽 → 与 Task 7 单一样式
// 契约同形）。
func (r *mdRenderer) renderFencedPlain(buf *strings.Builder, n ast.Node, src []byte, prefix string, avail int) {
	for i := range n.Lines().Len() {
		l := n.Lines().At(i)
		line := strings.TrimRight(string(l.Value(src)), "\n")
		for _, wl := range strings.Split(wrapAnsi(line, avail), "\n") {
			buf.WriteString(r.th.fenceBG(prefix + r.th.accent(wl)))
			buf.WriteString("\n")
		}
	}
}

// chromaFence 已知语言围栏经 chroma 高亮为 SGR 文本（多行）。门控：已知
// 语言（chroma 无 lexer 的语言降级铺色）+ 真实主题（中性缺省保持 Task 7
// 单一样式契约）+ 色彩层开启（tierNone 槽位全空——与 NO_COLOR 退避同路径，
// 不经 chroma）。整块 Tokenise（跨行字面量状态由 lexer 保持）→ formatter
// 直出 SGR。lexer/tokenise/format 任一失败返回 !ok——降级 renderFencedPlain
// （渲染层降级非治理收口：文本仍由原路径完整输出，不吞内容）。
func (r *mdRenderer) chromaFence(n ast.Node, src []byte) (string, bool) {
	lang := fenceLanguage(n, src)
	if lang == "" || r.th.name == neutralThemeName || r.th.accentColor == "" {
		return "", false
	}
	lexer := lexers.Get(lang)
	if lexer == nil || fenceChromaFormatter == nil {
		return "", false
	}
	var code strings.Builder
	for i := range n.Lines().Len() {
		l := n.Lines().At(i)
		code.Write(l.Value(src))
	}
	it, err := lexer.Tokenise(nil, code.String())
	if err != nil {
		return "", false
	}
	var b strings.Builder
	if fenceChromaFormatter.Format(&b, fenceChromaStyle(r.th.dark), it) != nil {
		return "", false
	}
	hl := strings.TrimRight(b.String(), "\n")
	if hl == "" {
		return "", false // 空围栏走铺色路径——不凭空画一条空轨
	}
	return hl, true
}

// fenceLanguage 围栏信息串的语言首词（小写）。indented CodeBlock 与无信息串
// 返回空；```go title=x 之类的附加参数取首词（GFM info 约定）。
func fenceLanguage(n ast.Node, src []byte) string {
	fb, ok := n.(*ast.FencedCodeBlock)
	if !ok || fb.Info == nil {
		return ""
	}
	info := string(fb.Info.Segment.Value(src))
	if i := strings.IndexAny(info, " \t"); i >= 0 {
		info = info[:i]
	}
	return strings.ToLower(strings.TrimSpace(info))
}

// neutralThemeName defaultTheme（theme.go）的身份名——chromaFence 以此识别
// Task 7 的无主题回退（该回退契约保持围栏单一样式）。neutral 与 buildTheme
// 的预设名空间不重叠：未知名被拒，用户主题不可能撞名。
const neutralThemeName = "neutral"

// fenceChromaFormatter 围栏高亮 formatter：terminal256 SGR 直出（真彩层亦然
// ——代码高亮在 256 层内色差不可辨，且与参考 diffview.go diffChromaFmt 同
// 选型；无色层在 chromaFence 门控处整路退避，不经此 formatter）。
var fenceChromaFormatter = formatters.Get("terminal256")

// fenceChromaStyle 围栏高亮的 chroma 样式（文档化裁定——对照参考 diffview.go
// activeDiffChromaStyle 的 GetForMode 按底色模式选型）：暗底主题族共用
// github-dark、亮底族共用同源 github（chroma 对 github 家族做 RegisterPair
// 双模解析——github-dark.xml counterpart="github"），不做逐预设定制（brief
// 允许族级共用；github 家族的克制配色与本仓槽位板同谱系）。每次围栏渲染期
// 解析：/theme 换肤后即时生效（对照参考「Resolve on each render」注记）。
func fenceChromaStyle(dark bool) *chroma.Style {
	mode := chroma.Dark
	if !dark {
		mode = chroma.Light
	}
	return styles.GetForMode("github-dark", mode)
}

// renderTable GFM 表格 → 对齐表格（对照参考 md.go:406 renderTable——Task 19
// 采纳）：dim 竖轨分隔、表头下 ─┼─ 规则线；列宽取各列最宽单元格的显示宽
// （CJK 计两列、SGR 零宽），整表超宽时按自然宽比例封顶（宽表不破帧），长
// 单元格换行不截断（整行膨胀到最高格）。对齐冒号（:---:）随解析读入但不
// 生效——左对齐（对照参考）。
func (r *mdRenderer) renderTable(buf *strings.Builder, n *extast.Table, src []byte, indent int) {
	var header []string
	var rows [][]string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch row := c.(type) {
		case *extast.TableHeader:
			header = r.collectCells(row, src)
		case *extast.TableRow:
			rows = append(rows, r.collectCells(row, src))
		}
	}
	if len(header) == 0 && len(rows) == 0 {
		return
	}
	cols := len(header)
	for _, row := range rows {
		if len(row) > cols {
			cols = len(row)
		}
	}
	if cols == 0 {
		return
	}

	// 初始列宽 = 各列最宽单元格的显示宽。
	widths := make([]int, cols)
	pick := func(i, w int) {
		if i < cols && w > widths[i] {
			widths[i] = w
		}
	}
	for i, h := range header {
		pick(i, ansi.StringWidth(h))
	}
	for _, row := range rows {
		for i, c := range row {
			pick(i, ansi.StringWidth(c))
		}
	}

	// 整表封顶：总预算 = 可用宽 - 竖轨（列间 3 列）。按自然宽比例分配——
	// 内容多的列保住更多空间；下限 3（对照参考；wrapAnsi 自身另有最小 4 的
	// 换行下限，极端窄宽下 CJK 格可能超帽 1-2 列，为参考同款边缘）。
	available := max(r.width-indent-3*(cols-1), cols*3)
	total := 0
	for _, w := range widths {
		total += w
	}
	if total > available {
		for i := range widths {
			widths[i] = max(widths[i]*available/total, 3)
		}
	}

	prefix := strings.Repeat(" ", indent)
	sep := r.th.dim(" │ ")
	if len(header) > 0 {
		r.renderTableRow(buf, prefix, sep, header, widths, true)
		buf.WriteString(prefix)
		for i := range widths {
			if i > 0 {
				buf.WriteString(r.th.dim("─┼─"))
			}
			buf.WriteString(r.th.dim(strings.Repeat("─", widths[i])))
		}
		buf.WriteByte('\n')
	}
	for _, row := range rows {
		r.renderTableRow(buf, prefix, sep, row, widths, false)
	}
	buf.WriteByte('\n')
}

// renderTableRow 逻辑行 → 视觉行：单元格各自按列宽换行（wrapAnsi 词换行 +
// CJK 硬断），行高 = 最长换行数；已尽内容的格补空格保持竖轨对齐。表头整格
// 加粗（含补位空白——SGR 零宽不影响列宽）。
func (r *mdRenderer) renderTableRow(buf *strings.Builder, prefix, sep string, cells []string, widths []int, isHeader bool) {
	cols := len(widths)
	wrapped := make([][]string, cols)
	maxLines := 1
	for i := range cols {
		var text string
		if i < len(cells) {
			text = cells[i]
		}
		wrapped[i] = strings.Split(wrapAnsi(text, widths[i]), "\n")
		if len(wrapped[i]) > maxLines {
			maxLines = len(wrapped[i])
		}
	}
	for line := range maxLines {
		buf.WriteString(prefix)
		for i := range cols {
			if i > 0 {
				buf.WriteString(sep)
			}
			var cell string
			if line < len(wrapped[i]) {
				cell = wrapped[i][line]
			}
			padded := padRight(cell, widths[i])
			if isHeader {
				padded = bold(padded)
			}
			buf.WriteString(padded)
		}
		buf.WriteByte('\n')
	}
}

// collectCells 遍历 TableHeader/TableRow 提取各 TableCell 的行内内容（ANSI
// 样式平文本）；非单元格子节点忽略（对照参考 md.go collectCells）。
func (r *mdRenderer) collectCells(parent ast.Node, src []byte) []string {
	var out []string
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if cell, ok := c.(*extast.TableCell); ok {
			out = append(out, strings.TrimSpace(r.collectInline(cell, src)))
		}
	}
	return out
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

// padRight 按显示宽右补空格至 w 列（CJK 计两列、SGR 零宽——fmt %-Ns 按字节
// 计数，含 CJK/SGR 时错列；对照参考 box.go padRight）。
func padRight(s string, w int) string {
	pad := w - ansi.StringWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
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
