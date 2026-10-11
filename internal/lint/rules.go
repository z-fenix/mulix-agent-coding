// rules.go——M17 分层规则表（design 001 §5.3 分层表 + design 002 修订）。
//
// 规则文本按 T6/C3 裁定修订为与已交付代码一致（brief Task 10 为准）：
//   - subagent 可 import event/kernel/engine/sessionx/cache/guard/memory，
//     不得 import control；
//   - tools/mulixmemory 可 import memory 与 registry；
//   - tools/mulixsubagent 不得 import 内核 internal 包——registry 除外
//     （RegisterConditional 契约签名所需）；
//   - cli 与 cmd 为组合根，可 import tools 子包。
//
// 矩阵 = design 001 §5.3 分层表 + 上述修订 + 各包已交付 import 实况
// （P2 全程保持分层干净——矩阵即现实，现实即矩阵）。未登记包按 leaf 处理
// （fail-closed：禁止一切内部导入；新增层/包须先在本文件登记并过评审）。
//
// 扫描口径：非测试 .go 文件。_test.go 不参与——测试合法 import 被测内核包
// （internal/e2e 先例），分层纪律只约束生产组合。
package lint

import "strings"

// layerMatrix 把包路径（模块名剥离后的相对路径）映射到允许的内部导入模式。
// 模式为精确路径（"internal/cache"）或尾部通配（"internal/*"——前缀匹配）。
// 缺席键 = leaf 缺省：禁止 import 任何本模块内部包。
var layerMatrix = map[string][]string{
	// 组合根（design 001 §5.3 第 1 条：只有 cli/serve/cmd 可 import control；
	// design 002 修订：cli 与 cmd 为组合根可 import tools 子包）。
	"cmd/*":          {"internal/*"},
	"internal/cli":   {"internal/*"},
	"internal/serve": {"internal/*"},

	// control：回合唯一驱动器（不得 import frontends——design 001 §5.3 第 2 条；
	// 现况 = cache/config/engine/event/guard/memory/sessionx/subagent）。
	"internal/control": {
		"internal/cache", "internal/config", "internal/engine", "internal/event",
		"internal/guard", "internal/memory", "internal/sessionx", "internal/subagent",
	},

	// subagent：T6/C3 修订文本（见文件头）；kernel 之上、control 之下的编排器。
	"internal/subagent": {
		"internal/event", "internal/kernel", "internal/engine",
		"internal/sessionx", "internal/cache", "internal/guard", "internal/memory",
	},

	// 内核层与 utility leaves（design 001 §5.3 第 3 条：禁止 import 任何内核包）。
	"internal/kernel":     {},
	"internal/eventbus":   {"internal/kernel"},
	"internal/llm":        {"internal/engine"},
	"internal/llm/claude": {"internal/engine", "internal/llm"},
	"internal/engine":     {},
	"internal/event":      {},
	"internal/jsonl":      {},
	"internal/textutil":   {},
	"internal/config":     {},

	// 中间层（现况矩阵：与已交付代码逐包核对一致）。
	"internal/memory":   {"internal/jsonl"},
	"internal/sessionx": {"internal/event", "internal/jsonl"},
	"internal/guard":    {"internal/config"},
	"internal/cache":    {"internal/config", "internal/jsonl"},
	"internal/registry": {"internal/cache"},

	// tools 子包（design 001 §5.3 第 4 条 + design 002 修订；P1 现况一并列全）。
	"internal/tools/mulixpermission": {"internal/registry"},
	"internal/tools/mulixsubagent":   {"internal/registry"},
	"internal/tools/mulixmemory":     {"internal/memory", "internal/registry"},
	"internal/tools/mulixusage":      {"internal/cache", "internal/registry"},
}

// refRule 是一条引用扫描规则：marker 字面量超出 allowlist 文件 → 违规
// （P1 guard.TestNoSecondSystemPromptPath 的行级先例规模化——design 002 §6 第 3 条）。
type refRule struct {
	Rule   string          // 违规规则名
	Marker string          // 引用标记（子串匹配）
	Reason string          // 违规细节（人读）
	Allow  map[string]bool // 合法引用文件（仓库相对正斜杠路径）
}

