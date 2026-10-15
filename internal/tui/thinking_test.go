package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/bubbletea/v2"

	"mulix/internal/event"
)

// ---- Task 8（023 D14 / FR-004）：思考块有界尾窗与折叠 ----

// TestTailWindowBound pins utf8SafeTail：保尾截断——超 maxBytes 从头部丢字节
// （丢到 RuneStart 边界，不劈半字符），超 maxLines 丢头部行；两个界同时成立。
// 对照参考 streamReasoning（chat_tui.go:2358-2365）的头丢弃 + RuneStart 对齐。
func TestTailWindowBound(t *testing.T) {
	t.Run("超长 ASCII 截到 maxBytes", func(t *testing.T) {
		in := strings.Repeat("a", 5000)
		got := utf8SafeTail(in, 4096, 12)
		if len(got) != 4096 {
			t.Fatalf("len = %d, want 4096", len(got))
		}
		if strings.Trim(got, "a") != "" {
			t.Fatalf("tail must keep the trailing bytes, got %q", got[:16]+"…")
		}
	})
	t.Run("UTF-8 不截半字符", func(t *testing.T) {
		in := strings.Repeat("思", 1400) // 3B × 1400 = 4200B > 4096，字节界落在符内
		got := utf8SafeTail(in, 4096, 0)
		if len(got) > 4096 {
			t.Fatalf("len = %d, want <= 4096", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatal("tail must not split a UTF-8 rune")
		}
		if !strings.HasSuffix(in, got) {
			t.Fatal("tail must be a complete suffix of the input（保尾）")
		}
	})
	t.Run("恰好等于上限不截", func(t *testing.T) {
		in := strings.Repeat("x", 4096)
		if got := utf8SafeTail(in, 4096, 12); got != in {
			t.Fatal("input at the bound must pass through unchanged")
		}
	})
	t.Run("超行保尾 12 行", func(t *testing.T) {
		lines := make([]string, 20)
		for i := range lines {
			lines[i] = fmt.Sprintf("l%d", i)
		}
		in := strings.Join(lines, "\n")
		got := utf8SafeTail(in, 1<<20, 12)
		if want := strings.Join(lines[8:], "\n"); got != want { // 最后 12 行 = l8..l19
			t.Fatalf("line cap must keep the trailing lines:\ngot  %q\nwant %q", got, want)
		}
	})
	t.Run("字节与行界同时成立", func(t *testing.T) {
		in := strings.Repeat(strings.Repeat("x", 500)+"\n", 20) // 10020B / 20 行
		got := utf8SafeTail(in, 4096, 12)
		if len(got) > 4096 || strings.Count(got, "\n") >= 12 {
			t.Fatalf("both bounds must hold: len=%d lines=%d", len(got), strings.Count(got, "\n")+1)
		}
	})
	t.Run("短输入原样返回", func(t *testing.T) {
		if got := utf8SafeTail("短\n文", 4096, 12); got != "短\n文" {
			t.Fatalf("short input must pass through: %q", got)
		}
	})
}

// TestThinkingCollapse pins（D14 块生命周期）：思考流 rewrite 同块不增块 →
// 下一非思考事件或回合收口折叠为单行「✓ 已思考 Ns · N tok」——Ns = 首末思考
// 事件到达的本地钟差，tok = 回执回填（Task 17 D16 接线 noteThinkingTokens）。
func TestThinkingCollapse(t *testing.T) {
	// (a) 流式：分片 rewrite 同一块（Task 4 rewriteLast 路径），尾窗含全文尾部
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Thinking: "alpha "}})
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 2, Thinking: "beta"}})
	if len(m.transcriptState.blocks) != 1 {
		t.Fatalf("thinking stream must rewrite one block in place, blocks = %d", len(m.transcriptState.blocks))
	}
	tb, ok := m.transcriptState.blocks[0].(thinkingBlock)
	if !ok || tb.collapsed {
		t.Fatalf("live block must be an uncollapsed thinkingBlock (ok=%v collapsed=%v)", ok, tb.collapsed)
	}
	if !strings.Contains(tb.tail, "alpha") || !strings.Contains(tb.tail, "beta") {
		t.Fatalf("tail window lost stream content: %q", tb.tail)
	}

	// (b) 回合收口折叠：Ns = 本地钟差、tok = 回执回填（收口前到账 → pending 落槽）
	m.thinkStart = m.thinkStart.Add(-3 * time.Second) // 白盒：把首事件到达拨回 3s
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 3, Thinking: "more"}})
	m.noteThinkingTokens(0, 45) // 思考事件 Turn 未设（0）——同回合守卫放行
	m = update(t, m, tuiEvent{ev: event.TurnDoneEvent{Seq: 4, Subtype: "success"}})
	if len(m.transcriptState.blocks) != 2 { // 折叠块 + 回合完成收口行
		t.Fatalf("collapse must not add blocks beyond the system line, blocks = %d", len(m.transcriptState.blocks))
	}
	tb, ok = m.transcriptState.blocks[0].(thinkingBlock)
	if !ok || !tb.collapsed || tb.tokens != 45 {
		t.Fatalf("turn close must collapse with tokens=45 (ok=%v collapsed=%v tokens=%d)", ok, tb.collapsed, tb.tokens)
	}
	if line := tb.render(80)[0]; !strings.Contains(line, "✓ 已思考 3s · 45 tok") {
		t.Fatalf("collapsed line = %q, want ✓ 已思考 3s · 45 tok", line)
	}

	// (c) 非思考事件折叠（Notice 打断）+ token 槽位缺省省略
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Thinking: "thought"}})
	m2 = update(t, m2, tuiEvent{ev: event.NoticeEvent{Seq: 2, Level: "info", Content: "n"}})
	tb2, ok := m2.transcriptState.blocks[0].(thinkingBlock)
	if !ok || !tb2.collapsed {
		t.Fatalf("next non-thinking event must collapse the block (ok=%v collapsed=%v)", ok, tb2.collapsed)
	}
	want := fmt.Sprintf("✓ 已思考 %ds", int64(tb2.endedAt.Sub(tb2.startedAt).Seconds()))
	if line := tb2.render(80)[0]; !strings.Contains(line, want) || strings.Contains(line, "tok") {
		t.Fatalf("collapsed line = %q, want %q without token slot", line, want)
	}

	// (d) 收口后 token 回填（Task 17 到账晚于收口的路径：usage → done 已折叠）
	m2.noteThinkingTokens(0, 7) // 思考事件 Turn 未设（0）——同回合守卫放行
	tb2, ok = m2.transcriptState.blocks[0].(thinkingBlock)
	if !ok || tb2.tokens != 7 {
		t.Fatalf("post-collapse backfill must stamp the latest thinking block, tokens = %d", tb2.tokens)
	}
	if line := tb2.render(80)[0]; !strings.Contains(line, "· 7 tok") {
		t.Fatalf("backfilled line = %q, want · 7 tok", line)
	}

	// (e) 折叠后新思考开新块（块生命周期重启）
	m2 = update(t, m2, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 3, Thinking: "again"}})
	if len(m2.transcriptState.blocks) != 3 {
		t.Fatalf("new thinking after collapse must open a new block, blocks = %d", len(m2.transcriptState.blocks))
	}
	nb, ok := m2.transcriptState.blocks[2].(thinkingBlock)
	if !ok || nb.collapsed {
		t.Fatalf("reopened thinking must stream uncollapsed (ok=%v collapsed=%v)", ok, nb.collapsed)
	}
	if !strings.Contains(nb.tail, "again") {
		t.Fatalf("reopened block lost its stream: %q", nb.tail)
	}
}

