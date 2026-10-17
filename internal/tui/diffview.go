package tui

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/aymanbagabas/go-udiff"
	"github.com/charmbracelet/x/ansi"
)

// ---- diff 卡（023 D9/FR-005，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）internal/cli/diffview.go：
// 行号 gutter + 增删行色 + hunk 间 ⋮ 分隔 + tab 展开 + 按 path 选 chroma
// lexer（github-dark 家族，同 fenceChromaStyle）。adapted：
//  1. diff 源——参考取引擎结构化 FileDiff（unified 文本回解析 @@ 头）；mulix
//     事件无此载荷（D9 裁定：不改变引擎前提下的唯一口径），从
//     ToolUseRequestedEvent.Input 结构化字段提取 old/new 对经 go-udiff 现算。
//     行号为片段内相对行号（Input 不含文件全文与行偏移，无法换算绝对行号）。
//  2. 增删行色——参考暗绿/暗红背景条（硬编码 256 bg + reset 后重铺 bg）；
//     本仓换装 dsh diff 令牌（2026-10-17 重设计 Task 6）：整行铺 diff bg 预留
//     槽 + gutter/标记段 marker 前景（diffRow——槽值经 sgrBg/sgrFg 参数化，
//     真彩层喂 hex 同源）；marker 段收口的重置点回铺 bg（fenceBG 同法，帧出
//     口 paintBG 三元序同构）。chroma 语法着色仍落上下文行——+/- 行整行槽色
//     与 chroma 的 \x1b[0m reset 互踩的现状约束不变（参考靠重铺 bg 合成的是
//     chroma 段，本仓 +/- 行不喂 chroma）。
//  3. 折叠——参考 diffFoldLimit + /diff-fold；本仓渲染全量，折叠上限随
//     Ctrl+B 展开面在 Task 21 接管（brief）。
//  4. green 槽方法落本文件——theme.go 归属 T18/T19 文件清单，本任务不触
//     （T18 预留注记：green/cyan/magenta/fg/bg 待 diff 卡/选择高亮接入）。

// diffTabWidth tab 展开步长（对照参考 tabWidth——字面 tab 显示宽为 0 但终端
// 推进到制表位，不展开则截断点与实际宽度错位）。
const diffTabWidth = 4

// diffInput 可 diff 的工具输入载荷（diffFromInput 提取 → renderDiff 渲染）。
type diffInput struct {
	path   string // 目标路径（file_path/notebook_path，path 别名兜底）
	verb   string // 动词（toolVerb——改/写）
	before string // 旧内容（Write 整文件口径为空串——Input 不含旧文）
	after  string // 新内容
}

