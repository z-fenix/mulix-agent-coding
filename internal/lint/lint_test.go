package lint

// lint_test.go——M17 分层检查工具的测试矩阵（brief Task 10 Step 1 ①-⑤）：
// ①分层导入矩阵（leaf→kernel）②tools 子包反向 import 内核包 ③棘轮（增→违规；
// 减/平→通过）④注入点唯一（AppendSystemPrompt / TurnTail 引用超出 allowlist）
// ⑤干净树零违规。fixture 树写在 t.TempDir()（在模块树之外，go 工具链不可见），
// 引擎只做文本行扫描、不编译——import 指向的包目录可以不存在。

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// writeTree 在 dir 下落一组文本文件（key 为正斜杠相对路径）。
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// findViolations 按规则名过滤违规。
func findViolations(vs []Violation, rule string) []Violation {
	var out []Violation
	for _, v := range vs {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

// ① 分层导入矩阵：leaf→kernel import → Violation(Rule="layer-import")。
func TestLayerImportLeafToKernel(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":      "module fixture\n\ngo 1.26\n",
		"kernel/k.go": "package kernel\n",
		"leaf/a.go":   "package leaf\n\nimport _ \"fixture/kernel\"\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("violations = %#v, want exactly 1", vs)
	}
	v := vs[0]
	if v.File != "leaf/a.go" || v.Line != 3 || v.Rule != "layer-import" {
		t.Fatalf("violation = %#v, want {leaf/a.go 3 layer-import}", v)
	}
	if !strings.Contains(v.Detail, "fixture/kernel") {
		t.Fatalf("detail = %q, want mention of fixture/kernel", v.Detail)
	}
}

// ② tools 子包 import 内核包 → Violation（mulix 形状：未登记 tools 包按 leaf）。
func TestLayerImportToolsSubpackageToKernel(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                       "module mulix\n",
		"internal/kernel/k.go":         "package kernel\n",
		"internal/tools/faketool/t.go": "package faketool\n\nimport _ \"mulix/internal/kernel\"\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 1 || vs[0].File != "internal/tools/faketool/t.go" ||
		vs[0].Line != 3 || vs[0].Rule != "layer-import" {
		t.Fatalf("violations = %#v, want 1 layer-import at faketool/t.go:3", vs)
	}
}

// 前向约束（brief 修订文本）：subagent 不得 import control。
func TestSubagentMustNotImportControl(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                   "module mulix\n",
		"internal/control/stub.go": "package control\n",
		"internal/subagent/s.go":   "package subagent\n\nimport _ \"mulix/internal/control\"\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 1 || vs[0].Rule != "layer-import" || vs[0].File != "internal/subagent/s.go" {
		t.Fatalf("violations = %#v, want subagent→control layer-import", vs)
	}
	if !strings.Contains(vs[0].Detail, "mulix/internal/control") {
		t.Fatalf("detail = %q, want mention of mulix/internal/control", vs[0].Detail)
	}
}

// design 001 §5.3 第 2 条：control 不得 import frontends。
func TestControlMustNotImportFrontends(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                "module mulix\n",
		"internal/cli/stub.go":  "package cli\n",
		"internal/control/c.go": "package control\n\nimport _ \"mulix/internal/cli\"\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 1 || vs[0].Rule != "layer-import" || vs[0].File != "internal/control/c.go" {
		t.Fatalf("violations = %#v, want control→cli layer-import", vs)
	}
}

// 正向钉：subagent 可 import event/kernel（brief 修订文本允许面）。
func TestSubagentKernelEventAllowed(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                 "module mulix\n",
		"internal/kernel/k.go":   "package kernel\n",
		"internal/event/e.go":    "package event\n",
		"internal/subagent/s.go": "package subagent\n\nimport (\n\t_ \"mulix/internal/event\"\n\t_ \"mulix/internal/kernel\"\n)\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("violations = %#v, want zero (subagent→event/kernel 允许)", vs)
	}
}

