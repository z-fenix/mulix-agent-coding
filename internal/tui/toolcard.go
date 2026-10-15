package tui

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"mulix/internal/event"
)

// ---- 工具卡（023 FR-005，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）internal/cli/toolcard.go：
// 卡片版式（状态符号 + 动词(主参) 头行、⎿ 连接槽）与「动词映射 + 主参键
// 提取」思路。adapted：
//  1. 工具名——参考是 snake_case 内建 id（read_file/bash…）；本仓引擎为
//     claude CLI，工具名是 Claude 风格（Read/Write/Edit/Bash/Glob/Grep/
//     mcp__*），动词映射按 brief 本地化为单字动词（读/写/改/运行/搜）。
//  2. 卡片形态——参考渲染无状态派发行（流式输出走独立 toolStream 槽，失败
//     打独立红行）；本仓一张语义卡随事件流转状态（运行中→完成/失败，同
//     ToolUseID upsert 原地改写），输出尾有界 ≤8 行驻留卡内（Ctrl+B 展开面
//     随 Task 21——尾窗只约束渲染，不约束驻留）。
//  3. 状态显式渲染（FR-005：运行/完成/失败状态）——运行中 dim「· 运行中」、
//     完成 dim「· 完成」、失败红卡「⊘ · 失败」+ 详情行。参考失败是头行内联
//     ⊘ errText；本仓详情独立成行——详情可能远长于头行余宽，截在头里不可读。
//  4. 失败详情取 Content 首个非空行（截断）；输出尾保尾 ≤8 行（对照参考
//     collapseToolOutput 的 dim 预览——整行弱化）。

const (
	// toolTailLines 输出尾行数上限（FR-005 有界尾；展开面随 Task 21 分离）。
	toolTailLines = 8
	// toolConnector 卡片连接槽（对照参考 toolcard.go:18 connector "  ⎿  "——
	// 本仓块模型无 2 列缩进，槽 = ⎿ + 2 空格）。
	toolConnector = "⎿  "
	// toolArgSummaryMax 缺省主参摘要的存储截断（渲染再按显示宽收口）。
	toolArgSummaryMax = 64
)

// toolState 工具卡状态（FR-005：运行/完成/失败）。
type toolState uint8

const (
	toolRunning toolState = iota
	toolDone
	toolFailed
)

// toolCard 工具卡：一张语义块随事件流转状态。ToolUseRequestedEvent 开卡
// （运行中），同 ToolUseID 的 ToolResultEvent 原地关卡（brief：同 ID upsert）。
// 指针驻块流——回执经模型改写后按块下标失效换行缓存。
type toolCard struct {
	toolUseID    string
	tool         string
	arg          string
	state        toolState
	outputTail   []string // ≤8 行有界尾（来自 ToolResult.Content）
	failedDetail string   // IsError 时的失败详情行
}

// toolArgKeys 各工具的主参 JSON 键（候选序——claude CLI 实况用 file_path，
// path/cmd 为别名兼容；对照参考 toolArgKey 映射——参考键是 snake_case 工具
// 集）。不在表内的工具（含 mcp__*）走缺省 JSON 摘要。
var toolArgKeys = map[string][]string{
	"Read":         {"file_path", "path"},
	"Write":        {"file_path", "path"},
	"Edit":         {"file_path", "path"},
	"NotebookEdit": {"notebook_path", "path"},
	"Bash":         {"command", "cmd"},
	"Glob":         {"pattern"},
	"Grep":         {"pattern"},
	"WebFetch":     {"url"},
	"WebSearch":    {"query"},
	"Task":         {"description", "prompt"},
}

// toolVerb 工具动词（brief：Read→读 Write→写 Edit→改 Bash→运行 Glob/Grep→搜；
// mcp__*→短名——对照参考 toolDisplayName 的 MCP 短名路径；未映射原名兜底）。
func toolVerb(tool string) string {
	switch tool {
	case "Read":
		return "读"
	case "Write":
		return "写"
	case "Edit":
		return "改"
	case "Bash":
		return "运行"
	case "Glob", "Grep":
		return "搜"
	}
	if short, ok := mcpShortName(tool); ok {
		return short
	}
	return tool
}

// mcpShortName MCP 工具短名：mcp__server__tool → tool（对照参考
// tool.SplitMCPName——server 段丢弃取短名）；无第二段时余部即短名。
func mcpShortName(tool string) (string, bool) {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return "", false
	}
	if _, short, found := strings.Cut(rest, "__"); found && short != "" {
		return short, true
	}
	return rest, true
}

// toolArgKey 主参数（brief：path/cmd/query…缺省 JSON 摘要截断）：按候选键
// 提取标量主参；未知工具/缺键/非法 JSON 退化为压缩单行摘要（存储截断）。
func toolArgKey(tool string, input json.RawMessage) string {
	var m map[string]any
	if len(input) > 0 && json.Unmarshal(input, &m) == nil {
		for _, k := range toolArgKeys[tool] {
			if s := argScalar(m[k]); s != "" {
				return s
			}
		}
	}
	return jsonSummary(input)
}

// argScalar 主参值标量化（对照参考 toolArg 的 switch：字符串 TrimSpace、
// 数组 join、数字整数化；其余不可读退空——回落摘要）。
func argScalar(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		return ""
	}
}

// jsonSummary 缺省主参：JSON 压缩单行 + 截断（未知工具/缺主参键的可辨认
// 摘要；非法 JSON 原文截断——不吞内容）。
func jsonSummary(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, input) != nil {
		buf.Reset()
		buf.Write(input)
	}
	return clampWidth(buf.String(), toolArgSummaryMax)
}