// diffFromInput 从工具 Input 结构化字段提取可 diff 对（D9）。工具/字段覆盖表
// （fixtures/engine/*.jsonl 无 Edit/Write/MultiEdit/NotebookEdit 实样——已核对，
// 字段名按 claude CLI 文档 schema）：
//
//	Edit         file_path + old_string/new_string（replace_all 语义对单对
//	             old/new 的视图无差——diff 只示变化本身）
//	Write        file_path + content（旧文不在 Input——整文件按纯新增口径）
//	MultiEdit    file_path + edits[{old_string,new_string}]（旧版 CLI 工具）
//	NotebookEdit notebook_path + old_source/new_source（insert/delete 模式
//	             缺侧即空侧，无需读 edit_mode）
//
// MultiEdit 链式口径：before = edits[0].old_string，after 从 edits[0].new_string
// 起依次 replace 后继对（后继 old 未命中即原样保留——净效果视图近似，引擎
// 语义出范围）。不可 diff（false，降级普通工具卡）：非法/空 JSON、非写类
// 工具、无路径、两侧全空、两侧相同（CLI 侧亦拒绝同串替换——防御同口径）。
func diffFromInput(tool string, input json.RawMessage) (diffInput, bool) {
	var p struct {
		Path      string `json:"file_path"`
		PathAlias string `json:"path"`
		Notebook  string `json:"notebook_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Content   string `json:"content"`
		OldSource string `json:"old_source"`
		NewSource string `json:"new_source"`
		Edits     []struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	if len(input) == 0 || json.Unmarshal(input, &p) != nil {
		return diffInput{}, false
	}
	path := p.Path
	if tool == "NotebookEdit" && p.Notebook != "" {
		path = p.Notebook
	}
	if path == "" {
		path = p.PathAlias
	}
	var d diffInput
	switch tool {
	case "Edit":
		d = diffInput{path: path, verb: toolVerb(tool), before: p.OldString, after: p.NewString}
	case "Write":
		d = diffInput{path: path, verb: toolVerb(tool), after: p.Content}
	case "MultiEdit":
		if len(p.Edits) == 0 {
			return diffInput{}, false
		}
		after := p.Edits[0].NewString
		for _, e := range p.Edits[1:] {
			after = strings.Replace(after, e.OldString, e.NewString, 1)
		}
		d = diffInput{path: path, verb: toolVerb(tool), before: p.Edits[0].OldString, after: after}
	case "NotebookEdit":
		d = diffInput{path: path, verb: toolVerb(tool), before: p.OldSource, after: p.NewSource}
	default:
		return diffInput{}, false
	}
	if d.path == "" || (d.before == "" && d.after == "") || d.before == d.after {
		return diffInput{}, false
	}
	return d, true
}

// diffHunks 统一 diff 计算（renderDiff 与 Task 14 变更聚合同源——同一
// ToUnifiedDiff 参数化不出第二算式）：D9 口径（before/after → Lines →
// ToUnifiedDiff，缺省上下文行）。失败防御 false（Lines 产物与内容自洽——
// 不可达；同 renderDiff 原 nil 降级，不出半成品）。
func diffHunks(d diffInput) ([]*udiff.Hunk, bool) {
	hunks, err := udiff.ToUnifiedDiff("", "", d.before, udiff.Lines(d.before, d.after), udiff.DefaultContextLines)
	if err != nil {
		return nil, false
	}
	return hunks.Hunks, true
}

// countDiffLines 增删行计数单源（renderDiff 头行 + Task 14 变更聚合共用——
// 不出第二计数器）。
func countDiffLines(hunks []*udiff.Hunk) (added, removed int) {
	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Insert:
				added++
			case udiff.Delete:
				removed++
			}
		}
	}
	return added, removed
}

// renderDiff diff 视图按宽渲染（块内挂载于工具卡头行之后）：✎ 动词 <path>
// +A -B 头 + 行号 hunk 体（增/删行 diff bg 槽整行 + marker 前景 gutter/标记
// 段——diffRow；上下文行 dim 行号 + chroma 代码着色——按 path 选 lexer；
// neutral 主题契约/无色层退避为平文，对照 chromaFence 门控同法）。hunk 间
// dim ⋮ 分隔（对照参考 diffBody）。行号取 hunk FromLine/ToLine（片段内相对
// 行号，见文件头注记 1）。全量渲染无折叠（Ctrl+B 展开面随 Task 21）。宽度
// <=0 取 80（对照 toolCard.render）。
func renderDiff(d diffInput, width int, th theme) []string {
	if width <= 0 {
		width = 80
	}
	hunks, ok := diffHunks(d)
	if !ok {
		return nil // Lines 产物与内容自洽——不可达；防御降级（不出半成品视图）
	}
	added, removed := countDiffLines(hunks)
	out := []string{diffHead(d, width, added, removed, th)}
	if len(hunks) == 0 {
		return out
	}
	gw := diffGutterWidth(hunks)
	avail := max(width-2-gw-3, 4) // 2 缩进 + gutter + 空格/符号/空格
	for hi, h := range hunks {
		if hi > 0 {
			out = append(out, "  "+th.dim("⋮"))
		}
		oldNo, newNo := h.FromLine, h.ToLine
		for _, l := range h.Lines {
			code := expandTabs(strings.TrimRight(l.Content, "\n"))
			switch l.Kind {
			case udiff.Insert:
				out = append(out, th.diffRow(th.diffAddedBGColor, th.diffAddedMarkerColor,
					"  "+lpad(strconv.Itoa(newNo), gw)+" + ", clampWidth(code, avail)))
				newNo++
			case udiff.Delete:
				out = append(out, th.diffRow(th.diffDeletedBGColor, th.diffDeletedMarkerColor,
					"  "+lpad(strconv.Itoa(oldNo), gw)+" - ", clampWidth(code, avail)))
				oldNo++
			default:
				out = append(out, "  "+th.dim(lpad(strconv.Itoa(newNo), gw))+"   "+diffCode(d.path, code, &th, avail))
				oldNo++
				newNo++
			}
		}
	}
	return out
}

// diffHead 头行：✎（accent）+ 动词（加粗）+ 路径（按余宽截断）+ +A -B 计数
// （零侧省略——对照参考 diffStat）。✎ 与 SGR 均零宽，余宽按显示宽计：
// 非路径固定段 = "✎ " + 动词 + 路径前导空格 + 计数段（段间 join 空格 n-1 +
// 计数前导空格 1）。
func diffHead(d diffInput, width, added, removed int, th theme) string {
	stat := make([]string, 0, 2)
	if added > 0 {
		stat = append(stat, th.green("+"+strconv.Itoa(added)))
	}
	if removed > 0 {
		stat = append(stat, th.red("-"+strconv.Itoa(removed)))
	}
	statW := len(stat) // 计数前导空格 1 + 段间 join 空格 n-1
	for _, s := range stat {
		statW += ansi.StringWidth(s)
	}
	fixed := 2 + ansi.StringWidth(d.verb) + statW + 1 // "✎ " + 动词 + 路径前导空格
	head := th.accent("✎") + " " + bold(d.verb) + " " + clampWidth(d.path, max(width-fixed, 4))
	if len(stat) > 0 {
		head += " " + strings.Join(stat, " ")
	}
	return head
}

// diffGutterWidth 行号列宽：各 hunk 旧/新侧行号上限的最大十进制位数，下限 2
// （对照参考 gutterWidth——一位行号补位对齐两位起）。
func diffGutterWidth(hunks []*udiff.Hunk) int {
	gw := 2
	pick := func(end int) {
		if end > 0 {
			if w := len(strconv.Itoa(end)); w > gw {
				gw = w
			}
		}
	}
	for _, h := range hunks {
		nOld, nNew := 0, 0
		for _, l := range h.Lines {
			switch l.Kind {
			case udiff.Delete:
				nOld++
			case udiff.Insert:
				nNew++
			default:
				nOld++
				nNew++
			}
		}
		pick(h.FromLine + nOld - 1)
		pick(h.ToLine + nNew - 1)
	}
	return gw
}

// diffCode 上下文行代码着色：先按余宽截断（SGR 前收口），再经 chroma 按 path
// 选 lexer 高亮（terminal256 直出；github-dark 家族按底色模式，同围栏高亮
// fenceChromaStyle/fenceChromaFormatter 复用）。门控：neutral 主题保持 Task 7
// 单一样式契约、无色层槽空退避、未知类型/词法/formatter 失败回落平文（渲染层
// 降级非治理收口：文本无损）。
func diffCode(path, code string, th *theme, avail int) string {
	code = clampWidth(code, avail)
	if code == "" || th.name == neutralThemeName || th.accentColor == "" {
		return code
	}
	lexer := lexers.Match(path)
	if lexer == nil {
		return code
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var b strings.Builder
	if fenceChromaFormatter.Format(&b, fenceChromaStyle(th.dark), it) != nil {
		return code
	}
	return strings.TrimRight(b.String(), "\n")
}

// lpad 左补空格至 w 位（gutter 对齐；对照参考 lpad）。
func lpad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return strings.Repeat(" ", w-len(s)) + s
}

// expandTabs tab 展开至下一 diffTabWidth 制表位（对照参考 expandTabs——
// 见 diffTabWidth 注记）。
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := diffTabWidth - col%diffTabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// green 绿槽包裹（T18 预留槽——theme.go 归属 T18/T19 文件清单，方法随 diff
// 卡任务落地，其余槽方法同构）。diff 体行已换装 diffRow（Task 6）；本方法现
// 消费点为 diff 头行 +A/-B 计数。
func (t *theme) green(s string) string { return sgrWrap(sgrFg(t.greenColor), s) }

// diffRow diff 增/删行换装（dsh diff 令牌——设计 §4 令牌映射表，重设计
// Task 6）：整行铺 diff bg 槽（暗绿/暗红背景条——对照参考「暗底背景条」口
// 径），gutter/标记段喂 marker 前景（行号与 +/- 标记同色），代码段经重置点
// 回铺 bg 落回默认前景压在色带上（fenceBG 同法：重置点即重铺；帧出口
// paintBG 在该重置点插帧底、diff 底后写生效——三元序由 layout_test.go
// TestFenceBGSurvivesFramePaint 钉死，本行产出同构）。gutter 须无内嵌 SGR
// （renderDiff 侧为纯文本拼接）。bg/marker 槽空（无色层/中性缺省）退避：
// bg 空时 marker 前景只上 gutter 段（双空在此原样返回——现状平文契约），
// marker 空时 bg 照铺整行（真实主题两槽同 tier 同步解析，单侧空为理论分支）。
func (t *theme) diffRow(bg, marker themeColor, gutter, code string) string {
	b, f := sgrBg(bg), sgrFg(marker)
	if b == "" {
		return sgrWrap(f, gutter) + code
	}
	if f == "" {
		return sgrWrap(b, gutter+code)
	}
	band := "\x1b[" + b + "m"
	return band + sgrWrap(f, gutter) + band + code + "\x1b[0m"
}
