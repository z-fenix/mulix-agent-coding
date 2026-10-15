package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/bubbletea/v2"

	"mulix/internal/event"
)

// ---- 键位语义测试（Task 10 D10/FR-011：取消语义阶梯）----
//
// brief Step 1 五组钉测试 + 阶梯补钉：
//   TestBubbleCommitsOnTurnStarted      气泡回合开始才上屏（Enter 只置在途）
//   TestEscBeforeTurnStartedDiscards    忙碌 Esc（气泡未上屏）= 丢弃提交+还原草稿
//   TestEscRunningCancels               忙碌 Esc（回合进行中）= Cancel 恰一次
//   TestCancelledTurnRendersPausedLine  取消收口行凭本地意图（不看引擎回执形态）
//   TestApprovalCtrlCQuits              授权卡独占态单击 Ctrl+C 退出（Minor-4）
//   TestCtrlCLadder / TestEscIdleClearsInput / TestSwapClearsCancelState（补钉）

// userBubbleCount 统计转写块流中的用户气泡数。白盒计数而非视图包含性断言：
// 丢弃路径还原草稿后 composer 也含同文（"> hi"），视图断言无法区分草稿与气泡。
func userBubbleCount(m *tuiModel) int {
	n := 0
	for _, b := range m.transcriptState.blocks {
		if _, ok := b.(userBlock); ok {
			n++
		}
	}
	return n
}

// TestBubbleCommitsOnTurnStarted 钉 D10 上屏时机：Enter 只置 pendingSubmit
// （转写无气泡），TurnStartedEvent 到达才提交 userBlock 并清 pending。
func TestBubbleCommitsOnTurnStarted(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, keyPress('h'))
	m = update(t, m, keyPress('i'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pendingSubmit == nil || m.pendingSubmit.text != "hi" {
		t.Fatalf("Enter must arm pendingSubmit: %+v", m.pendingSubmit)
	}
	if n := userBubbleCount(m); n != 0 {
		t.Fatalf("bubble must not commit at Enter: %d", n)
	}
	if strings.Contains(m.View().Content, "hi") {
		t.Fatalf("bubble must not render before TurnStarted:\n%s", m.View().Content)
	}
	m = update(t, m, tuiEvent{ev: event.TurnStartedEvent{Seq: 1}})
	if n := userBubbleCount(m); n != 1 {
		t.Fatalf("bubble must commit exactly once on TurnStarted: %d", n)
	}
	if m.pendingSubmit != nil {
		t.Fatal("pendingSubmit must clear after commit")
	}
	if !strings.Contains(m.View().Content, "hi") {
		t.Fatalf("committed bubble must render:\n%s", m.View().Content)
	}
}

// TestEscBeforeTurnStartedDiscards 钉：气泡未上屏时 Esc = 丢弃在途提交——
// 草稿还原、pending 清空、不触发 Cancel；迟到的 TurnStarted 为 no-op（代际
// 纪律：无气泡）；迟到的提交收据作废（running 清除——回合确实收口——但不渲染
// [提交失败]）。对照组：未丢弃的失败提交照常留痕（代际匹配）。
func TestEscBeforeTurnStartedDiscards(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, keyPress('h'))
	m = update(t, m, keyPress('i'))
	// 直调 submit 并丢弃回执命令（模拟在途窗口：Submit 已派发、TurnStarted 未
	// 到、收据未回——update 辅助同步执行回执会立即清 running，故不走 update）。
	if _, cmd := m.submit(); cmd == nil {
		t.Fatal("precondition: submit must dispatch a command")
	}
	if m.pendingSubmit == nil || !m.running {
		t.Fatalf("precondition: in-flight submit (pending=%+v running=%v)", m.pendingSubmit, m.running)
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.composerValue() != "hi" {
		t.Fatalf("draft must be restored: %q", m.composerValue())
	}
	if m.pendingSubmit != nil {
		t.Fatal("pendingSubmit must clear on discard")
	}
	if n := userBubbleCount(m); n != 0 {
		t.Fatalf("discard must not commit bubble: %d", n)
	}
	if stub.cancels != 0 {
		t.Fatalf("discard path must not Cancel: %d", stub.cancels)
	}
	// 迟到的 TurnStarted（丢弃竞态）：pending 已清 → no-op——无气泡。
	m = update(t, m, tuiEvent{ev: event.TurnStartedEvent{Seq: 1}})
	if n := userBubbleCount(m); n != 0 {
		t.Fatalf("late TurnStarted after discard must not commit bubble: %d", n)
	}
	// 迟到的提交收据（代际已作废）：清 running，但不渲染 [提交失败]。
	m = update(t, m, submitDoneMsg{gen: 1, err: errors.New("boom")})
	if m.running {
		t.Fatal("stale receipt must still clear running (turn really ended)")
	}
	if strings.Contains(m.View().Content, "[提交失败]") {
		t.Fatalf("discarded receipt must not render submit error:\n%s", m.View().Content)
	}
	// 对照组：未丢弃的失败提交（代际匹配）照常留痕。
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, keyPress('x'))
	if _, cmd := m2.submit(); cmd == nil {
		t.Fatal("precondition: submit must dispatch a command")
	}
	m2 = update(t, m2, submitDoneMsg{gen: 1, err: errors.New("boom")})
	if !strings.Contains(m2.View().Content, "[提交失败]") {
		t.Fatalf("live failed submit must render error:\n%s", m2.View().Content)
	}
}

