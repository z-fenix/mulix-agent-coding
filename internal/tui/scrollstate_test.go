package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"

	"mulix/internal/event"
)

// ---- 尾随状态机与滚动（023 D5；契约=设计 §7.2，参考 scroll_state.go/
// chat_tui.go:950-1012/1095-1118/1316-1333）----

// newScrollTestModel 造一个已首帧的滚动测试模型：80×24、60 行转写（视口必然
// 可滚动），首帧已喂给并钉底（followTail 默认态的落点）。
func newScrollTestModel(t *testing.T) *tuiModel {
	t.Helper()
	m, _, _ := newTestModel(t)
	m.width, m.height = 80, 24
	for i := 0; i < 60; i++ {
		m.appendLine(fmt.Sprintf("line-%02d %s", i, strings.Repeat("x", 40)))
	}
	m.View()
	if !m.shouldFollowTail() || !m.transcriptState.vp.AtBottom() {
		t.Fatalf("seed must start pinned at bottom in followTail: follow=%v yoff=%d/%d",
			m.shouldFollowTail(), m.transcriptState.vp.YOffset(), m.transcriptState.vp.Height())
	}
	return m
}

// TestScrollGestureStateMachine 钉手势状态机（brief Step 1a，设计 §7.2）：
// 显式滚离（滚轮上/PgUp/Ctrl+Home）→ userScrolled；回底（滚到底/PgDn 到底/
// Ctrl+End）→ followTail；forceGotoBottom 单帧豁免胜出、消费帧即清并恢复跟随。
func TestScrollGestureStateMachine(t *testing.T) {
	t.Run("leave-tail gestures mark userScrolled", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			msg  tea.Msg
		}{
			{"wheel up", tea.MouseWheelMsg{Button: tea.MouseWheelUp}},
			{"pgup", tea.KeyPressMsg{Code: tea.KeyPgUp}},
			{"ctrl+home", tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				m := newScrollTestModel(t)
				m = update(t, m, tt.msg)
				if m.scrollState.mode != scrollUserScrolled {
					t.Fatalf("%s must mark userScrolled, got %v", tt.name, m.scrollState.mode)
				}
				if m.shouldFollowTail() {
					t.Fatalf("%s must disable tail-follow", tt.name)
				}
			})
		}
	})

	t.Run("wheel down to bottom restores followTail", func(t *testing.T) {
		m := newScrollTestModel(t)
		m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp}) // 先滚离
		for i := 0; i < 100 && !m.transcriptState.vp.AtBottom(); i++ {
			m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		}
		if m.scrollState.mode != scrollFollowTail || !m.transcriptState.vp.AtBottom() {
			t.Fatalf("landing on the bottom must restore followTail: mode=%v yoff=%d/%d",
				m.scrollState.mode, m.transcriptState.vp.YOffset(), m.transcriptState.vp.Height())
		}
	})

	t.Run("pgdown to bottom restores followTail", func(t *testing.T) {
		m := newScrollTestModel(t)
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp}) // 先滚离
		for i := 0; i < 20 && !m.transcriptState.vp.AtBottom(); i++ {
			m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		if m.scrollState.mode != scrollFollowTail || !m.transcriptState.vp.AtBottom() {
			t.Fatalf("pgdown to the bottom must restore followTail: mode=%v yoff=%d/%d",
				m.scrollState.mode, m.transcriptState.vp.YOffset(), m.transcriptState.vp.Height())
		}
	})

	t.Run("ctrl+end restores followTail from the top", func(t *testing.T) {
		m := newScrollTestModel(t)
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl}) // 顶格
		if m.transcriptState.vp.YOffset() != 0 {
			t.Fatalf("ctrl+home must reach the top: yoff=%d", m.transcriptState.vp.YOffset())
		}
		m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
		if m.scrollState.mode != scrollFollowTail || !m.transcriptState.vp.AtBottom() {
			t.Fatalf("ctrl+end must restore followTail and pin bottom: mode=%v yoff=%d",
				m.scrollState.mode, m.transcriptState.vp.YOffset())
		}
	})

	t.Run("forceGotoBottom wins one frame then clears", func(t *testing.T) {
		m := newScrollTestModel(t)
		m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp}) // userScrolled
		m.scrollState.forceGotoBottom = true
		if !m.shouldFollowTail() {
			t.Fatal("forceGotoBottom must win for one frame even in userScrolled")
		}
		m.View() // 消费帧：置底 + 恢复跟随 + 清除
		if m.scrollState.forceGotoBottom {
			t.Fatal("forceGotoBottom must clear after one frame")
		}
		if m.scrollState.mode != scrollFollowTail || !m.transcriptState.vp.AtBottom() {
			t.Fatalf("force consumption must pin bottom and restore follow: mode=%v yoff=%d",
				m.scrollState.mode, m.transcriptState.vp.YOffset())
		}
	})
}