// TestBusyGateDoesNotSplitThinking pins（终审 Important：appendLine 绕过块生命
// 周期纪律 → 活思考块被系统行劈裂）：回合进行中（思考流式激活）触发任一本地系
// 统行（最常见：Enter 撞忙门禁）必须与 handleEvent 同纪律——先收口思考块、封
// 口流式答案，再落系统行。否则活块被顶离末位永久保持展开，下一思考事件走防御
// 分支另开新块，收口时同一思考「尾窗 + 折叠行」并存（P3 冒烟 216.0s 帧实证）。
func TestBusyGateDoesNotSplitThinking(t *testing.T) {
	// (a) 忙门禁系统行先收口活思考块（原地折叠，不被顶离末位）
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Turn: 1, Thinking: "deep"}})
	m.running = true // 回合进行中（生产：submitCmd 置位，收口回执清除）
	m = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.thinkingIdx != -1 {
		t.Fatalf("system line must close the live thinking block first, thinkingIdx = %d", m.thinkingIdx)
	}
	last := m.transcriptState.blocks[len(m.transcriptState.blocks)-1]
	if pb, ok := last.(plainBlock); !ok || !strings.Contains(pb.text, "[忙]") {
		t.Fatalf("busy notice must be the last block, got %T", last)
	}

	// (b) 劈裂的重复思考形态：下一思考事件另开新块 → 回合收口后不得残留
	// 未折叠思考块（孤儿尾窗）。
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 2, Turn: 1, Thinking: "more"}})
	m = update(t, m, tuiEvent{ev: event.TurnDoneEvent{Seq: 3, Turn: 1, Subtype: "success"}})
	for i, b := range m.transcriptState.blocks {
		if tb, ok := b.(thinkingBlock); ok && !tb.collapsed {
			t.Fatalf("turn close must leave no uncollapsed thinking block (orphan at block %d)", i)
		}
	}

	// (c) 次序倒挂：先到答文（半段滞留 pending 未落块）后插系统行——答文块
	// 必须先于系统行落块（封口在前）。
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "未完段落"}})
	if m2.pendingMD == "" {
		t.Fatal("precondition: partial answer text must stay pending before the system line")
	}
	m2.running = true
	m2 = update(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	ansIdx, busyIdx := -1, -1
	for i, b := range m2.transcriptState.blocks {
		switch x := b.(type) {
		case assistantBlock:
			if strings.Contains(x.text, "未完段落") && ansIdx < 0 {
				ansIdx = i
			}
		case plainBlock:
			if strings.Contains(x.text, "[忙]") && busyIdx < 0 {
				busyIdx = i
			}
		}
	}
	if ansIdx < 0 || busyIdx < 0 || ansIdx > busyIdx {
		t.Fatalf("answer text that arrived first must land before the system line: ansIdx=%d busyIdx=%d", ansIdx, busyIdx)
	}
}

