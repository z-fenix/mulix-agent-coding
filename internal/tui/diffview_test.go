package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"mulix/internal/event"

	"github.com/charmbracelet/x/ansi"
)

// ---- Task 20（023 D9/FR-005）：diff 卡 ----
//
// 工具/字段覆盖口径（D9）：fixtures/engine/*.jsonl 无 Edit/Write 实样（已核对
// ——只有 Read/Bash/Write 类占位外的工具事件缺席），字段名按 claude CLI 文档
// schema：Edit old_string/new_string + file_path；Write content（整文件纯新增
// 口径——Input 不含旧文）；MultiEdit edits[]（旧版 CLI 工具，链式取净效果）；
// NotebookEdit old_source/new_source + notebook_path。

// TestDiffFromEditInput 钉死（brief a）：Edit Input → diff 行含 -old/+new 与
// 行号；Write 整文件纯新增、NotebookEdit 换源、MultiEdit 链式净效果同口径。
// 卡片路径：写类请求开卡即挂 diff 视图（运行中可见），回执关卡照旧（尾部
// 输出 + diff 保留——对照参考 diffBlock 于 dispatch 时打印、结果不回收）。
func TestDiffFromEditInput(t *testing.T) {
	d, ok := diffFromInput("Edit", json.RawMessage(
		`{"file_path":"src/main.go","old_string":"old line","new_string":"new line"}`))
	if !ok {
		t.Fatal("Edit old_string/new_string must be diffable")
	}
	if d.path != "src/main.go" || d.verb != "改" || d.before != "old line" || d.after != "new line" {
		t.Fatalf("diffInput = %+v", d)
	}
	// 内容断言剥 SGR（Task 6 换装后标记与代码段被行内回铺 SGR 分隔——内容
	// 连续性按剥离文本判定；SGR 形态钉点归 TestDiffRowBGSlots/TestDiffHeaderCounts）。
	rendered := ansi.Strip(strings.Join(renderDiff(d, 80, activeTheme), "\n"))
	for _, want := range []string{"- old line", "+ new line", "1"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Edit diff render %q missing %q", rendered, want)
		}
	}

	// 卡片接线：写类请求 → 卡内 diff 视图（运行中即渲染）。
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 1, ToolUseID: "call_1", Tool: "Edit",
		Input: json.RawMessage(`{"file_path":"src/main.go","old_string":"old line","new_string":"new line"}`),
	}})
	card, isCard := m.transcriptState.blocks[0].(*toolCard)
	if !isCard || card.diff == nil {
		t.Fatalf("write-class request must attach a diff view (isCard=%v diff=%v)", isCard, card.diff)
	}
	if card.state != toolRunning {
		t.Fatalf("card must still be running, state = %d", card.state)
	}
	body := ansi.Strip(strings.Join(card.render(80), "\n"))
	if !strings.Contains(body, "✎") || !strings.Contains(body, "- old line") || !strings.Contains(body, "+ new line") {
		t.Fatalf("running card must render the diff view:\n%s", body)
	}

	// 回执关卡照旧（同 ID upsert 原地改写）：状态转完成 + 输出尾，diff 保留。
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{
		Seq: 2, ToolUseID: "call_1", Content: "applied",
	}})
	if card.state != toolDone {
		t.Fatalf("result must close the card as today, state = %d", card.state)
	}
	body = ansi.Strip(strings.Join(card.render(80), "\n"))
	for _, want := range []string{"- old line", "+ new line", "⎿", "applied"} {
		if !strings.Contains(body, want) {
			t.Fatalf("closed card must keep diff rows and the output tail:\n%s", body)
		}
	}

	// Write：整文件纯新增口径（before 空——Input 不含旧文）。
	w, ok := diffFromInput("Write", json.RawMessage(`{"file_path":"a.txt","content":"one\ntwo\n"}`))
	if !ok || w.before != "" || w.after != "one\ntwo\n" || w.verb != "写" {
		t.Fatalf("Write diffInput = %+v ok=%v", w, ok)
	}
	wr := ansi.Strip(strings.Join(renderDiff(w, 80, activeTheme), "\n"))
	if !strings.Contains(wr, "+ one") || !strings.Contains(wr, "+ two") {
		t.Fatalf("Write diff render:\n%s", wr)
	}

	// NotebookEdit：old_source/new_source + notebook_path。
	n, ok := diffFromInput("NotebookEdit", json.RawMessage(
		`{"notebook_path":"n.ipynb","old_source":"x","new_source":"y"}`))
	if !ok || n.path != "n.ipynb" || n.before != "x" || n.after != "y" {
		t.Fatalf("NotebookEdit diffInput = %+v ok=%v", n, ok)
	}

	// MultiEdit：链式套用取净效果（A→B→C 净差 A→C；未命中的旧串原样保留）。
	me, ok := diffFromInput("MultiEdit", json.RawMessage(
		`{"file_path":"m.go","edits":[{"old_string":"A","new_string":"B"},{"old_string":"B","new_string":"C"}]}`))
	if !ok || me.path != "m.go" || me.before != "A" || me.after != "C" {
		t.Fatalf("MultiEdit chained diffInput = %+v ok=%v", me, ok)
	}
}

