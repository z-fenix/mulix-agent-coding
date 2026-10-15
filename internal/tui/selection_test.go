package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"

	"mulix/internal/event"
)

// ---- 选择复制 + Ctrl+O/B 展开 + 滚动条拖拽（023 Story4 / Task 21）----

// seedLines 直接向转写追加 plainBlock 行（选择/拖拽测试的几何种子）。
func seedLines(m *tuiModel, lines ...string) {
	for _, l := range lines {
		m.appendLine(l)
	}
}

// mouse 是鼠标消息构造捷径（坐标为零基屏幕格）。
func mouseClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func mouseMotion(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func mouseRelease(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// stubClipboard 换掉剪贴板接缝并记录调用（t.Cleanup 还原）。
func stubClipboard(t *testing.T) (calls *[]string, fail *error) {
	t.Helper()
	var got []string
	var err error
	prev := copyToClipboard
	copyToClipboard = func(s string) error { got = append(got, s); return err }
	calls, fail = &got, &err
	t.Cleanup(func() { copyToClipboard = prev })
	return calls, fail
}

// TestSelectCopy pins（Story4-a）：左键拖选行内区间 → 松开复制选中文本；
// 跨行选择逐行拼接；无拖动单击清旧选区不复制；宽度变更重置选区（设计 §7.4）。
func TestSelectCopy(t *testing.T) {
	m, _, _ := newTestModel(t)
	seedLines(m, "line-one", "line-two", "line-three")
	ts := &m.transcriptState
	ts.syncViewport(40, 6) // 固定几何（cw=40, h=6）——不走 View() 免重排
	calls, _ := stubClipboard(t)

	// (a) 行内拖选：click(2,0) → motion(5,0) → release → 复制 "ne-"
	m = update(t, m, mouseClick(2, 0))
	if !ts.sel.active || ts.sel.anchor != (selPos{0, 2}) || ts.sel.head != (selPos{0, 2}) {
		t.Fatalf("click must start selection: %+v", ts.sel)
	}
	m = update(t, m, mouseMotion(5, 0))
	if ts.sel.head != (selPos{0, 5}) {
		t.Fatalf("motion must extend head: %+v", ts.sel)
	}
	// 高亮区间：第 0 行 [2,5)
	start, end := ts.sel.ordered()
	lo, hi, ok := selSpan(0, start, end, 40)
	if !ok || lo != 2 || hi != 5 {
		t.Fatalf("selSpan = (%d,%d,%v), want (2,5,true)", lo, hi, ok)
	}
	m = update(t, m, mouseRelease(5, 0))
	if len(*calls) != 1 || (*calls)[0] != "ne-" {
		t.Fatalf("release must copy selection: %v", *calls)
	}

	// (b) 跨行选择：第 1 行 col2 → 第 2 行 col4 → "ne-two\nline"
	m = update(t, m, mouseClick(2, 1))
	m = update(t, m, mouseMotion(4, 2))
	m = update(t, m, mouseRelease(4, 2))
	if len(*calls) != 2 || (*calls)[1] != "ne-two\nline" {
		t.Fatalf("multi-line copy = %v", *calls)
	}

	// (c) 无拖动单击：清旧选区不复制
	m = update(t, m, mouseClick(0, 0))
	if !ts.sel.active {
		t.Fatal("click must arm a fresh selection")
	}
	m = update(t, m, mouseRelease(0, 0))
	if len(*calls) != 2 {
		t.Fatalf("plain click must not copy: %v", *calls)
	}
	if ts.sel.active {
		t.Fatal("empty release must clear the selection")
	}

	// (d) 任意击键散选区（复制后高亮是残影——对照参考）
	m = update(t, m, mouseClick(0, 0))
	m = update(t, m, mouseMotion(3, 0))
	m = update(t, m, keyPress('x'))
	if ts.sel.active {
		t.Fatal("any keystroke must dismiss the selection")
	}

	// (e) 宽度变更重置选区（设计 §7.4：重排后列坐标失效）
	m = update(t, m, mouseClick(1, 0))
	m = update(t, m, mouseMotion(3, 0))
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if ts.sel.active {
		t.Fatal("width resize must reset the selection")
	}
}

// TestCtrlCCopyRung pins（Story4-a × T10 阶梯）：Ctrl+C 第一档——活跃选区
// 复制（不清输入、不武装退出）；无选区回退既有阶梯（清输入/双击武装）。
func TestCtrlCCopyRung(t *testing.T) {
	m, _, _ := newTestModel(t)
	seedLines(m, "line-one", "line-two")
	ts := &m.transcriptState
	ts.syncViewport(40, 6)
	calls, _ := stubClipboard(t)

	m = update(t, m, mouseClick(0, 0))
	m = update(t, m, mouseMotion(8, 0))
	m.composerState.ta.SetValue("draft")
	m = update(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if len(*calls) != 1 || (*calls)[0] != "line-one" {
		t.Fatalf("Ctrl+C must copy the active selection: %v", *calls)
	}
	if v := m.composerValue(); v != "draft" {
		t.Fatalf("copy rung must not clear input: %q", v)
	}
	if !m.quitArm.IsZero() {
		t.Fatal("copy rung must not arm quit")
	}
	if ts.sel.active {
		t.Fatal("copy rung consumes the highlight")
	}

	// 无选区：既有阶梯不受影响（清输入）
	m = update(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if v := m.composerValue(); v != "" {
		t.Fatalf("ladder must clear input without selection: %q", v)
	}
}

// TestSelectGovernanceGate pins（治理独占优先）：审批态下鼠标点击/拖动/松开
// 一律忽略——不启动选区、不复制。
func TestSelectGovernanceGate(t *testing.T) {
	m, _, _ := newTestModel(t)
	seedLines(m, "line-one", "line-two")
	ts := &m.transcriptState
	ts.syncViewport(40, 6)
	calls, _ := stubClipboard(t)

	m.approval = &approvalCard{toolUseID: "t1"}
	m = update(t, m, mouseClick(0, 0))
	m = update(t, m, mouseMotion(5, 0))
	m = update(t, m, mouseRelease(5, 0))
	m = update(t, m, mouseClick(ts.vp.Width(), 0)) // 滚动条列同样被门禁
	m = update(t, m, mouseMotion(ts.vp.Width(), 3))
	if ts.sel.active || ts.scrollbarDrag {
		t.Fatalf("governance-exclusive state must ignore mouse: sel=%+v drag=%v", ts.sel, ts.scrollbarDrag)
	}
	m.approval = nil
	m = update(t, m, mouseRelease(5, 0))
	if len(*calls) != 0 {
		t.Fatalf("no copy may leak from the gated gesture: %v", *calls)
	}
}

// TestCopyFailureNotice pins：剪贴板写失败留痕（fail-visible，不静默）。
func TestCopyFailureNotice(t *testing.T) {
	m, _, _ := newTestModel(t)
	seedLines(m, "line-one")
	ts := &m.transcriptState
	ts.syncViewport(40, 6)
	_, fail := stubClipboard(t)
	*fail = fmt.Errorf("no clipboard")

	m = update(t, m, mouseClick(0, 0))
	m = update(t, m, mouseMotion(4, 0))
	_, cmd := m.Update(mouseRelease(4, 0))
	if cmd == nil {
		t.Fatal("release must return the copy cmd")
	}
	msg := cmd()
	if _, ok := msg.(copyDoneMsg); !ok {
		t.Fatalf("cmd = %T, want copyDoneMsg", msg)
	}
	next, _ := m.Update(msg)
	m = next.(*tuiModel)
	if !strings.Contains(m.View().Content, "[复制失败]") {
		t.Fatal("copy failure must leave a notice")
	}
}

// TestCtrlOVerbose pins（Story4-b）：Ctrl+O 折叠思考块 ↔ 全文（会话级开关
// ——对照参考 showReasoning）；无思考块时 no-op（不追加留痕行）。
func TestCtrlOVerbose(t *testing.T) {
	m, _, _ := newTestModel(t)
	prev := verboseThinking
	t.Cleanup(func() { verboseThinking = prev })
	verboseThinking = false

	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Turn: 1, Thinking: "secret-thought"}})
	m = update(t, m, tuiEvent{ev: event.NoticeEvent{Content: "after"}}) // 收口折叠
	view := m.View().Content
	if strings.Contains(view, "secret-thought") {
		t.Fatalf("collapsed block must hide full text:\n%s", view)
	}
	if !strings.Contains(view, "已思考") {
		t.Fatalf("collapsed summary missing:\n%s", view)
	}

	// Ctrl+O 展开：全文可见
	m = update(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if !verboseThinking {
		t.Fatal("Ctrl+O must toggle verbose on")
	}
	if view := m.View().Content; !strings.Contains(view, "secret-thought") {
		t.Fatalf("Ctrl+O must expand full text:\n%s", view)
	}

	// 再按折叠
	m = update(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if verboseThinking {
		t.Fatal("second Ctrl+O must toggle verbose off")
	}
	if view := m.View().Content; strings.Contains(view, "secret-thought") {
		t.Fatal("second Ctrl+O must collapse back")
	}

	// no-op：无思考块不动作（不留痕、不动开关）
	m2, _, _ := newTestModel(t)
	b2 := len(m2.transcriptState.blocks)
	m2 = update(t, m2, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if verboseThinking || len(m2.transcriptState.blocks) != b2 {
		t.Fatalf("Ctrl+O without thinking blocks must no-op: %d blocks, verbose=%v",
			len(m2.transcriptState.blocks), verboseThinking)
	}
}

// TestCtrlBExpand pins（Story4-c）：Ctrl+B 工具卡输出尾 ↔ 展开（≤200 行上限
// ——驻留保尾 200，折叠渲染保尾 8）；目标 = 最近一张有输出的非失败卡；无目标
// no-op；失败卡排除。
func TestCtrlBExpand(t *testing.T) {
	m, _, _ := newTestModel(t)

	// 300 行输出 → 折叠尾 8 行，展开保尾 200 行（前 100 行弃）
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "out-%03d\n", i)
	}
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{ToolUseID: "t1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{ToolUseID: "t1", Content: b.String()}})
	idx := m.cards["t1"]
	card := m.transcriptState.blocks[idx].(*toolCard)

	if lines := card.render(40); len(lines) != 9 || !strings.Contains(lines[1], "out-292") {
		t.Fatalf("collapsed tail must be head+8: %d lines", len(lines))
	}

	m = update(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !card.expanded {
		t.Fatal("Ctrl+B must expand the most recent tool card")
	}
	lines := card.render(40)
	if len(lines) != 201 || !strings.Contains(lines[1], "out-100") || strings.Contains(lines[1], "out-099") {
		t.Fatalf("expanded tail must be head+200 (last 200 of 300): %d lines, first=%q", len(lines), lines[1])
	}
	// 换行缓存随切换失效 → 视图同步后逐块缓存反映展开
	_ = m.View()
	if got := len(m.transcriptState.wrapBlockLines[idx]); got != 201 {
		t.Fatalf("wrap cache must reflect expansion: %d lines", got)
	}

	// 再按折叠
	m = update(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if card.expanded {
		t.Fatal("second Ctrl+B must collapse back")
	}
	if lines := card.render(40); len(lines) != 9 {
		t.Fatalf("re-collapsed tail must be head+8: %d lines", len(lines))
	}

	// 最近卡优先：开第二张小输出卡 → Ctrl+B 只切第二张
	m = update(t, m, tuiEvent{ev: event.ToolUseRequestedEvent{ToolUseID: "t2", Tool: "Glob", Input: json.RawMessage(`{"pattern":"*.go"}`)}})
	m = update(t, m, tuiEvent{ev: event.ToolResultEvent{ToolUseID: "t2", Content: "few lines"}})
	card1 := card
	card2 := m.transcriptState.blocks[m.cards["t2"]].(*toolCard)
	m = update(t, m, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !card2.expanded || card1.expanded {
		t.Fatalf("toggle must target the most recent card: card1=%v card2=%v", card1.expanded, card2.expanded)
	}

	// 失败卡排除 + 无目标 no-op
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.ToolUseRequestedEvent{ToolUseID: "f1", Tool: "Bash"}})
	m2 = update(t, m2, tuiEvent{ev: event.ToolResultEvent{ToolUseID: "f1", Content: "boom", IsError: true}})
	failed := m2.transcriptState.blocks[m2.cards["f1"]].(*toolCard)
	before := len(m2.transcriptState.blocks)
	m2 = update(t, m2, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if failed.expanded {
		t.Fatal("failed card must be excluded from toggle")
	}
	m3, _, _ := newTestModel(t)
	b3 := len(m3.transcriptState.blocks)
	m3 = update(t, m3, tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if len(m3.transcriptState.blocks) != b3 {
		t.Fatal("Ctrl+B without targets must no-op")
	}
	_ = before
}

// TestScrollbarDrag pins（Story4-d）：滚动条列按下开始拖拽（thumb 抓取偏移）
// → motion 映射视口偏移 → 松开收口；拖拽清活跃选区；非滚动条列点击走选区。
func TestScrollbarDrag(t *testing.T) {
	m, _, _ := newTestModel(t)
	for i := 0; i < 40; i++ {
		m.appendLine(fmt.Sprintf("row-%02d", i))
	}
	ts := &m.transcriptState
	ts.syncViewport(30, 10) // total=40 h=10 → thumbSize=2, maxTop=8, maxYoff=30
	cw := ts.vp.Width()     // 30 = 滚动条列

	// 活跃选区被拖拽清掉
	m = update(t, m, mouseClick(1, 1))
	if !ts.sel.active {
		t.Fatal("precondition: selection armed")
	}

	// thumb 当前在 [0,2)（yoff=0）→ 点击行 1 抓取偏移 1
	m = update(t, m, mouseClick(cw, 1))
	if !ts.scrollbarDrag || ts.sel.active {
		t.Fatalf("scrollbar press must start drag and clear selection: drag=%v sel=%+v", ts.scrollbarDrag, ts.sel)
	}
	if ts.scrollbarGrabOffset != 1 {
		t.Fatalf("grab offset = %d, want 1", ts.scrollbarGrabOffset)
	}
	// motion 行 5：top=min(5-1,8)=4 → yoff=(4*30+4)/8=15
	m = update(t, m, mouseMotion(cw, 5))
	if got := ts.vp.YOffset(); got != 15 {
		t.Fatalf("yoff = %d, want 15", got)
	}
	if m.scrollState.mode != scrollUserScrolled {
		t.Fatalf("drag off bottom must mark userScrolled, mode=%v", m.scrollState.mode)
	}

	// 松开收口
	m = update(t, m, mouseRelease(cw, 5))
	if ts.scrollbarDrag || ts.scrollbarGrabOffset != 0 {
		t.Fatalf("release must finalize drag: drag=%v grab=%d", ts.scrollbarDrag, ts.scrollbarGrabOffset)
	}

	// 非滚动条列点击 → 选区（不进拖拽）
	m = update(t, m, mouseClick(3, 2))
	if ts.scrollbarDrag || !ts.sel.active {
		t.Fatalf("non-scrollbar click must select: drag=%v sel=%+v", ts.scrollbarDrag, ts.sel)
	}
}

// TestScrollbarDragBottom pins：从底部 thumb 向上拖拽映射回中段偏移（抓取偏移
// 参与映射）；溢出缺席时滚动条列点击不进拖拽。
func TestScrollbarDragBottom(t *testing.T) {
	m, _, _ := newTestModel(t)
	for i := 0; i < 40; i++ {
		m.appendLine(fmt.Sprintf("row-%02d", i))
	}
	ts := &m.transcriptState
	ts.syncViewport(30, 10)
	cw := ts.vp.Width()

	// 底部：yoff=30 → thumbStart=min(30*8/30,8)=8 → 点击行 9 抓取偏移 1
	ts.vp.GotoBottom()
	m = update(t, m, mouseClick(cw, 9))
	if ts.scrollbarGrabOffset != 1 {
		t.Fatalf("grab offset = %d, want 1", ts.scrollbarGrabOffset)
	}
	// motion 行 3：top=min(3-1,8)=2 → yoff=(2*30+4)/8=8
	m = update(t, m, mouseMotion(cw, 3))
	if got := ts.vp.YOffset(); got != 8 {
		t.Fatalf("yoff = %d, want 8", got)
	}
	m = update(t, m, mouseRelease(cw, 3))

	// 无溢出：滚动条列点击不进拖拽（th=6 ≥ total）
	m2, _, _ := newTestModel(t)
	seedLines(m2, "only", "three", "lines")
	ts2 := &m2.transcriptState
	ts2.syncViewport(30, 6)
	m2 = update(t, m2, mouseClick(ts2.vp.Width(), 2))
	if ts2.scrollbarDrag {
		t.Fatal("no overflow → no scrollbar drag")
	}
	if !ts2.sel.active {
		t.Fatal("click falls through to selection when scrollbar is blank")
	}
}