// ③ 棘轮：计数增 → Violation(Rule="ratchet")；减/平 → 通过。
func TestRatchetIncreaseFailsDecreaseEqualPass(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":   "module mulix\n",
		"app/a.go": "package app\n\nvar ref1 = \"AppendSystemPrompt\"\n\nvar ref2 = \"AppendSystemPrompt\"\n",
	})
	ratchet := filepath.Join(root, "ratchet.json")

	// 平（2/2）与降（2/3）→ 通过（基线内既有债务不再报）。
	for _, allowed := range []int{2, 3} {
		writeTree(t, root, map[string]string{
			"ratchet.json": "{\"app/a.go\": " + strconv.Itoa(allowed) + "}\n",
		})
		vs, err := RunLayers(root, ratchet)
		if err != nil {
			t.Fatalf("RunLayers(allowed=%d): %v", allowed, err)
		}
		if len(vs) != 0 {
			t.Fatalf("allowed=%d: violations = %#v, want zero (减/平 → 通过)", allowed, vs)
		}
	}

	// 增（2 项 > 基线 1）→ 2 条原始违规 + 1 条 ratchet。
	writeTree(t, root, map[string]string{"ratchet.json": "{\"app/a.go\": 1}\n"})
	vs, err := RunLayers(root, ratchet)
	if err != nil {
		t.Fatalf("RunLayers(increase): %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("violations = %#v, want 2 raw + 1 ratchet", vs)
	}
	ratchets := findViolations(vs, "ratchet")
	if len(ratchets) != 1 {
		t.Fatalf("ratchet violations = %#v, want exactly 1", ratchets)
	}
	if ratchets[0].File != "app/a.go" || ratchets[0].Line != 3 {
		t.Fatalf("ratchet violation = %#v, want {app/a.go 3}", ratchets[0])
	}
	if !strings.Contains(ratchets[0].Detail, "2") || !strings.Contains(ratchets[0].Detail, "1") {
		t.Fatalf("ratchet detail = %q, want counts 2 > 1", ratchets[0].Detail)
	}

	// 空基线 {}：基线未登记 → 任何违规都算棘轮突破（2 原始 + 1 ratchet）。
	writeTree(t, root, map[string]string{"ratchet.json": "{}\n"})
	vs, err = RunLayers(root, ratchet)
	if err != nil {
		t.Fatalf("RunLayers(empty baseline): %v", err)
	}
	if len(vs) != 3 || len(findViolations(vs, "ratchet")) != 1 {
		t.Fatalf("violations = %#v, want 2 raw + 1 ratchet with empty baseline", vs)
	}

	// 棘轮文件缺席 → 棘轮不启用，只报原始违规（不发明 ratchet 违规）。
	if err := os.Remove(ratchet); err != nil {
		t.Fatalf("remove ratchet: %v", err)
	}
	vs, err = RunLayers(root, ratchet)
	if err != nil {
		t.Fatalf("RunLayers(no baseline file): %v", err)
	}
	if len(vs) != 2 || len(findViolations(vs, "ratchet")) != 0 {
		t.Fatalf("violations = %#v, want 2 raw, no ratchet (文件缺席→棘轮不启用)", vs)
	}
}

// 棘轮基线 JSON 非法 → error（fail-closed，不静默当空基线）。
func TestRatchetMalformedJSONIsError(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":       "module mulix\n",
		"ratchet.json": "{not json",
	})
	if _, err := RunLayers(root, filepath.Join(root, "ratchet.json")); err == nil {
		t.Fatal("RunLayers with malformed ratchet.json = nil error, want error")
	}
}

// ④ turn-tail / 注入点唯一：AppendSystemPrompt 与 --append-system-prompt 引用
// 超出 allowlist → Violation(Rule="system-prompt-path")（P1 TestNoSecondSystemPromptPath
// 规则并入；allowlist 文件内引用合法）。
func TestSystemPromptPathReferenceOutsideAllowlist(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module mulix\n",
		// allowlist 内：唯一生产者 boot.go——两个标记都合法。
		"internal/cache/boot.go": "package cache\n\nvar y = \"--append-system-prompt\"\n",
		// allowlist 外：标识符引用 → 违规。
		"internal/engine/other.go": "package engine\n\nvar x = \"AppendSystemPrompt\"\n",
		// allowlist 外：flag 字面量引用 → 违规。
		"internal/other/o.go": "package other\n\nvar z = \"--append-system-prompt\"\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("violations = %#v, want exactly 2", vs)
	}
	for _, v := range vs {
		if v.Rule != "system-prompt-path" {
			t.Fatalf("rule = %q, want system-prompt-path", v.Rule)
		}
		if v.Line != 3 {
			t.Fatalf("line = %d, want 3 (%#v)", v.Line, v)
		}
	}
	files := map[string]bool{vs[0].File: true, vs[1].File: true}
	if !files["internal/engine/other.go"] || !files["internal/other/o.go"] {
		t.Fatalf("files = %v, want engine/other.go + other/o.go", files)
	}
}

