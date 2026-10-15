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
		joined := strings.Join(m.transcriptState.lines(80), "\n")
		for _, want := range []string{
			"✓ 回合 t1", "入100/写20/读300/出50/思42 tok", "$0.0123", "命中72%",
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
		joined := strings.Join(m.transcriptState.lines(80), "\n")
		if !strings.Contains(joined, "✓ 回合 t2") || !strings.Contains(joined, "思7") {
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
	// 行 1：空闲（左）起头，模型 · 会话 id8（右）收尾——右对齐即右组贴行尾。
	if !strings.HasPrefix(rows[0], "空闲") || !strings.HasSuffix(rows[0], "fake-model · s-x") {
		t.Fatalf("row1 = %q, want 空闲(左) + fake-model · s-x(右对齐)", rows[0])
	}
	// 行 2：数据带字段（命中率来自 Snapshot；回合累计 = 回合号+本回合 tok）。
	for _, want := range []string{"命中率 82.0%", "回合 t3 420 tok", "成本 $1.25", "冷回合 1"} {
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
