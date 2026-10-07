package guard

import (
	"encoding/json"
	"testing"
)

// TestDecisionConstants pins：Decision 类型化常量（Task 6 交接）——JSON 稳定
// 字符串（event.ApprovalResolvedEvent 决策词表为 allow|deny|timeout，
// 与本词表 allow|ask|deny 刻意区分：ask 是策略裁决，进入审批队列）。
func TestDecisionConstants(t *testing.T) {
	if DecisionAllow != "allow" || DecisionAsk != "ask" || DecisionDeny != "deny" {
		t.Fatalf("decision constants drifted: %q %q %q", DecisionAllow, DecisionAsk, DecisionDeny)
	}
}

// TestParseRule pins：规则字符串形态——裸工具名与 Tool(specifier)。
func TestParseRule(t *testing.T) {
	cases := []struct {
		in        string
		tool      string
		specifier string
	}{
		{"Bash", "Bash", ""},
		{"WebFetch", "WebFetch", ""},
		{"Bash(go test:*)", "Bash", "go test:*"},
		{"Edit(docs/**)", "Edit", "docs/**"},
		{"Bash(npm run build)", "Bash", "npm run build"},
	}
	for _, c := range cases {
		r, err := ParseRule(c.in)
		if err != nil {
			t.Fatalf("ParseRule(%q): %v", c.in, err)
		}
		if r.Tool != c.tool || r.Specifier != c.specifier {
			t.Fatalf("ParseRule(%q) = %+v, want tool=%q spec=%q", c.in, r, c.tool, c.specifier)
		}
	}
	for _, bad := range []string{"", "(", "Bash(", "Bash()", "Too l(spec)"} {
		if _, err := ParseRule(bad); err == nil {
			t.Fatalf("ParseRule(%q) must fail", bad)
		}
	}
}

// TestNewPolicyValidation pins：非法 preset / 规则效果在构造期报错（fail-closed）。
func TestNewPolicyValidation(t *testing.T) {
	if _, err := NewPolicy(Preset("yolo"), nil); err == nil {
		t.Fatal("invalid preset must fail")
	}
	deny, _ := ParseRule("Bash(rm:*)")
	if _, err := NewPolicy(PresetDefault, []Rule{{Tool: "Bash", Effect: Decision("maybe")}, deny}); err == nil {
		t.Fatal("invalid effect must fail")
	}
	if _, err := NewPolicy(PresetDefault, []Rule{{Tool: "", Effect: DecisionDeny}}); err == nil {
		t.Fatal("empty tool must fail")
	}
}

// TestRulePriorityMatrix pins：deny > ask > allow > fallback——优先级与规则
// 顺序无关；无匹配规则时落预设 fallback。
func TestRulePriorityMatrix(t *testing.T) {
	allow, _ := ParseRule("Bash")
	allow.Effect = DecisionAllow
	ask, _ := ParseRule("Bash")
	ask.Effect = DecisionAsk
	// 三种排列都必须是 Deny（deny 最高优先）
	orders := [][]Rule{
		{{Tool: "Bash", Effect: DecisionDeny}, {Tool: "Bash", Effect: DecisionAllow}, {Tool: "Bash", Effect: DecisionAsk}},
		{{Tool: "Bash", Effect: DecisionAllow}, {Tool: "Bash", Effect: DecisionDeny}, {Tool: "Bash", Effect: DecisionAsk}},
		{{Tool: "Bash", Effect: DecisionAsk}, {Tool: "Bash", Effect: DecisionAllow}, {Tool: "Bash", Effect: DecisionDeny}},
	}
	for i, rules := range orders {
		p, err := NewPolicy(PresetDefault, rules)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.Decide("Bash", bashInput("anything")); got != DecisionDeny {
			t.Fatalf("order %d: deny must win, got %v", i, got)
		}
	}
	// ask 胜 allow
	p, _ := NewPolicy(PresetDefault, []Rule{allow, ask})
	if got := p.Decide("Bash", bashInput("x")); got != DecisionAsk {
		t.Fatalf("ask must beat allow, got %v", got)
	}
	// allow 规则压过 default 预设的 Ask fallback
	p, _ = NewPolicy(PresetDefault, []Rule{allow})
	if got := p.Decide("Bash", bashInput("x")); got != DecisionAllow {
		t.Fatalf("explicit allow must beat preset fallback, got %v", got)
	}
	// 无匹配 → fallback（default 下 Bash=Ask）
	p, _ = NewPolicy(PresetDefault, nil)
	if got := p.Decide("Bash", bashInput("x")); got != DecisionAsk {
		t.Fatalf("fallback must be preset decision, got %v", got)
	}
}

