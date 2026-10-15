package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"mulix/internal/event"
)

// ---- Task 9（023 FR-005）：工具卡 ----

// TestToolCardLifecycle 钉死（brief a）：ToolUseRequested(Bash, cmd) 开运行中
// 卡 → ToolResult(content, IsError=false) 同 ToolUseID upsert 原地关卡（不增
// 块）+ ≤8 行有界输出尾（⎿ 槽首行 + 对齐续行）。
func TestToolCardLifecycle(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 1, ToolUseID: "call_1", Tool: "Bash",
		Input: json.RawMessage(`{"command":"npm test"}`),
	}})
	if len(m.transcriptState.blocks) != 1 {
		t.Fatalf("tool request must open exactly one card block, blocks = %d", len(m.transcriptState.blocks))
	}
	card, ok := m.transcriptState.blocks[0].(*toolCard)
	if !ok {
		t.Fatalf("block must be a *toolCard, got %T", m.transcriptState.blocks[0])
	}
	if card.state != toolRunning || card.arg != "npm test" {
		t.Fatalf("running card: state=%d arg=%q", card.state, card.arg)
	}
	head := card.render(80)[0]
	for _, want := range []string{"\x1b[38;5;173m⏺\x1b[0m", "\x1b[1m运行\x1b[0m", "npm test", "运行中"} { // T18：accent 槽
		if !strings.Contains(head, want) {
			t.Fatalf("running header %q missing %q", head, want)
		}
	}

	// 回执关卡：同 ID upsert——块数不变，状态转完成，输出尾入卡。
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{
		Seq: 2, ToolUseID: "call_1", Content: "pass 1\npass 2",
	}})
	if len(m.transcriptState.blocks) != 1 {
		t.Fatalf("same-ID result must upsert the running card in place, blocks = %d", len(m.transcriptState.blocks))
	}
	card = m.transcriptState.blocks[0].(*toolCard)
	if card.state != toolDone {
		t.Fatalf("card must be done after result, state = %d", card.state)
	}
	lines := card.render(80)
	if len(lines) != 3 { // 头行 + 2 尾行
		t.Fatalf("done card = %d lines, want header + 2 tail:\n%v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "⎿") || !strings.Contains(lines[1], "pass 1") {
		t.Fatalf("first tail line = %q, want ⎿ gutter + pass 1", lines[1])
	}
	if !strings.Contains(lines[2], "pass 2") || strings.Contains(lines[2], "⎿") {
		t.Fatalf("continuation line = %q, want aligned without gutter", lines[2])
	}
	if strings.Contains(strings.Join(lines, "\n"), "运行中") {
		t.Fatal("closed card must not keep the running marker")
	}
}

// TestToolCardFailure 钉死（brief b）：IsError=true → 红卡 ⊘ 含失败详情。
func TestToolCardFailure(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 1, ToolUseID: "call_9", Tool: "Bash", Input: json.RawMessage(`{"command":"boom"}`),
	}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{
		Seq: 2, ToolUseID: "call_9", IsError: true,
		Content: "Error: command failed\n  at doThing",
	}})
	card := m.transcriptState.blocks[0].(*toolCard)
	if card.state != toolFailed {
		t.Fatalf("IsError must fail the card, state = %d", card.state)
	}
	if card.failedDetail != "Error: command failed" {
		t.Fatalf("failedDetail = %q, want first non-empty content line", card.failedDetail)
	}
	rendered := strings.Join(card.render(80), "\n")
	for _, want := range []string{"⊘", "失败", "Error: command failed"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("failure card %q missing %q", rendered, want)
		}
	}
	if !strings.Contains(rendered, "\x1b[38;5;167m") {
		t.Fatal("failure card must take the red slot (SGR 38;5;167)")
	}
}

// TestToolCardTailBound 钉死（brief c）：超长输出驻留保尾 ≤200 行（Task 21
// 展开面——原 T9 钉的 ≤8 行驻留已随 Ctrl+B 展开面升级为「驻留 ≤200、折叠
// 渲染尾 8」）；折叠渲染保尾 8 行；短输出原样。
func TestToolCardTailBound(t *testing.T) {
	var lines []string
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("l%02d", i))
	}
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 1, ToolUseID: "c", Tool: "Bash", Input: json.RawMessage(`{"command":"gen"}`),
	}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{
		Seq: 2, ToolUseID: "c", Content: strings.Join(lines, "\n"),
	}})
	card := m.transcriptState.blocks[0].(*toolCard)
	// 驻留：20 行 ≤ toolExpandMaxLines 全量保尾驻卡（展开面可用）
	if len(card.outputTail) != 20 || card.outputTail[0] != "l00" || card.outputTail[19] != "l19" {
		t.Fatalf("retention must keep up to %d lines: %d (first=%q last=%q)",
			toolExpandMaxLines, len(card.outputTail), card.outputTail[0], card.outputTail[19])
	}
	// 折叠渲染：头 + 保尾 8 行（l12..l19）
	rendered := card.render(80)
	if got := len(rendered); got != 1+toolTailLines {
		t.Fatalf("rendered = %d lines, want 1 header + %d tail", got, toolTailLines)
	}
	if !strings.Contains(rendered[1], "l12") || !strings.Contains(rendered[toolTailLines], "l19") {
		t.Fatalf("collapsed render must keep the trailing window, first=%q last=%q",
			rendered[1], rendered[toolTailLines])
	}

	// 短输出不截，尾空行修剪。
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 1, ToolUseID: "c", Tool: "Read", Input: json.RawMessage(`{"file_path":"a.txt"}`),
	}})
	m2 = update(t, m2, tuiEvent{ev: event.ToolResultEvent{
		Seq: 2, ToolUseID: "c", Content: "only\n\n",
	}})
	c2 := m2.transcriptState.blocks[0].(*toolCard)
	if len(c2.outputTail) != 1 || c2.outputTail[0] != "only" {
		t.Fatalf("short tail = %v, want [only]", c2.outputTail)
	}
}

