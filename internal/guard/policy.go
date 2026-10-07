// Package guard 实现权限与钩子层（design §9）：Policy 纯函数裁决、
// Gate 审批队列与会话级授权、Hooks 回调分发。
// 红线：Decision 是稳定 JSON 词表；规则优先级 deny > ask > allow > fallback；
// 未知工具 fail-closed（Ask），绝不静默放行。
package guard

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// Decision 是策略裁决词表（JSON 稳定字符串）。ask 表示进入审批队列
// （Gate/Task 20），与事件层 allow|deny|timeout 的已裁决词表刻意区分。
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionAsk   Decision = "ask"
	DecisionDeny  Decision = "deny"
)

// Preset 是权限预设（design §9：default/acceptEdits/plan/danger-full）。
// 预设决定「无规则匹配时的 fallback」；显式规则恒压过预设。
type Preset string

const (
	PresetDefault     Preset = "default"
	PresetAcceptEdits Preset = "acceptEdits"
	PresetPlan        Preset = "plan"
	PresetDangerFull  Preset = "danger-full" // user-global 准入（config 锁定规则管进得来）
)

// Rule 是一条权限规则：工具名 + 可选 specifier + 效果。
// 形态 v1（design §9）：`Tool` 与 `Tool(specifier)`——specifier 语义只对
// Bash（command 前缀/精确）与文件族（路径 glob）有定义。
type Rule struct {
	Tool      string   // 工具名字面（如 "Bash"）
	Specifier string   // 可空；如 "go test:*"、"docs/**"
	Effect    Decision // allow|ask|deny
}

// ParseRule 解析规则字符串 "Tool" 或 "Tool(specifier)"。
func ParseRule(s string) (Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, fmt.Errorf("guard: empty rule")
	}
	open := strings.IndexByte(s, '(')
	if open < 0 {
		if strings.ContainsAny(s, " \t()") {
			return Rule{}, fmt.Errorf("guard: rule %q: tool name must not contain whitespace", s)
		}
		return Rule{Tool: s}, nil
	}
	if !strings.HasSuffix(s, ")") {
		return Rule{}, fmt.Errorf("guard: rule %q: unterminated specifier", s)
	}
	tool := strings.TrimSpace(s[:open])
	spec := strings.TrimSpace(s[open+1 : len(s)-1])
	if tool == "" || spec == "" {
		return Rule{}, fmt.Errorf("guard: rule %q: tool and specifier must be non-empty", s)
	}
	if strings.ContainsAny(tool, " \t()") {
		return Rule{}, fmt.Errorf("guard: rule %q: malformed tool name", s)
	}
	return Rule{Tool: tool, Specifier: spec}, nil
}

// Policy 是不可变权限策略：预设 fallback + 显式规则集。
type Policy struct {
	preset Preset
	rules  []Rule
}

// NewPolicy 构造策略（规则集不可变；构造期 fail-closed 校验）。
func NewPolicy(preset Preset, rules []Rule) (*Policy, error) {
	switch preset {
	case PresetDefault, PresetAcceptEdits, PresetPlan, PresetDangerFull:
	default:
		return nil, fmt.Errorf("guard: unknown preset %q", preset)
	}
	normalized := make([]Rule, len(rules))
	for i, r := range rules {
		if r.Tool == "" {
			return nil, fmt.Errorf("guard: rule %d: empty tool", i)
		}
		switch r.Effect {
		case DecisionAllow, DecisionAsk, DecisionDeny:
		default:
			return nil, fmt.Errorf("guard: rule %d (%s): invalid effect %q", i, r.Tool, r.Effect)
		}
		normalized[i] = r
	}
	return &Policy{preset: preset, rules: normalized}, nil
}

// Decide 裁决一次工具调用（纯函数）：收集全部匹配规则，按
// deny > ask > allow 取最高优先；无匹配 → 预设 fallback。
func (p *Policy) Decide(tool string, input json.RawMessage) Decision {
	matched := 0 // 位标：0=none 1=allow 2=ask 3=deny（数值即优先级）
	for _, r := range p.rules {
		if r.Tool == tool && ruleMatches(r, tool, input) {
			if pri := effectPriority(r.Effect); pri > matched {
				matched = pri
			}
		}
	}
	if matched > 0 {
		return decisionByPriority(matched)
	}
	return fallbackDecision(p.preset, tool)
}

func effectPriority(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	}
	return 0
}

func decisionByPriority(pri int) Decision {
	switch pri {
	case 3:
		return DecisionDeny
	case 2:
		return DecisionAsk
	case 1:
		return DecisionAllow
	}
	return DecisionAsk
}

// ruleMatches 判定单条规则是否命中本次调用。
func ruleMatches(r Rule, tool string, input json.RawMessage) bool {
	if r.Specifier == "" {
		return true // 裸工具规则：任意输入命中
	}
	switch tool {
	case "Bash":
		cmd := inputString(input, "command")
		if cmd == "" {
			return false
		}
		return bashSpecifierMatches(r.Specifier, cmd)
	case "Edit", "Write", "Read", "NotebookEdit", "MultiEdit":
		p := inputString(input, "file_path")
		if p == "" {
			p = inputString(input, "notebook_path")
		}
		if p == "" {
			return false
		}
		return globMatches(r.Specifier, p)
	default:
		return false // v1：其他工具无 specifier 语义（显式文档化，不匹配非报错）
	}
}