// TestDiffHeaderCounts 钉死（brief b）：头行 +A -B 计数正确——增行绿槽
// （T18 预留槽首个消费点，dsh dark 手选 114）、删行红槽（203）；零侧省略
// （对照参考 diffStat）；SGR 钉点与 activeTheme 包级初值同源（T18 注记）。
func TestDiffHeaderCounts(t *testing.T) {
	d := diffInput{path: "a.go", verb: "改", before: "a\nb\n", after: "a\nc\nd\n"}
	lines := renderDiff(d, 80, activeTheme)
	head := lines[0]
	if !strings.Contains(head, "+2") || !strings.Contains(head, "-1") {
		t.Fatalf("header %q must carry +2 -1", head)
	}
	if !strings.Contains(head, "\x1b[38;5;114m+2") || !strings.Contains(head, "\x1b[38;5;203m-1") {
		t.Fatalf("header counts must take green(114)/red(203) slots: %q", head)
	}
	if !strings.Contains(head, "a.go") || !strings.Contains(head, "✎") {
		t.Fatalf("header %q must carry ✎ and path", head)
	}
	// 行级色分（Task 6 换装后）：增/删行 = diff bg 槽整行 + marker 前景
	// （gutter/标记段），上下文行不沾 diff 槽色（扫描体行——跳过头行；定位
	// 按剥离文本——标记与代码段被行内回铺 SGR 分隔，变量留原行供 SGR 断言）。
	var addRow, delRow, ctxRow string
	for _, l := range lines[1:] {
		switch {
		case strings.Contains(ansi.Strip(l), "+ c") && addRow == "":
			addRow = l
		case strings.Contains(ansi.Strip(l), "- b") && delRow == "":
			delRow = l
		case strings.Contains(l, "a") && ctxRow == "":
			ctxRow = l
		}
	}
	if !strings.HasPrefix(addRow, "\x1b[48;5;22m\x1b[38;5;114m") {
		t.Fatalf("added row must lead with diffAddedBG band + marker fg: %q", addRow)
	}
	if !strings.HasPrefix(delRow, "\x1b[48;5;52m\x1b[38;5;203m") {
		t.Fatalf("deleted row must lead with diffDeletedBG band + marker fg: %q", delRow)
	}
	for _, bad := range []string{"48;5;22", "48;5;52", "38;5;114", "38;5;203"} {
		if strings.Contains(ctxRow, bad) {
			t.Fatalf("context row must not take diff slots (%s): %q", bad, ctxRow)
		}
	}

	// 纯新增：零删侧省略（不出 -0）。
	pure := renderDiff(diffInput{path: "b.txt", verb: "写", after: "x\ny\nz\n"}, 80, activeTheme)
	if !strings.Contains(pure[0], "+3") {
		t.Fatalf("pure-add header %q must carry +3", pure[0])
	}
	if strings.Contains(pure[0], "-0") {
		t.Fatalf("pure-add header %q must omit the zero side", pure[0])
	}
}