// refRules 是注入点唯一规则族：
//   - "--append-system-prompt"：P1 TestNoSecondSystemPromptPath 逐字并入
//     （flag 生产者唯一；allowlist 去 P1 测试文件——本工具不扫测试）。
//   - "AppendSystemPrompt"：标识符引用面（design 002 §6 第 3 条第一 scan）。
//   - "TurnTail"：user message 组装只经 control 的 turn-tail 函数
//     （withMemoryRecall 唯一调用点——design 002 §6 第 3 条第二 scan）。
var refRules = []refRule{
	{
		Rule:   "system-prompt-path",
		Marker: "--append-system-prompt",
		Reason: "引用 --append-system-prompt 超出 allowlist（生产者唯一——design 001 §5.3 第 6 条，P1 TestNoSecondSystemPromptPath 并入）",
		Allow: map[string]bool{
			"internal/cache/boot.go":        true, // 唯一生产者（组合 prompt 值）
			"internal/engine/host.go":       true, // BootConfig 契约字段文档
			"internal/engine/claudehost.go": true, // 唯一 argv 写入方
			"internal/lint/rules.go":        true, // 本规则定义处（M17 并入）
		},
	},
	{
		Rule:   "system-prompt-path",
		Marker: "AppendSystemPrompt",
		Reason: "引用 AppendSystemPrompt 超出 allowlist（system prompt 注入面唯一——design 002 §6 第 3 条）",
		Allow: map[string]bool{
			"internal/cache/boot.go":         true, // 生产者
			"internal/control/controller.go": true, // 回合组装拷贝 boot 工件
			"internal/engine/host.go":        true, // 契约字段
			"internal/engine/claudehost.go":  true, // argv 写入方
			"internal/lint/rules.go":         true, // 本规则定义处
		},
	},
	{
		Rule:   "turn-tail",
		Marker: "TurnTail",
		Reason: "引用 TurnTail 超出 allowlist（user message 组装只经 control 的 turn-tail 函数——design 002 §6 第 3 条）",
		Allow: map[string]bool{
			"internal/engine/host.go":        true, // UserMessage.TurnTail 字段定义
			"internal/engine/claudehost.go":  true, // wire 编码（消费，不自产）
			"internal/control/controller.go": true, // 唯一追加点（withMemoryRecall 调用点）
			"internal/lint/rules.go":         true, // 本规则定义处
		},
	},
}

// allowFor 解析 pkgPath 的允许清单：精确键优先，其后通配键（尾部 "/*"）
// 前缀匹配。未登记 → (nil, false)——按 leaf 处理（fail-closed）。
func allowFor(pkgPath string) ([]string, bool) {
	if allow, ok := layerMatrix[pkgPath]; ok {
		return allow, true
	}
	for pattern, allow := range layerMatrix {
		if suffix, found := strings.CutSuffix(pattern, "/*"); found &&
			strings.HasPrefix(pkgPath, suffix+"/") {
			return allow, true
		}
	}
	return nil, false
}

// pkgAllowed 报告 pkgPath 是否允许 import internalPkg（模块内相对路径）。
func pkgAllowed(pkgPath, internalPkg string) bool {
	allow, ok := allowFor(pkgPath)
	if !ok {
		return false // 未登记包按 leaf：fail-closed
	}
	for _, entry := range allow {
		if entry == internalPkg {
			return true
		}
		if suffix, found := strings.CutSuffix(entry, "/*"); found &&
			strings.HasPrefix(internalPkg, suffix+"/") {
			return true
		}
	}
	return false
}

// FlagAllowlistFor 返回指定 marker 的 allowlist 集合副本（T10-② 交叉断言
// 消费：guard 侧测试经此读取 lint 侧清单，双源单侧漂移在断言层暴露）。
// 未知 marker 返回 nil。
func FlagAllowlistFor(marker string) map[string]bool {
	for _, rr := range refRules {
		if rr.Marker == marker {
			out := make(map[string]bool, len(rr.Allow))
			for k, v := range rr.Allow {
				out[k] = v
			}
			return out
		}
	}
	return nil
}