// TestEscRunningCancels 钉：回合进行中（气泡已上屏、pending 已消费）Esc =
// 取消意图——ctrl.Cancel 恰好一次（异步命令经测试辅助同步回执）、cancelIntent
// 置位；重复 Esc 幂等（取消已在途不重发）；取消回执成功静默，意图保持到收口。
func TestEscRunningCancels(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m.running = true // 回合进行中（TestBusyGate 同款：模拟异步提交在途）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if stub.cancels != 1 {
		t.Fatalf("Cancel calls = %d, want exactly 1", stub.cancels)
	}
	if !m.cancelIntent {
		t.Fatal("cancelIntent must latch on Esc-cancel")
	}
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if stub.cancels != 1 {
		t.Fatalf("repeat Esc must not re-Cancel: %v", stub.cancels)
	}
	m = update(t, m, cancelDoneMsg{})
	if !m.cancelIntent {
		t.Fatal("cancelIntent must persist until TurnDone (success receipt is silent)")
	}
}

// TestCancelledTurnRendersPausedLine 钉：本地取消意图下回合收口渲染
// 「⏹ 回合已取消 tN」——不看 TurnDone.Subtype/IsError（引擎中断后的回执形态
// 无 fixture 钉死，绝不渲染为「回合失败」）；意图消费即清。对照组：无取消
// 意图时既有 [回合完成] 行不变。
func TestCancelledTurnRendersPausedLine(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.cancelIntent = true
	m = update(t, m, tuiEvent{ev: event.TurnDoneEvent{
		Seq: 1, Turn: 7, Subtype: "interrupted", IsError: true,
	}})
	view := m.View().Content
	if !strings.Contains(view, "⏹ 回合已取消 t7") {
		t.Fatalf("cancelled close line missing:\n%s", view)
	}
	if strings.Contains(view, "回合失败") || strings.Contains(view, "回合完成") {
		t.Fatalf("cancelled turn must not render engine-shape close lines:\n%s", view)
	}
	if m.cancelIntent {
		t.Fatal("cancelIntent must clear after the cancelled close")
	}
	// 对照组：无取消意图 → 既有收口语义不变。
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.TurnDoneEvent{Seq: 2, Turn: 8, Subtype: "success"}})
	if !strings.Contains(m2.View().Content, "[回合完成 t8]") {
		t.Fatalf("normal close line missing:\n%s", m2.View().Content)
	}
}