// TestDiffFallback 钉死（brief c）：无可 diff 结构字段的 Input →
// diffFromInput=false → 卡片无 diff 视图（普通工具卡降级路径）。
func TestDiffFallback(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		input string
	}{
		{"非写类工具", "Bash", `{"command":"npm test"}`},
		{"非法 JSON", "Edit", `not json`},
		{"空输入", "Write", ``},
		{"缺路径", "Edit", `{"old_string":"a","new_string":"b"}`},
		{"两侧相同", "Edit", `{"file_path":"a.go","old_string":"s","new_string":"s"}`},
		{"Write 空内容", "Write", `{"file_path":"a.txt","content":""}`},
		{"MultiEdit 空编辑", "MultiEdit", `{"file_path":"m.go","edits":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if d, ok := diffFromInput(tc.tool, json.RawMessage(tc.input)); ok {
				t.Fatalf("diffFromInput(%q, %s) = %+v, want false", tc.tool, tc.input, d)
			}
		})
	}

	// 卡片降级：Bash 请求 → 卡无 diff 视图，头行 + 输出尾照旧。
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{
		Seq: 1, ToolUseID: "call_2", Tool: "Bash",
		Input: json.RawMessage(`{"command":"npm test"}`),
	}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{
		Seq: 2, ToolUseID: "call_2", Content: "ok",
	}})
	card, isCard := m.transcriptState.blocks[0].(*toolCard)
	if !isCard || card.diff != nil {
		t.Fatalf("non-diffable request must leave the card without a diff view (isCard=%v)", isCard)
	}
	body := strings.Join(card.render(80), "\n")
	if strings.Contains(body, "✎") {
		t.Fatalf("degraded card must not render a diff view:\n%s", body)
	}
	if !strings.Contains(body, "⎿") || !strings.Contains(body, "ok") {
		t.Fatalf("degraded card must render the ordinary tail:\n%s", body)
	}
}

// ---- Task 6（2026-10-17-dsh-tui-visual-redesign）：diff 卡 bg 槽消费 ----

// TestDiffRowBGSlots 钉点（Task 6 Step 1）：diff 增/删行换装 dsh diff 令牌——
// 整行铺 bg 槽（暗板 added 22 / deleted 52）+ gutter/标记段 marker 前景
// （114/203）；marker 段收口的重置点回铺 bg（fenceBG 同法，帧出口 paintBG 的
// 三元序 reset→帧底→diff 底同构——TestFenceBGSurvivesFramePaint 钉点），代码
// 段无独立前景（默认前景压色带）；上下文行不沾 diff 槽色；hunk 间 ⋮ 分隔 dim
// 不变；NO_COLOR 退避平文（TestNoColorFallback 口径：Setenv + 重建主题）。
func TestDiffRowBGSlots(t *testing.T) {
	resetTheme(t)
	applyTheme(defaultActiveTheme()) // 钉确定性 256 层（防前序测试主题泄漏）
	d := diffInput{path: "a.go", verb: "改",
		before: "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nn\n",
		after:  "a\nX\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\nZ\n"}
	lines := renderDiff(d, 80, activeTheme)
	joined := strings.Join(lines, "\n")
	// 增/删行整行钉字面（gw=2——行号上限 14；marker 段 reset 后回铺 bg、代码
	// 段无独立前景）：加行 22/114、删行 52/203；两 hunk 同构（14 行侧行同钉）。
	wantAdd := "\x1b[48;5;22m\x1b[38;5;114m   2 + \x1b[0m\x1b[48;5;22mX\x1b[0m"
	wantDel := "\x1b[48;5;52m\x1b[38;5;203m   2 - \x1b[0m\x1b[48;5;52mb\x1b[0m"
	wantAdd2 := "\x1b[48;5;22m\x1b[38;5;114m  14 + \x1b[0m\x1b[48;5;22mZ\x1b[0m"
	wantDel2 := "\x1b[48;5;52m\x1b[38;5;203m  14 - \x1b[0m\x1b[48;5;52mn\x1b[0m"
	for _, want := range []string{wantAdd, wantDel, wantAdd2, wantDel2} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diff rows must be the band+marker composite:\nwant %q\ngot:\n%s", want, joined)
		}
	}
	// hunk 间 ⋮ 分隔 dim 不变（且恰一处——双 hunk 的形状守卫）。
	sep := "  \x1b[38;5;244m⋮\x1b[0m"
	if n := strings.Count(joined, sep); n != 1 {
		t.Fatalf("hunk separator must be exactly one dim ⋮ row, got %d:\n%s", n, joined)
	}
	// 上下文行不沾 diff 槽色（dim 行号 + chroma/平文——行 "c" 为 hunk1 上下文）。
	var ctxRow string
	for _, l := range lines[1:] {
		if strings.Contains(l, "c") && ctxRow == "" {
			ctxRow = l
		}
	}
	if ctxRow == "" {
		t.Fatalf("context row missing:\n%s", joined)
	}
	for _, bad := range []string{"48;5;22", "48;5;52", "38;5;114", "38;5;203"} {
		if strings.Contains(ctxRow, bad) {
			t.Fatalf("context row must not take diff slots (%s): %q", bad, ctxRow)
		}
	}

	// NO_COLOR：槽位全空 → 增/删行退避平文（TestNoColorFallback 口径：只禁
	// 色不禁装饰——断言无前景/背景色 SGR，bold 等装饰属性不在禁列），内容原样。
	t.Setenv("NO_COLOR", "1")
	resetTheme(t)
	th, ok := themeByName("ember")
	if !ok {
		t.Fatal("ember must resolve under NO_COLOR")
	}
	applyTheme(th)
	plain := strings.Join(renderDiff(d, 80, activeTheme), "\n")
	for _, bad := range []string{"\x1b[38;2;", "\x1b[38;5;", "\x1b[48;2;", "\x1b[48;5;"} {
		if strings.Contains(plain, bad) {
			t.Fatalf("no-color tier must render the diff without color SGR (%s):\n%q", bad, plain)
		}
	}
	if !strings.Contains(plain, "+ X") || !strings.Contains(plain, "- b") {
		t.Fatalf("content must survive the degrade:\n%q", plain)
	}
}