// TestBashSpecifier pins：Bash specifier 语义——`:*` 后缀 = 前缀匹配；
// 无 `:*` = 整串精确匹配；不匹配落 fallback。
func TestBashSpecifier(t *testing.T) {
	p, err := NewPolicy(PresetDefault, mustRules(t,
		ruleIn{raw: "Bash(go test:*)", effect: DecisionAllow},
		ruleIn{raw: "Bash(npm run build)", effect: DecisionDeny},
	))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		cmd  string
		want Decision
	}{
		{"go test ./...", DecisionAllow},         // 前缀命中
		{"go test", DecisionAllow},               // 恰为前缀
		{"go test\tx", DecisionAllow},            // tab = 词边界（shell IFS 空白，等价 go test x）
		{"go testx", DecisionAsk},                // 前缀必须按词面起始（非子串）
		{"go test;rm", DecisionAsk},              // 词边界红线：分号渗漏不放行
		{"go test; rm -rf /", DecisionAsk},       // 空白+shell 元字符：v1 前缀白名单固有残余（已披露），不扩大
		{"go test\nx", DecisionAsk},              // 换行 = POSIX 命令分隔符，fail-closed
		{"go vet ./...", DecisionAsk},            // 无匹配 → fallback
		{"npm run build", DecisionDeny},          // 精确命中
		{"npm run build --verbose", DecisionAsk}, // 精确规则不吃前缀
	}
	for _, c := range cases {
		if got := p.Decide("Bash", bashInput(c.cmd)); got != c.want {
			t.Fatalf("Decide(Bash, %q) = %v, want %v", c.cmd, got, c.want)
		}
	}
	// input 缺 command 字段 → 规则无从匹配 → fallback
	if got := p.Decide("Bash", json.RawMessage(`{}`)); got != DecisionAsk {
		t.Fatalf("missing command must fall back, got %v", got)
	}
}

// TestFileSpecifierGlob pins：文件工具 specifier = glob（`**` 跨段、`*` 单段）；
// Windows 反斜杠路径先规范化再匹配。
func TestFileSpecifierGlob(t *testing.T) {
	p, err := NewPolicy(PresetDefault, mustRules(t,
		ruleIn{raw: "Edit(docs/**)", effect: DecisionAllow},
		ruleIn{raw: "Read(secrets/*.env)", effect: DecisionDeny},
	))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tool, path string
		want       Decision
	}{
		{"Edit", "docs/a.md", DecisionAllow},
		{"Edit", "docs/sub/deep/b.md", DecisionAllow},   // ** 跨段
		{"Edit", `docs\win\path.md`, DecisionAllow},     // 反斜杠规范化
		{"Edit", "docsx/a.md", DecisionAsk},             // 段边界不吃前缀
		{"Write", "docs/other.md", DecisionAsk},         // 规则绑定 Edit，Write 不沾光
		{"Read", "secrets/prod.env", DecisionDeny},      // * 单段
		{"Read", "secrets/sub/deep.env", DecisionAllow}, // * 不跨段：deny 规则不命中 → Read 落 allow fallback
	}
	for _, c := range cases {
		if got := p.Decide(c.tool, pathInput(c.path)); got != c.want {
			t.Fatalf("Decide(%s, %q) = %v, want %v", c.tool, c.path, got, c.want)
		}
	}
}