// TestNoZeroTokenRender pins（D14）：EstimatedTokens 恒零（control 层硬编码 0，
// controller.go:1293-1295）——流式与折叠都不得渲染任何「0 tok」；字段即便非零
// 也绝不采纳（真实 token 只来自回合末 UsageEvent.ThinkingTokens 回填）。
func TestNoZeroTokenRender(t *testing.T) {
	assertNoTok := func(t *testing.T, b thinkingBlock, stage string) {
		t.Helper()
		rendered := strings.Join(b.render(80), "\n")
		if strings.Contains(rendered, "tok") {
			t.Fatalf("%s must not render any token count:\n%s", stage, rendered)
		}
	}
	m, _, _ := newTestModel(t)
	m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Thinking: "deep", EstimatedTokens: 0}})
	tb := m.transcriptState.blocks[len(m.transcriptState.blocks)-1].(thinkingBlock)
	assertNoTok(t, tb, "streaming view")
	m = update(t, m, tuiEvent{ev: event.TurnDoneEvent{Seq: 2, Subtype: "success"}})
	tb = m.transcriptState.blocks[0].(thinkingBlock)
	if !tb.collapsed {
		t.Fatal("turn close must collapse")
	}
	assertNoTok(t, tb, "collapsed view")

	// 防御：EstimatedTokens 非零也绝不渲染（恒零字段，渲染即死 UI）
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Thinking: "x", EstimatedTokens: 42}})
	m2 = update(t, m2, tuiEvent{ev: event.TurnDoneEvent{Seq: 2, Subtype: "success"}})
	tb2 := m2.transcriptState.blocks[0].(thinkingBlock)
	assertNoTok(t, tb2, "collapsed view with nonzero EstimatedTokens")
}