// TestApprovalCtrlCQuits 钉（Minor-4 逃生口，迁自现语义）：授权卡独占态单击
// Ctrl+C 直接退出——不走渲染态双击阶梯（审批态不可困死用户）。
func TestApprovalCtrlCQuits(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t10", Tool: "Bash"}})
	if m.approval == nil {
		t.Fatal("precondition: approval card must show")
	}
	if _, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Fatal("single Ctrl+C in approval state must quit (Minor-4)")
	}
}

// TestCtrlCLadder 钉渲染态 Ctrl+C 阶梯（FR-011 防误触）：有输入 → 清空（清空
// 按不武装）；空输入首按 → 武装（不退）；窗口外二按 → 重武装（不退）；窗口内
// 二按 → 退出。选区复制随 Task 21 接入（P3 前恒走本阶梯）。
func TestCtrlCLadder(t *testing.T) {
	m, _, _ := newTestModel(t)
	ctrlC := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	// 有输入 → 清空，不退出，不武装
	m = update(t, m, keyPress('x'))
	if _, cmd := m.handleKey(ctrlC); cmd != nil {
		t.Fatal("Ctrl+C with input must clear, not quit")
	}
	if m.composerValue() != "" {
		t.Fatalf("input must clear: %q", m.composerValue())
	}
	if !m.quitArm.IsZero() {
		t.Fatal("clearing press must not arm the quit timer")
	}
	// 空输入首按 → 武装（不退）
	if _, cmd := m.handleKey(ctrlC); cmd != nil {
		t.Fatal("first Ctrl+C on empty input must arm, not quit")
	}
	if m.quitArm.IsZero() {
		t.Fatal("first Ctrl+C on empty input must arm the quit timer")
	}
	// 窗口外二按 → 重武装（不退）
	m.quitArm = time.Now().Add(-2 * time.Second)
	if _, cmd := m.handleKey(ctrlC); cmd != nil {
		t.Fatal("Ctrl+C outside the window must re-arm, not quit")
	}
	if m.quitArm.IsZero() || time.Since(m.quitArm) > quitArmWindow {
		t.Fatal("outside-window press must re-arm the timer")
	}
	// 窗口内二按 → 退出
	if _, cmd := m.handleKey(ctrlC); cmd == nil {
		t.Fatal("second Ctrl+C within the window must quit")
	}
}

// TestEscIdleClearsInput 钉阶梯末档：空闲 Esc = 清空输入（不触发 Cancel）。
func TestEscIdleClearsInput(t *testing.T) {
	m, stub, _ := newTestModel(t)
	m = update(t, m, keyPress('a'))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.composerValue() != "" {
		t.Fatalf("idle Esc must clear input: %q", m.composerValue())
	}
	if stub.cancels != 0 {
		t.Fatalf("idle Esc must not Cancel: %v", stub.cancels)
	}
}

// TestSwapClearsCancelState 钉：换绑回执作废旧会话的取消/在途状态（成败两路
// 同清）。旧 pendingSubmit 若不清，新会话首个 TurnStarted 会把旧稿错挂上新
// 转写（旧会话事件已被排空，气泡等不到正确的回合）；旧 cancelIntent 若不清，
// 新会话收口会错渲染 ⏹。残态形态 = 回执已清 running 而 pending/意图未消费的
// 窗口（生产路径：Submit 回执先于 TurnStarted 排空到达）——running 置位反而
// 会被 /new 忙门禁拦下，故不设。
func TestSwapClearsCancelState(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.cancelIntent = true
	m.pendingSubmit = &pendingSubmit{text: "旧会话残稿", gen: 1}
	newCtrl := &stubController{}
	sw := &fakeSwapper{sink: m.sink, ctrl: newCtrl, id: "s-swap"}
	m.swapper = sw
	m = update(t, m, typeStr(m, "/new"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pendingSubmit != nil || m.cancelIntent {
		t.Fatal("swap receipt must clear old-session cancel state")
	}
	m = update(t, m, tuiEvent{ev: event.TurnStartedEvent{Seq: 2}})
	if n := userBubbleCount(m); n != 0 {
		t.Fatalf("stale pendingSubmit must not commit into the new session: %d", n)
	}
}
