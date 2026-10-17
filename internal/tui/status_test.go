package tui

import (
	"strings"
	"testing"

	"mulix/internal/control"
	"mulix/internal/event"
)

// ---- Task 17（023 FR-012 / D16 / D7）：回合回执 + 双行状态栏 ----
//
// 三组钉：
//   - TestTurnReceipt——UsageEvent 落回合回试行（字段齐全、答案下方）+ 思考
//     token 回填同回合思考块（Task 8 接缝闭环；跨回合守卫不回填）；
//   - TestStatusBarTwoRows——双行版式（对照参考 status_footer.go）：行 1 交互
//     态左 + 模型·会话 id8 右对齐，行 2 数据带（命中率来自 Snapshot）；
//   - TestContextGaugeFallback——D7 兜底：Snapshot 无上下文字段时只渲染可得
//     子集，绝不造数（上下文仪表在快照字段落地前不渲染）。

// TestTurnReceipt 钉（D16）：UsageEvent → 回试行落转写（该回合答案下方），
// 字段 = 入/写/读/出/思 tok · 成本 · 命中率（Task 13 口径：UncachedInputTokens/
// WriteTokens/ReadTokens/OutputTokens/ThinkingTokens/CostUSD/HitRate）；思考
// token 回填同回合思考块（usage 先于 TurnDone 到达，块已收口——回填最近一个
// 同回合思考块，controller.go:1357→1369）。
func TestTurnReceipt(t *testing.T) {
	t.Run("回执行字段齐全且落答案下方", func(t *testing.T) {
		m, _, _ := newTestModel(t)
		m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Turn: 1, Thinking: "推理"}})
		m = update(t, m, tuiEvent{ev: event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "第一段。\n\n尾段"}})
		m = update(t, m, tuiEvent{ev: event.UsageEvent{Seq: 3, Turn: 1,
			UncachedInputTokens: 100, WriteTokens: 20, ReadTokens: 300, OutputTokens: 50,
			ThinkingTokens: 42, CostUSD: 0.0123, HitRate: 0.72}})
		joined := strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
		for _, want := range []string{
			"────", "本轮  512 tok · in 100 · cached 300 · new 20 · out 50 · reasoning 42", "≈$0.0123",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("receipt missing %q\n--- transcript ---\n%s", want, joined)
			}
		}
		// 布局：思考块 → 答案（含冲刷尾段——usage 先于 TurnDone，不冲刷会让
		// 尾段落进回执之后）→ 回试行（末块）。
		if len(m.transcriptState.blocks) != 3 {
			t.Fatalf("thinking + answer + receipt = 3 blocks, got %d\n%s", len(m.transcriptState.blocks), joined)
		}
		if ab, ok := m.transcriptState.blocks[1].(assistantBlock); !ok || !strings.Contains(ab.text, "尾段") {
			t.Fatalf("answer block must precede the receipt, got %#v", m.transcriptState.blocks[1])
		}
		if _, ok := m.transcriptState.blocks[2].(receipt); !ok {
			t.Fatalf("last block must be the receipt, got %#v", m.transcriptState.blocks[2])
		}
		// 思考 token 回填：同回合 usage 落进刚收口的思考块（Task 8 接缝闭环）。
		if tb, ok := m.transcriptState.blocks[0].(thinkingBlock); !ok || tb.tokens != 42 {
			t.Fatalf("same-turn thinking block must be backfilled to 42, got %#v", m.transcriptState.blocks[0])
		}
	})

	t.Run("跨回合守卫不回填旧思考块", func(t *testing.T) {
		m, _, _ := newTestModel(t)
		m = update(t, m, tuiEvent{ev: event.AssistantThinkingEvent{Seq: 1, Turn: 1, Thinking: "推理"}})
		m = update(t, m, tuiEvent{ev: event.UsageEvent{Seq: 2, Turn: 1, ThinkingTokens: 42}})
		// t2 无思考块：t2 用量不得回填 t1 的思考块（归属守卫）。
		m = update(t, m, tuiEvent{ev: event.UsageEvent{Seq: 3, Turn: 2, ThinkingTokens: 7}})
		if tb, ok := m.transcriptState.blocks[0].(thinkingBlock); !ok || tb.tokens != 42 {
			t.Fatalf("turn-1 block must keep its own tokens, got %#v", m.transcriptState.blocks[0])
		}
		joined := strings.Join(flattenTranscript(&m.transcriptState, 80), "\n")
		if !strings.Contains(joined, "本轮  42 tok") || !strings.Contains(joined, "本轮  7 tok") {
			t.Fatalf("turn-2 receipt missing:\n%s", joined)
		}
	})
}