// setResult 回执落卡：IsError → 失败态 + 详情行；输出尾一律保尾 ≤8 行
// （失败卡渲染只出详情行，尾驻卡供 Task 21 展开面）。
func (c *toolCard) setResult(e event.ToolResultEvent) {
	if e.IsError {
		c.state = toolFailed
		c.failedDetail = failureDetail(e.Content)
	} else {
		c.state = toolDone
	}
	c.outputTail = boundedTail(e.Content, toolTailLines)
}

// failureDetail 失败详情行：Content 首个非空行（截断——详情是摘要行，
// 全量在输出尾/模型侧）。
func failureDetail(content string) string {
	for ln := range strings.SplitSeq(content, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return clampWidth(t, 200)
		}
	}
	return ""
}

// boundedTail 有界输出尾（保尾，对照参考 thinking 尾窗同法）：修剪尾空行后
// 取最后 maxLines 行；空内容 nil。
func boundedTail(s string, maxLines int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

// render 工具卡按宽渲染（块接口）：头行 ⏺/⊘ 动词(主参) · 状态 + ⎿ 连接槽
// 输出尾（完成卡）/失败详情行（失败卡）。对照参考 toolCard/toolHead 的头行
// 组装——动词加粗、主参裸文（括号弱化）、按余宽截断。
func (c *toolCard) render(width int) []string {
	if width <= 0 {
		width = 80
	}
	lines := []string{c.header(width)}
	if c.state == toolFailed {
		if c.failedDetail != "" {
			lines = append(lines, failLine(toolConnector+c.failedDetail))
		}
		return lines
	}
	lines = append(lines, c.tail(width)...)
	return lines
}

// header 卡头行：状态符号 + 加粗动词 + 弱化括号主参（按显示宽余量截断）+
// 弱化/红色状态尾注。
func (c *toolCard) header(width int) string {
	verb := toolVerb(c.tool)
	glyph, status := accentLine("⏺"), ""
	switch c.state {
	case toolRunning:
		status = dimLine(" · 运行中")
	case toolFailed:
		glyph, status = failLine("⊘"), failLine(" · 失败")
	default:
		status = dimLine(" · 完成")
	}
	head := glyph + " " + bold(verb)
	if c.arg != "" {
		fixed := ansi.StringWidth(head) + ansi.StringWidth(status) + 2 // 2 = 括号
		head += dimLine("(") + clampWidth(c.arg, max(width-fixed, 4)) + dimLine(")")
	}
	return head + status
}

// tail 连接槽输出尾：首行携 ⎿ 槽、续行按槽宽对齐（对照参考
// renderConnectorBlock）；整行弱化（对照参考 collapseToolOutput 的 dim
// 预览），逐行按余宽截断。
func (c *toolCard) tail(width int) []string {
	if len(c.outputTail) == 0 {
		return nil
	}
	slot := ansi.StringWidth(toolConnector)
	indent := strings.Repeat(" ", slot)
	avail := max(width-slot, 4)
	out := make([]string, 0, len(c.outputTail))
	for i, l := range c.outputTail {
		prefix := indent
		if i == 0 {
			prefix = toolConnector
		}
		out = append(out, dimLine(prefix+clampWidth(l, avail)))
	}
	return out
}

// accentLine 工具卡运行/完成态符号（对照参考 toolDot 的类别色——本仓中性
// 单色，类别配色随 Task 18 主题系统）。
func accentLine(s string) string { return sgrWrap("36", s) }

// failLine 失败卡红样式（FR-005：失败卡显著标红）。
func failLine(s string) string { return sgrWrap("31", s) }

// clampWidth 按显示宽截断（SGR 零宽计列、全角计两列；超宽补省略号——对照
// 参考 clampPlain 的按宽收口，经 x/ansi 保持宽度感知）。
func clampWidth(s string, w int) string {
	if w < 1 {
		w = 1
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// ---- 模型接线（brief：ToolUseRequestedEvent/ToolResultEvent）----

// openToolCard 开运行中卡（ToolUseRequestedEvent）：主参已提取，卡驻块流尾，
// 索引记 toolUseID → 块下标（回执按同 ID upsert）。
func (m *tuiModel) openToolCard(e event.ToolUseRequestedEvent) {
	m.transcriptState.append(&toolCard{
		toolUseID: e.ToolUseID,
		tool:      e.Tool,
		arg:       toolArgKey(e.Tool, e.Input),
		state:     toolRunning,
	})
	if m.cards == nil {
		m.cards = make(map[string]int)
	}
	m.cards[e.ToolUseID] = len(m.transcriptState.blocks) - 1
}

// closeToolCard 回执关卡（ToolResultEvent，同 ToolUseID upsert）：原卡改写
// 状态/输出尾/详情，失效该块起的换行缓存（运行中槽位的原地改写——缓存契约
// 的「工具尾」路径，见 wrapcache.go invalidateWrapFrom）。索引失配（换绑
// 残渍等）或孤儿回执（请求事件缺席——重放边界）退化为合成新卡留痕，失败
// 不静默（ToolResultEvent 无工具名——请求缺席时名不可考）。
func (m *tuiModel) closeToolCard(e event.ToolResultEvent) {
	if idx, ok := m.cards[e.ToolUseID]; ok {
		if card, isCard := m.transcriptState.blocks[idx].(*toolCard); isCard && card.toolUseID == e.ToolUseID {
			card.setResult(e)
			m.transcriptState.invalidateWrapFrom(idx)
			return
		}
	}
	card := &toolCard{toolUseID: e.ToolUseID, tool: "未知工具", state: toolRunning}
	card.setResult(e)
	m.transcriptState.append(card)
	if m.cards == nil {
		m.cards = make(map[string]int)
	}
	m.cards[e.ToolUseID] = len(m.transcriptState.blocks) - 1
}