// TestSpecifierOnUnsupportedTool pins：v1 specifier 语义只覆盖 Bash（命令）
// 与文件族（路径）——其他工具带 specifier 的规则永不匹配（落 fallback，
// 不报错；无 specifier 的裸规则照常匹配）。
func TestSpecifierOnUnsupportedTool(t *testing.T) {
	p, err := NewPolicy(PresetDefault, mustRules(t,
		ruleIn{raw: "WebFetch(example.com/*)", effect: DecisionAllow},
		ruleIn{raw: "Task", effect: DecisionDeny},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Decide("WebFetch", json.RawMessage(`{"url":"https://example.com/x"}`)); got != DecisionAsk {
		t.Fatalf("unsupported specifier must not match, got %v", got)
	}
	if got := p.Decide("Task", json.RawMessage(`{}`)); got != DecisionDeny {
		t.Fatalf("bare rule must match any input, got %v", got)
	}
}

// TestPresetMatrices pins：§9 预设矩阵（fallback 语义）。
func TestPresetMatrices(t *testing.T) {
	read := map[string]json.RawMessage{
		"Read":         pathInput("any.txt"),
		"Glob":         json.RawMessage(`{"pattern":"**"}`),
		"Grep":         json.RawMessage(`{}`),
		"Bash":         bashInput("go version"),
		"Write":        pathInput("out.txt"),
		"Edit":         pathInput("out.txt"),
		"NotebookEdit": pathInput("nb.ipynb"),
		"WebFetch":     json.RawMessage(`{"url":"https://x"}`),
		"Task":         json.RawMessage(`{}`),
		"MysteryTool":  json.RawMessage(`{}`),
	}
	type expect map[string]Decision
	cases := []struct {
		preset Preset
		want   expect
	}{
		{PresetDefault, expect{
			"Read": DecisionAllow, "Glob": DecisionAllow, "Grep": DecisionAllow,
			"Bash": DecisionAsk, "Write": DecisionAsk, "Edit": DecisionAsk,
			"NotebookEdit": DecisionAsk, "WebFetch": DecisionAsk, "Task": DecisionAsk,
			"MysteryTool": DecisionAsk, // 未知工具 fail-closed → Ask
		}},
		{PresetAcceptEdits, expect{
			"Read": DecisionAllow, "Glob": DecisionAllow, "Grep": DecisionAllow,
			"Bash":  DecisionAsk,
			"Write": DecisionAllow, "Edit": DecisionAllow, "NotebookEdit": DecisionAllow,
			"WebFetch": DecisionAsk, "Task": DecisionAsk, "MysteryTool": DecisionAsk,
		}},
		{PresetPlan, expect{
			"Read": DecisionAllow, "Glob": DecisionAllow, "Grep": DecisionAllow,
			"Bash": DecisionDeny, "Write": DecisionDeny, "Edit": DecisionDeny,
			"NotebookEdit": DecisionDeny, "WebFetch": DecisionAllow,
			"Task": DecisionAsk, "MysteryTool": DecisionAsk,
		}},
		{PresetDangerFull, expect{
			"Read": DecisionAllow, "Glob": DecisionAllow, "Grep": DecisionAllow,
			"Bash": DecisionAllow, "Write": DecisionAllow, "Edit": DecisionAllow,
			"NotebookEdit": DecisionAllow, "WebFetch": DecisionAllow,
			"Task": DecisionAllow, "MysteryTool": DecisionAllow,
		}},
	}
	for _, c := range cases {
		p, err := NewPolicy(c.preset, nil)
		if err != nil {
			t.Fatalf("preset %s: %v", c.preset, err)
		}
		for tool, input := range read {
			if got := p.Decide(tool, input); got != c.want[tool] {
				t.Fatalf("preset %s: Decide(%s) = %v, want %v", c.preset, tool, got, c.want[tool])
			}
		}
	}
}

// TestDangerFullExplicitDenyWins pins：danger-full 全 Allow 但显式 deny 仍优先
// （§9 红线：显式 deny > 预设）。
func TestDangerFullExplicitDenyWins(t *testing.T) {
	p, err := NewPolicy(PresetDangerFull, mustRules(t,
		ruleIn{raw: "Bash(rm:*)", effect: DecisionDeny},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Decide("Bash", bashInput("rm -rf /")); got != DecisionDeny {
		t.Fatalf("explicit deny must win under danger-full, got %v", got)
	}
	if got := p.Decide("Bash", bashInput("go test ./...")); got != DecisionAllow {
		t.Fatalf("danger-full fallback must allow, got %v", got)
	}
}

// TestDangerFullExplicitAskBeatsFallback pins：danger-full 的 allow fallback
// 之上，显式 ask 规则仍按优先级胜出（ask > allow，与预设无关）。
func TestDangerFullExplicitAskBeatsFallback(t *testing.T) {
	p, err := NewPolicy(PresetDangerFull, mustRules(t,
		ruleIn{raw: "Bash(git:*)", effect: DecisionAsk},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Decide("Bash", bashInput("git push")); got != DecisionAsk {
		t.Fatalf("explicit ask must beat danger-full allow fallback, got %v", got)
	}
}

// TestPlanPresetExplicitAllowWins pins：plan 预设的 Deny 是 fallback——
// 显式 allow 规则仍可放行（规则 > 预设；显式配置是操作者意图）。
func TestPlanPresetExplicitAllowWins(t *testing.T) {
	p, err := NewPolicy(PresetPlan, mustRules(t,
		ruleIn{raw: "Edit(docs/**)", effect: DecisionAllow},
	))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Decide("Edit", pathInput("docs/a.md")); got != DecisionAllow {
		t.Fatalf("explicit allow must beat plan deny-fallback, got %v", got)
	}
	if got := p.Decide("Edit", pathInput("src/a.go")); got != DecisionDeny {
		t.Fatalf("plan fallback must deny, got %v", got)
	}
}

// ---- 测试助手 ----

func bashInput(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func pathInput(path string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"file_path": path})
	return b
}

type ruleIn struct {
	raw    string
	effect Decision
}

func mustRules(t *testing.T, ins ...ruleIn) []Rule {
	t.Helper()
	var rules []Rule
	for _, in := range ins {
		r, err := ParseRule(in.raw)
		if err != nil {
			t.Fatalf("ParseRule(%q): %v", in.raw, err)
		}
		r.Effect = in.effect
		rules = append(rules, r)
	}
	return rules
}