// design 002 §6 第 3 条：user message 组装只经 control 的 turn-tail 函数——
// TurnTail 引用超出 allowlist → Violation(Rule="turn-tail")。
func TestTurnTailReferenceOutsideAllowlist(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module mulix\n",
		// allowlist 内：字段定义（engine）、wire 编码（engine）、唯一追加点（control）。
		"internal/engine/host.go":        "package engine\n\ntype userMessage struct{ TurnTail []string }\n",
		"internal/control/controller.go": "package control\n\nfunc f(m *userMessage) { m.TurnTail = append(m.TurnTail, \"x\") }\n",
		// allowlist 外：cli 直接组装 turn-tail → 违规。
		"internal/cli/x.go": "package cli\n\nvar t = msg.TurnTail\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 1 || vs[0].Rule != "turn-tail" || vs[0].File != "internal/cli/x.go" || vs[0].Line != 3 {
		t.Fatalf("violations = %#v, want 1 turn-tail at cli/x.go:3", vs)
	}
}

// ⑤ 干净树 → 零违规（nil error；mulix 形状全层正向矩阵）。
func TestCleanTreeZeroViolations(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                         "module mulix\n",
		"internal/kernel/k.go":           "package kernel\n",
		"internal/event/e.go":            "package event\n",
		"internal/jsonl/j.go":            "package jsonl\n",
		"internal/config/c.go":           "package config\n",
		"internal/cache/boot.go":         "package cache\n\nimport (\n\t_ \"mulix/internal/config\"\n\t_ \"mulix/internal/jsonl\"\n)\n",
		"internal/memory/m.go":           "package memory\n\nimport _ \"mulix/internal/jsonl\"\n",
		"internal/sessionx/s.go":         "package sessionx\n\nimport (\n\t_ \"mulix/internal/event\"\n\t_ \"mulix/internal/jsonl\"\n)\n",
		"internal/guard/g.go":            "package guard\n\nimport _ \"mulix/internal/config\"\n",
		"internal/registry/r.go":         "package registry\n\nimport _ \"mulix/internal/cache\"\n",
		"internal/subagent/s.go":         "package subagent\n\nimport (\n\t_ \"mulix/internal/event\"\n\t_ \"mulix/internal/kernel\"\n)\n",
		"internal/control/ctl.go":        "package control\n\nimport (\n\t_ \"mulix/internal/cache\"\n\t_ \"mulix/internal/config\"\n\t_ \"mulix/internal/engine\"\n\t_ \"mulix/internal/event\"\n\t_ \"mulix/internal/guard\"\n\t_ \"mulix/internal/memory\"\n\t_ \"mulix/internal/sessionx\"\n\t_ \"mulix/internal/subagent\"\n)\n",
		"internal/cli/ui.go":             "package cli\n\nimport (\n\t_ \"mulix/internal/control\"\n\t_ \"mulix/internal/registry\"\n\t_ \"mulix/internal/tools/mulixusage\"\n)\n",
		"internal/tools/mulixusage/u.go": "package mulixusage\n\nimport (\n\t_ \"mulix/internal/cache\"\n\t_ \"mulix/internal/registry\"\n)\n",
		"cmd/mulix-cli/main.go":          "package main\n\nimport (\n\t_ \"mulix/internal/cli\"\n\t_ \"mulix/internal/tools/mulixusage\"\n)\n",
	})
	for _, ratchet := range []string{"", filepath.Join(root, "ratchet.json")} {
		vs, err := RunLayers(root, ratchet)
		if err != nil {
			t.Fatalf("RunLayers(ratchet=%q): %v", ratchet, err)
		}
		if len(vs) != 0 {
			t.Fatalf("violations = %#v, want zero (干净树)", vs)
		}
	}
}

// import 扫描形态：单行 import、具名 import、空白 import、块内注释与空行。
func TestImportScanForms(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod": "module fixture\n",
		// 行号：1 package / 2 空 / 3 单行 / 4 空 / 5 import( / 6 注释 / 7 具名 / 8 空 / 9 空白 / 10 )
		"leaf/forms.go": "package leaf\n\nimport \"fixture/kernel\"\n\nimport (\n\t// 前置注释\n\tk \"fixture/kernel2\"\n\n\t_ \"fixture/kernel3\"\n)\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("violations = %#v, want 3 import violations", vs)
	}
	wantLines := map[int]bool{3: true, 7: true, 9: true}
	for _, v := range vs {
		if v.Rule != "layer-import" || v.File != "leaf/forms.go" || !wantLines[v.Line] {
			t.Fatalf("violation = %#v, want layer-import at forms.go lines 3/7/9", v)
		}
	}
}