// TestStatusBarTwoRows 钉（FR-012）：状态栏两行——行 1 交互态（左）+「模型 ·
// 会话 id8」（右，宽端右对齐，对照参考 layoutStatusSides）；行 2 数据带：命中
// 率/回合累计/成本/冷回合（全部来自 500ms Snapshot 轮询，D7）。
func TestStatusBarTwoRows(t *testing.T) {
	m, _, _ := newTestModel(t)
	m = update(t, m, statusTick{}) // 数据由 tick 拉快照（stub：SessionHit 0.82 / Turn 3 / 420 tok）
	rows := strings.Split(m.statusView(), "\n")
	if len(rows) != 2 {
		t.Fatalf("status bar must be exactly two rows, got %d:\n%s", len(rows), m.statusView())
	}
	// 行 1：模式徽章（accent 反白 pill——025 FR-006）+「模型 <model>」右对齐；
	// 会话 id8 不再上状态栏。
	if !strings.Contains(rows[0], "空闲") || !strings.Contains(rows[0], "模型 fake-model") {
		t.Fatalf("row1 = %q, want 徽章(空闲) + 模型 fake-model", rows[0])
	}
	if strings.Contains(rows[0], "s-x") {
		t.Fatalf("row1 must not carry session id8: %q", rows[0])
	}
	// 行 2：缓存命中率（本次=TurnTokens.CacheRead/(Input+Write+Read)=75.0%、
	// 平均=SessionHit 82.0%）+ 费用 + 冷回合；回合累计/旧命中率字段退场。
	for _, want := range []string{"缓存 本次命中 75.0%", "平均 82.0%", "费用 $1.2500", "冷回合 1"} {
		if !strings.Contains(rows[1], want) {
			t.Fatalf("row2 missing %q:\n%s", want, rows[1])
		}
	}

	// 运行中 → 行 1 交互态切换。
	m2, stub, _ := newTestModel(t)
	stub.snap.Running = true
	m2 = update(t, m2, statusTick{})
	if r1 := strings.Split(m2.statusView(), "\n")[0]; !strings.Contains(r1, "运行中") {
		t.Fatalf("running mode missing from row1: %q", r1)
	}

	// 无冷回合 → 冷回合标注整体省略。
	// 027 FR-003：引擎就绪徽章三态。
	m4, _, _ := newTestModel(t)
	m4.statusState.snapshot.Ready = "NOT_READY"
	m4.statusState.snapshot.Running = false
	if r1 := strings.Split(m4.statusView(), "\n")[0]; !strings.Contains(r1, "引擎就绪中") {
		t.Fatalf("NOT_READY badge missing: %q", r1)
	}
	m4.statusState.snapshot.Ready = "FAILED"
	if r1 := strings.Split(m4.statusView(), "\n")[0]; !strings.Contains(r1, "引擎异常") {
		t.Fatalf("FAILED badge missing: %q", r1)
	}
	m4.statusState.snapshot.Ready = "READY"
	m4.statusState.snapshot.Running = true
	if r1 := strings.Split(m4.statusView(), "\n")[0]; !strings.Contains(r1, "运行中") {
		t.Fatalf("READY+running must show running mode: %q", r1)
	}
	m3, _, _ := newTestModel(t)
	m3 = update(t, m3, statusTick{})
	m3.statusState.snapshot.MeterReport.ColdTurns = 0
	if strings.Contains(m3.statusView(), "冷") {
		t.Fatal("cold marker must be absent without cold turns")
	}
}