// TestToolArgKey 钉死（brief d）：主参数提取表驱动（path/cmd/缺省摘要截断）。
func TestToolArgKey(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{"Read file_path", "Read", `{"file_path":"src/main.go"}`, "src/main.go"},
		{"Read path 别名", "Read", `{"path":"a.txt"}`, "a.txt"},
		{"Write file_path", "Write", `{"file_path":"a.txt"}`, "a.txt"},
		{"Bash command", "Bash", `{"command":"npm test"}`, "npm test"},
		{"Bash cmd 别名", "Bash", `{"cmd":"go vet"}`, "go vet"},
		{"Grep pattern", "Grep", `{"pattern":"^foo"}`, "^foo"},
		{"WebSearch query", "WebSearch", `{"query":"mulix"}`, "mulix"},
		{"mcp 工具缺省摘要", "mcp__github__create_issue", `{"title":"x"}`, `{"title":"x"}`},
		{"已知工具缺键退摘要", "Read", `{"other":1}`, `{"other":1}`},
		{"非法 JSON 原文摘要", "Bash", `not json`, "not json"},
		{"空输入空主参", "Bash", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolArgKey(tc.tool, json.RawMessage(tc.input)); got != tc.want {
				t.Fatalf("toolArgKey(%q, %s) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
	// 缺省摘要存储截断（toolArgSummaryMax + 省略号）。
	long := `{"command":"` + strings.Repeat("x", 200) + `"}`
	got := toolArgKey("TodoWrite", json.RawMessage(long))
	if n := len([]rune(got)); n > toolArgSummaryMax+1 {
		t.Fatalf("default summary must clamp to %d(+ellipsis) runes, got %d", toolArgSummaryMax, n)
	}
}

// TestToolVerb 钉死动词映射（brief：Read→读 Write→写 Edit→改 Bash→运行
// Glob/Grep→搜 mcp__*→短名；未映射原名兜底）。
func TestToolVerb(t *testing.T) {
	cases := map[string]string{
		"Read": "读", "Write": "写", "Edit": "改", "Bash": "运行",
		"Glob": "搜", "Grep": "搜",
		"mcp__github__create_issue": "create_issue",
		"mcp__server":               "server", // 无第二段——余部即短名
		"TodoWrite":                 "TodoWrite",
	}
	for tool, want := range cases {
		if got := toolVerb(tool); got != want {
			t.Fatalf("toolVerb(%q) = %q, want %q", tool, got, want)
		}
	}
}

// TestToolCardEventDiscipline 钉死块生命周期纪律（D14 + Task 7 裁定）：工具
// 事件是非文本转写事件——先收口思考块、整段冲刷流式答案，再落卡；孤儿回执
// （请求缺席）合成卡留痕，失败不静默。
func TestToolCardEventDiscipline(t *testing.T) {
	m, _, _ := newTestModel(t)
	// 段落未闭合的文本滞留 pending（无空行界）；随后思考分片重开活思考块。
	m = update(t, m, tuiEvent{ev: event.AssistantTextEvent{Seq: 2, Text: "intro"}})
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Thinking: "deep"}})
	if m.pendingMD == "" || m.thinkingIdx < 0 {
		t.Fatalf("setup: pending=%q thinkingIdx=%d", m.pendingMD, m.thinkingIdx)
	}
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 3, ToolUseID: "c", Tool: "Bash", Input: json.RawMessage(`{"command":"x"}`),
	}})
	if len(m.transcriptState.blocks) != 3 {
		t.Fatalf("collapse thinking + seal answer + card = 3 blocks, got %d", len(m.transcriptState.blocks))
	}
	tb, ok := m.transcriptState.blocks[0].(thinkingBlock)
	if !ok || !tb.collapsed {
		t.Fatalf("tool request must collapse the thinking block (ok=%v collapsed=%v)", ok, tb.collapsed)
	}
	ab, ok := m.transcriptState.blocks[1].(assistantBlock)
	if !ok || ab.text != "intro" {
		t.Fatalf("tool request must seal the pending answer, block[1] = %#v", m.transcriptState.blocks[1])
	}
	if m.pendingMD != "" || m.answerIdx != -1 {
		t.Fatalf("answer cursor must reset after seal, pending=%q idx=%d", m.pendingMD, m.answerIdx)
	}
	card := m.transcriptState.blocks[2].(*toolCard)
	if card.state != toolRunning {
		t.Fatalf("card must open running, state = %d", card.state)
	}

	// 孤儿回执：请求事件缺席（重放边界）——合成卡留痕。
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.ToolResultEvent{
		Seq: 1, ToolUseID: "ghost", IsError: true, Content: "lost",
	}})
	card2, ok := m2.transcriptState.blocks[0].(*toolCard)
	if !ok || card2.state != toolFailed || card2.failedDetail != "lost" {
		t.Fatalf("orphan result must synthesize a card (ok=%v state=%d detail=%q)",
			ok, card2.state, card2.failedDetail)
	}
}