// TestScrollUserScrolledNotPulledBack 钉（brief Step 1c）：上翻锁定后新事件
// 到达视口不动（finalize 只在跟随态钉底——旧行为的无条件 GotoBottom 已废）；
// Ctrl+End 恢复尾随后新内容重新钉底。
func TestScrollUserScrolledNotPulledBack(t *testing.T) {
	m := newScrollTestModel(t)
	m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.shouldFollowTail() {
		t.Fatal("precondition: wheel-up must leave followTail")
	}
	off := m.transcriptState.vp.YOffset()
	if off == 0 {
		t.Fatal("precondition: wheel-up must have scrolled away from the bottom")
	}
	// 新事件到达：转写增长，视口偏移不动
	m = update(t, m, tuiEvent{ev: event.NoticeEvent{Content: "稍后到达的内容"}})
	if got := m.transcriptState.vp.YOffset(); got != off {
		t.Fatalf("new content must not yank the viewport: %d → %d", off, got)
	}
	m.View()
	if m.transcriptState.vp.AtBottom() {
		t.Fatal("userScrolled viewport must stay away from the bottom after new content")
	}
	// Ctrl+End 回底恢复跟随；后续新事件钉底
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	if !m.shouldFollowTail() || !m.transcriptState.vp.AtBottom() {
		t.Fatal("ctrl+end must restore followTail and pin bottom")
	}
	m = update(t, m, tuiEvent{ev: event.NoticeEvent{Content: "恢复跟随后的内容"}})
	m.View()
	if !m.transcriptState.vp.AtBottom() {
		t.Fatal("followTail must pin new content to the bottom")
	}
}

// TestModalOpenNoUnfollow 钉（brief Step 1b——参考 #6430/#6978 回归钉）：
// 授权卡/agents 面板开合只改变底部区高度（视口变矮/变高）——height-only 变化
// 永不翻转尾随模式；跟随态下开模态仍钉底；模态开着新内容照常钉底；模态独占
// 期滚动手势不泄漏（D10/D11：治理独占期交互冻结）。
func TestModalOpenNoUnfollow(t *testing.T) {
	m := newScrollTestModel(t)
	// 授权卡打开（height-only：底部区 composer 行 → 卡片多行）
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t1", Tool: "Bash"}})
	if m.approval == nil || !m.modalOpen() {
		t.Fatal("approval card must report modalOpen")
	}
	if m.scrollState.mode != scrollFollowTail {
		t.Fatalf("modal open must not flip the mode (#6430): %v", m.scrollState.mode)
	}
	m.View() // 开卡后首帧：视口变矮，跟随态仍钉底
	if !m.transcriptState.vp.AtBottom() {
		t.Fatalf("followTail must re-pin after modal shrinks the viewport: yoff=%d h=%d",
			m.transcriptState.vp.YOffset(), m.transcriptState.vp.Height())
	}
	// 模态开着，新内容到达仍钉底
	m = update(t, m, tuiEvent{ev: event.NoticeEvent{Content: "模态期间的流式输出"}})
	m.View()
	if !m.transcriptState.vp.AtBottom() {
		t.Fatal("new output while modal open must keep the followTail pin")
	}
	// 模态独占期滚动手势不泄漏（滚轮/PgUp 都不可达转写）
	yoff := m.transcriptState.vp.YOffset()
	m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.scrollState.mode != scrollFollowTail || m.transcriptState.vp.YOffset() != yoff {
		t.Fatalf("scroll gestures must not reach the transcript while a modal is open: mode=%v yoff=%d→%d",
			m.scrollState.mode, yoff, m.transcriptState.vp.YOffset())
	}
	// 关卡（height-only）不翻模式；视口变高，跟随态钉底
	m = update(t, m, keyPress('a'))
	if m.approval != nil || m.modalOpen() {
		t.Fatal("card must clear after resolve")
	}
	if m.scrollState.mode != scrollFollowTail {
		t.Fatalf("modal close must not flip the mode: %v", m.scrollState.mode)
	}
	m.View()
	if !m.transcriptState.vp.AtBottom() {
		t.Fatal("followTail must pin after modal closes")
	}

	// agents 面板（豁免集第二项）——直置面板态（面板开合流程由既有 agents
	// 测试钉），开合同样不翻模式且保持钉底。
	m2 := newScrollTestModel(t)
	m2.agentsOpen = true
	if !m2.modalOpen() {
		t.Fatal("agents panel must report modalOpen")
	}
	m2.View()
	if m2.scrollState.mode != scrollFollowTail || !m2.transcriptState.vp.AtBottom() {
		t.Fatalf("agents panel open must not break followTail pin: mode=%v atBottom=%v",
			m2.scrollState.mode, m2.transcriptState.vp.AtBottom())
	}
	m2.agentsOpen = false
	m2.View()
	if m2.scrollState.mode != scrollFollowTail || !m2.transcriptState.vp.AtBottom() {
		t.Fatal("agents panel close must not break followTail pin")
	}
}

// TestScrollSwapRestoresFollow 钉（设计 §7.2：会话切换经 forceGotoBottom 回底
// 恢复跟随；Task 3 遗留的换绑窗口针对性测试）：上翻锁定下换绑收口——收口
// 回执武装单帧强制、消费帧置底并恢复跟随、force 即清。
func TestScrollSwapRestoresFollow(t *testing.T) {
	m := newScrollTestModel(t)
	m = update(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.shouldFollowTail() {
		t.Fatal("precondition: wheel-up must leave followTail")
	}
	newCtrl := &stubController{}
	m.swapper = &fakeSwapper{sink: m.sink, ctrl: newCtrl, id: "s-new"}
	m = update(t, m, typeStr(m, "/new"))
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.scrollState.forceGotoBottom {
		t.Fatal("swap receipt must arm forceGotoBottom")
	}
	m.View() // 消费帧
	if m.scrollState.forceGotoBottom || m.scrollState.mode != scrollFollowTail || !m.transcriptState.vp.AtBottom() {
		t.Fatalf("swap must pin bottom and restore follow for one frame: force=%v mode=%v atBottom=%v",
			m.scrollState.forceGotoBottom, m.scrollState.mode, m.transcriptState.vp.AtBottom())
	}
}