// repoRoot 缺 go.mod → error（lint layers 需在模块根运行）。
func TestMissingGoModIsError(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"main.go": "package main\n"})
	if _, err := RunLayers(root, ""); err == nil {
		t.Fatal("RunLayers without go.mod = nil error, want error")
	}
}

// _test.go 不参与扫描：测试文件合法 import 被测内核包（e2e 先例），不报违规。
func TestTestFilesAreSkipped(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":         "module mulix\n",
		"leaf/a_test.go": "package leaf\n\nimport _ \"mulix/internal/kernel\"\n\nvar ref = \"AppendSystemPrompt\"\n",
	})
	vs, err := RunLayers(root, "")
	if err != nil {
		t.Fatalf("RunLayers: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("violations = %#v, want zero (_test.go 跳过)", vs)
	}
}

// GenerateRatchet：首轮基线生成（自食其力）——写 {file: count}，再跑 RunLayers
// 在基线内通过；干净树 → "{}"。
func TestGenerateRatchet(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":     "module mulix\n",
		"bad/a.go":   "package bad\n\nimport _ \"mulix/internal/kernel\"\n",
		"app/b.go":   "package app\n\nvar r = \"AppendSystemPrompt\"\n",
		"clean/c.go": "package clean\n",
	})
	ratchet := filepath.Join(root, "ratchet.json")
	raw, err := GenerateRatchet(root, ratchet)
	if err != nil {
		t.Fatalf("GenerateRatchet: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("raw violations = %#v, want 2", raw)
	}
	data, err := os.ReadFile(ratchet)
	if err != nil {
		t.Fatalf("read ratchet: %v", err)
	}
	// encoding/json 对 map 键排序输出（确定性入库面）。
	if want := "{\n  \"app/b.go\": 1,\n  \"bad/a.go\": 1\n}\n"; string(data) != want {
		t.Fatalf("ratchet.json = %q, want %q", string(data), want)
	}
	vs, err := RunLayers(root, ratchet)
	if err != nil {
		t.Fatalf("RunLayers after fix: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("violations = %#v, want zero (基线内)", vs)
	}

	// 干净树 → "{}"。
	clean := t.TempDir()
	writeTree(t, clean, map[string]string{
		"go.mod":     "module mulix\n",
		"clean/c.go": "package clean\n",
	})
	cleanRatchet := filepath.Join(clean, "ratchet.json")
	if _, err := GenerateRatchet(clean, cleanRatchet); err != nil {
		t.Fatalf("GenerateRatchet(clean): %v", err)
	}
	data, err = os.ReadFile(cleanRatchet)
	if err != nil {
		t.Fatalf("read clean ratchet: %v", err)
	}
	if string(data) != "{}\n" {
		t.Fatalf("clean ratchet.json = %q, want \"{}\\n\"", string(data))
	}
}

// GenerateRatchet 只紧不松：既有更高计数保留（max-merge）。
func TestGenerateRatchetOnlyTightens(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":       "module mulix\n",
		"app/a.go":     "package app\n\nvar r = \"AppendSystemPrompt\"\n",
		"ratchet.json": "{\n  \"app/a.go\": 5\n}\n",
	})
	ratchet := filepath.Join(root, "ratchet.json")
	if _, err := GenerateRatchet(root, ratchet); err != nil {
		t.Fatalf("GenerateRatchet: %v", err)
	}
	data, err := os.ReadFile(ratchet)
	if err != nil {
		t.Fatalf("read ratchet: %v", err)
	}
	if want := "{\n  \"app/a.go\": 5\n}\n"; string(data) != want {
		t.Fatalf("ratchet.json = %q, want %q (既有计数 5 不得放松为 1)", string(data), want)
	}
}