// TestContextGaugeFallback 钉（D7 兜底）：control.Snapshot（controller.go:1042）
// 无上下文 token 字段——上下文仪表（参考 renderContextStatusGroups 的
// `tokens (pct)`）不得渲染、不得造数；零值快照只渲染可得子集且不 panic。
func TestContextGaugeFallback(t *testing.T) {
	// 零值快照：无任何数据也不 panic，行数仍是 2，仪表不出现。
	m, _, _ := newTestModel(t)
	m.statusState.snapshot = control.Snapshot{}
	view := m.statusView()
	if strings.Contains(view, "上下文") {
		t.Fatalf("context gauge must not render without snapshot fields:\n%s", view)
	}
	if rows := strings.Split(view, "\n"); len(rows) != 2 {
		t.Fatalf("empty snapshot must still render two rows, got %d:\n%s", len(rows), view)
	}

	// 有数据的快照同样无上下文仪表（字段不存在——可得子集照常渲染）。
	m2, _, _ := newTestModel(t)
	m2 = update(t, m2, statusTick{})
	if view := m2.statusView(); strings.Contains(view, "上下文") {
		t.Fatalf("context gauge must not be invented:\n%s", view)
	}
}

// ---- 状态栏换装（2026-10-17-dsh-tui-visual-redesign Task 7；设计 §6 状态栏）----

// TestStatusBarDress 钉：行 1 徽章 pill accent 底 + bg 槽 fg（styleBadge 既有
// 机制——不变量钉）+ 模型名 fgSecondary 右组（38;5;252m模型 …）；行 2 数据带
// caption 色（设计令牌表 caption/dim 同槽——dim 244）；引擎就绪中 = amber
// （yellow 槽 214）、引擎异常 = red（red 槽 203）——语义色不弱化，pill 形态
// 与文本不变。
func TestStatusBarDress(t *testing.T) {
	resetTheme(t)
	m, _, _ := newTestModel(t)
	m = update(t, m, statusTick{})
	rows := strings.Split(m.statusView(), "\n")
	if !strings.Contains(rows[0], "48;5;111") { // 徽章 accent 底（deepseek 默认；lipgloss 合并序 fg;bg——无 ESC 前缀钉，TestStyleKeycap 同法）
		t.Fatalf("badge must take the accent slot:\n%q", rows[0])
	}
	if !strings.Contains(rows[0], "38;5;234") { // 徽章 bg 槽 fg（不变量）
		t.Fatalf("badge fg must take the bg slot:\n%q", rows[0])
	}
	if !strings.Contains(rows[0], "\x1b[38;5;252m模型 fake-model") { // 模型名 fgSecondary
		t.Fatalf("model name must take the fgSecondary slot:\n%q", rows[0])
	}
	if !strings.Contains(rows[1], "\x1b[38;5;244m") { // 行 2 caption（dim 槽）
		t.Fatalf("row2 must take the caption/dim slot:\n%q", rows[1])
	}
	// 引擎就绪中/异常徽章：amber/red 语义底色（不弱化）。
	m2, _, _ := newTestModel(t)
	m2.statusState.snapshot.Ready = "NOT_READY"
	if r1 := strings.Split(m2.statusView(), "\n")[0]; !strings.Contains(r1, "\x1b[48;5;214") {
		t.Fatalf("NOT_READY badge must take amber:\n%q", r1)
	}
	m2.statusState.snapshot.Ready = "FAILED"
	if r1 := strings.Split(m2.statusView(), "\n")[0]; !strings.Contains(r1, "\x1b[48;5;203") {
		t.Fatalf("FAILED badge must take red:\n%q", r1)
	}
}

// TestStatusModeAgentsTab 钉（Task 15 收编——brief statusMode 文案）：agents
// 页签交互态（收编口径 agentsTabInteractive）→「agents 页签」；审批在场优先
// （statusMode 治理独占序——审批 > agents 页签，与 handleKey 独占层级一致）；
// 退出交互回落「空闲」。
func TestStatusModeAgentsTab(t *testing.T) {
	m, _, _ := newTestModel(t)
	m.panel.tab = tabAgents
	m.focus = focusPanel
	if got := m.statusMode(); got != "agents 页签" {
		t.Fatalf("statusMode = %q, want agents 页签", got)
	}
	m = update(t, m, tuiEvent{ev: event.ApprovalRequestEvent{ToolUseID: "t1", Tool: "Bash"}})
	if got := m.statusMode(); got != "审批中" {
		t.Fatalf("statusMode with approval = %q, want 审批中（审批优先）", got)
	}
	m = update(t, m, keyPress('a')) // 审批放行收口
	m.focus = focusMain
	if got := m.statusMode(); got != "空闲" {
		t.Fatalf("statusMode after exit = %q, want 空闲", got)
	}
}