// bashSpecifierMatches：`:*` 后缀 = 前缀匹配，且**前缀必须终结于词边界**
// （其余部分为空或以空白起始——比 claude 的纯 HasPrefix 更保守：
// "go test:*" 不放行 "go testx"/"go test;rm" 的词面渗漏，fail-closed）；
// 无 `:*` = 整串精确匹配。
func bashSpecifierMatches(spec, cmd string) bool {
	if !strings.HasSuffix(spec, ":*") {
		return cmd == spec
	}
	prefix := strings.TrimSuffix(spec, ":*")
	if !strings.HasPrefix(cmd, prefix) {
		return false
	}
	rest := cmd[len(prefix):]
	return rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")
}

// inputString 提取 input 顶层字符串字段（畸形/缺失 → 空）。
func inputString(input json.RawMessage, key string) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// globCache 并发注记：Decide 目前只在 control 的串行路径被调用
// （会话内回合互斥，design §6；测试顺序执行）——首次命中时写入该 map 是
// 唯一的变异点。P2/P4 若出现并发 Decide（多入口/异步审批路径），必须先改
// sync.Map 或构造期预编译。
var globCache = map[string]*globMatcher{}

// globMatcher 是极简 glob（对齐 gitignore/claude 约定的子集）：
// `**` 跨段（含空），`*`/`?` 单段；段边界按 `/`。
type globMatcher struct {
	segments []segment
}

type segment struct {
	pattern string // 单段 pattern（"" = ** 吞并任意段）
	double  bool   // ** 段
}

// globMatches 报告 path 是否命中 pattern（Windows 反斜杠先规范化）。
func globMatches(pattern, p string) bool {
	if p == "" || pattern == "" {
		return false
	}
	p = strings.ReplaceAll(p, "\\", "/")
	m := globCache[pattern]
	if m == nil {
		m = compileGlob(pattern)
		globCache[pattern] = m
	}
	return m.match(strings.Split(p, "/"))
}

func compileGlob(pattern string) *globMatcher {
	parts := strings.Split(strings.ReplaceAll(pattern, "\\", "/"), "/")
	segs := make([]segment, 0, len(parts))
	for _, part := range parts {
		segs = append(segs, segment{pattern: part, double: part == "**"})
	}
	return &globMatcher{segments: segs}
}

// match 递归匹配：`**` 段可吞并零或多段；其余段用 path.Match 单段匹配。
func (m *globMatcher) match(rest []string) bool {
	if len(m.segments) == 0 {
		return len(rest) == 0
	}
	head := m.segments[0]
	tail := globMatcher{segments: m.segments[1:]}
	if head.double {
		// ** 吞并 0..n 段
		for i := 0; i <= len(rest); i++ {
			if tail.match(rest[i:]) {
				return true
			}
		}
		return false
	}
	if len(rest) == 0 {
		return false
	}
	ok, err := path.Match(head.pattern, rest[0])
	if err != nil || !ok {
		return false
	}
	return tail.match(rest[1:])
}

// ---- 工具分类与预设 fallback（§9 矩阵） ----

type toolClass int

const (
	classRead     toolClass = iota // 本地只读：Read/Glob/Grep
	classResearch                  // 网络只读：WebFetch/WebSearch（默认 Ask；plan 放行研究）
	classEdit                      // 文件写：Write/Edit/NotebookEdit/MultiEdit
	classExec                      // 执行：Bash
	classOther                     // 其余（Task/Agent/未知…）——fail-closed
)

func classifyTool(tool string) toolClass {
	switch tool {
	case "Read", "Glob", "Grep":
		return classRead
	case "WebFetch", "WebSearch":
		return classResearch
	case "Write", "Edit", "NotebookEdit", "MultiEdit":
		return classEdit
	case "Bash":
		return classExec
	default:
		return classOther
	}
}

// fallbackDecision 返回预设下该工具的基准裁决（无规则匹配时）。
// 矩阵（§9；research=网络只读）：
//
//	             read    research  edit    exec    other
//	default     allow   ask       ask     ask     ask
//	acceptEdits allow   ask       allow   ask     ask
//	plan        allow   allow     deny    deny    ask
//	danger-full allow   allow     allow   allow   allow
func fallbackDecision(preset Preset, tool string) Decision {
	class := classifyTool(tool)
	switch preset {
	case PresetDangerFull:
		return DecisionAllow
	case PresetPlan:
		switch class {
		case classRead, classResearch:
			return DecisionAllow
		case classEdit, classExec:
			return DecisionDeny
		default:
			return DecisionAsk
		}
	case PresetAcceptEdits:
		switch class {
		case classRead, classEdit:
			return DecisionAllow
		default:
			return DecisionAsk
		}
	default: // PresetDefault
		if class == classRead {
			return DecisionAllow
		}
		return DecisionAsk
	}
}