// TestSplitLineCommentContract 直钉 splitLineComment 契约(009 修复轮:
// 块注释跳过必须落在收尾之后——off-by-one 会让收尾的 / 泄漏进代码段,
// 且整行块注释的 wholeComment 契约被破坏)。
func TestSplitLineCommentContract(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		code         string
		wholeComment bool
	}{
		{name: "整行块注释已终止", line: "	/* c */", code: "", wholeComment: true},
		{name: "行内块注释终止后接代码", line: "/* c */ \"internal/x\"", code: " \"internal/x\"", wholeComment: false},
		{name: "相邻块注释后接代码", line: "/*a*//*b*/ \"internal/x\"", code: " \"internal/x\"", wholeComment: false},
		{name: "块注释未终止", line: "	/* 开启", code: "", wholeComment: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, whole := splitLineComment(tc.line)
			if code != tc.code || whole != tc.wholeComment {
				t.Fatalf("splitLineComment(%q) = (%q, %v), want (%q, %v)", tc.line, code, whole, tc.code, tc.wholeComment)
			}
		})
	}
}

// TestScanImportsCommentEdges 钉 T10-①(008 FR-101):import 块内注释行
// 既不假闭合也不假 import——扫描结果与剥离注释的等价文件完全一致。
func TestScanImportsCommentEdges(t *testing.T) {
	cases := []struct {
		name  string
		lines []string // 原始文件行
		want  []string // 期望 import 路径(按序)
	}{
		{
			name: "注释含右括号不假闭合",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"",
				"\t// internal/config) 这是注释", "\t\"internal/jsonl\"", ")",
			},
			want: []string{"internal/cache", "internal/jsonl"},
		},
		{
			name: "注释内引号串不假 import",
			lines: []string{
				"package p", "import (", "\t// 见 \"internal/guard\" 文档", "\t\"internal/cache\"", ")",
			},
			want: []string{"internal/cache"},
		},
		{
			name: "行尾注释不干扰",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\" // cache: 记账", ")",
			},
			want: []string{"internal/cache"},
		},
		{
			name: "右括号后行尾注释正常闭合",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"", ") // 分层矩阵收口", "var _ = 1",
			},
			want: []string{"internal/cache"},
		},
		{
			name: "单行块注释不假闭合",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"", "\t/* internal/config) 历史说明 */", "\t\"internal/jsonl\"", ")",
			},
			want: []string{"internal/cache", "internal/jsonl"},
		},
		{
			name: "多行块注释跨行不假闭合不假 import",
			lines: []string{
				"package p", "import (", "\t\"internal/cache\"", "\t/* 历史说明开始",
				"\t\"internal/guard\" 引用于旧版", "\t结束 */", "\t\"internal/jsonl\"", ")",
			},
			want: []string{"internal/cache", "internal/jsonl"},
		},
		{
			name: "行内块注释终止后接代码(008 终审失败形态)",
			lines: []string{
				"package p", "import (", "	/* c */ \"internal/x\"", ")", "var _ = 1",
			},
			want: []string{"internal/x"},
		},
		{
			name: "行内块注释前后均有代码",
			lines: []string{
				"package p", "import (", "	\"internal/cache\" /* 历史注 */ \"internal/y\"", ")", "var _ = 2",
			},
			want: []string{"internal/cache", "internal/y"},
		},
		{
			name: "相邻块注释后接代码不假阴性",
			lines: []string{
				"package p", "import (", "	/*a*//*b*/ \"internal/x\"", ")", "var _ = 3",
			},
			want: []string{"internal/x"},
		},
		{
			name: "整行块注释含引号串不假 import",
			lines: []string{
				"package p", "import (", "	/* 见 \"internal/guard\" 文档 */", "	\"internal/cache\"", ")",
			},
			want: []string{"internal/cache"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := scanImports(tc.lines)
			if err != nil {
				t.Fatalf("scanImports: %v", err)
			}
			var got []string
			for _, r := range refs {
				got = append(got, r.Path)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("imports = %v, want %v", got, tc.want)
			}
		})
	}
}

// 023 重构前置钉（Task 1）：internal/tui 须登记为组合根级——缺席时
// fail-closed leaf 缺省会静默禁止一切内部导入，后续迁移任务无从落码。
func TestLayerMatrixHasTUI(t *testing.T) {
	allow, ok := allowFor("internal/tui")
	if !ok {
		t.Fatal("internal/tui 未登记 layerMatrix（fail-closed leaf 缺省会静默禁止一切内部导入）")
	}
	_ = allow // 清单内容由下方 pkgAllowed 断言（与 allowFor 同源；brief 原文未消费 allow 编译不过）
	if !pkgAllowed("internal/tui", "internal/control") {
		t.Fatal("internal/tui 应为组合根级（internal/*）")
	}
}
