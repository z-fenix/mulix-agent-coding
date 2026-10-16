package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"

	"mulix/internal/event"
)

// ---- Task 11（023 FR-016/SC-004）：事件渲染覆盖矩阵 ----
//
// 设计 §6 映射表逐行断言：15 种前端可见事件 × 输入序列 → 转写块序列（子串）
// /状态谓词。语义按 P1 落地面钉住——语义升级（P2/P3）只改渲染不改本骨架
//（设计 §6 覆盖纪律）。Task 17（D16）已把两处 P1 接缝升级为设计形态：
//   - Usage 落回合回试行（字段见 status_test.go TestTurnReceipt）+ 思考 token
//     回填同回合块；
//   - TurnDone/EngineExited 收口行升级为设计 §6 形态（✓/⏹/✗ + 时长）。

// matrixCase 一行矩阵：events 依序驱动（tea.KeyPressMsg 直通 Update、
// func(*tuiModel) 白盒布置前置态、其余按事件包 tuiEvent）；wantBlocks 为
// 转写渲染的必含子串、wantAbsent 必不含子串、wantState 状态谓词。
type matrixCase struct {
	name       string
	events     []any
	wantBlocks []string
	wantAbsent []string
	wantState  func(t *testing.T, m *tuiModel)
}

func TestEventRenderMatrix(t *testing.T) {
	cases := []matrixCase{
		// 1. TurnStartedEvent：回合起点（设计 §6「不出行」）——在途用户气泡
		//    于此上屏（D10 上屏时机），且无任何系统行。
		{
			name: "TurnStarted 起点：气泡上屏且不出系统行",
			events: []any{
				keyPress('h'), keyPress('i'),
				tea.KeyPressMsg{Code: tea.KeyEnter},
				event.TurnStartedEvent{Seq: 1},
			},
			wantBlocks: []string{"hi"},
			wantState: func(t *testing.T, m *tuiModel) {
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("TurnStarted must not emit a system line, blocks = %d", len(m.transcriptState.blocks))
				}
				ub, ok := m.transcriptState.blocks[0].(userBlock)
				if !ok || ub.text != "hi" {
					t.Fatalf("turn start must commit the pending bubble, got %#v", m.transcriptState.blocks[0])
				}
				if m.pendingSubmit != nil {
					t.Fatal("pendingSubmit must be consumed by the bubble commit")
				}
			},
		},
		// 1b. TurnStartedEvent 重置流式态：上回合残开答案（回合未正常收口的
		//     中断路径）整段冲刷落块并复位游标——新回合文本不得续进旧答案块。
		{
			name: "TurnStarted 重置流式态：残开答案冲刷复位",
			events: []any{
				event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "残段"},
				event.TurnStartedEvent{Seq: 2},
			},
			wantBlocks: []string{"残段"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.pendingMD != "" || m.answerIdx != -1 || m.answerFlushed != 0 {
					t.Fatalf("turn start must reset the answer cursor: pending=%q idx=%d flushed=%d",
						m.pendingMD, m.answerIdx, m.answerFlushed)
				}
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("sealed residue must land as one block, blocks = %d", len(m.transcriptState.blocks))
				}
			},
		},
		// 2. AssistantTextEvent：完整段落 → markdown 块提交；半段滞留缓冲（D17）。
		{
			name: "AssistantText 完整段落提交、半段滞留缓冲",
			events: []any{
				event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "第一段。\n\n第二段未完"},
			},
			wantBlocks: []string{"第一段。"},
			wantState: func(t *testing.T, m *tuiModel) {
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("only the stable paragraph must commit, blocks = %d", len(m.transcriptState.blocks))
				}
				ab, ok := m.transcriptState.blocks[0].(assistantBlock)
				if !ok || ab.text != "第一段。" {
					t.Fatalf("committed block = %#v, want assistantBlock(第一段。)", m.transcriptState.blocks[0])
				}
				if !strings.Contains(m.pendingMD, "第二段未完") {
					t.Fatalf("incomplete tail must stay buffered, pendingMD = %q", m.pendingMD)
				}
			},
		},
		// 3. AssistantThinkingEvent：思考块有界尾窗流式；EstimatedTokens 恒零
		//    字段绝不渲染（D14）。
		{
			name: "AssistantThinking 尾窗流式且估 token 不渲染",
			events: []any{
				event.AssistantThinkingEvent{Seq: 1, Turn: 1, Thinking: "推理进行中", EstimatedTokens: 7},
			},
			wantBlocks: []string{"推理进行中", "\x1b[38;5;245m"}, // T18：dim 槽（graphite 暗 245）
			wantAbsent: []string{"7 tok"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.thinkingIdx != 0 {
					t.Fatalf("thinking block must be live, thinkingIdx = %d", m.thinkingIdx)
				}
				tb, ok := m.transcriptState.blocks[0].(thinkingBlock)
				if !ok || tb.collapsed {
					t.Fatalf("live uncollapsed thinking expected (ok=%v collapsed=%v)", ok, tb.collapsed)
				}
			},
		},
		// 4. ToolUseRequestedEvent：工具卡出现（运行中），⏺ 动词(主参)。
		{
			name: "ToolUseRequested 运行中工具卡",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "tu-a", Tool: "Bash",
					Input: json.RawMessage(`{"command":"go test ./..."}`)},
			},
			wantBlocks: []string{"\x1b[38;5;173m⏺\x1b[0m", "go test ./...", "运行中"}, // T18：accent 槽
			wantState: func(t *testing.T, m *tuiModel) {
				idx, ok := m.cards["tu-a"]
				if !ok || idx != 0 {
					t.Fatalf("card must index by ToolUseID, idx = %d ok = %v", idx, ok)
				}
				card, isCard := m.transcriptState.blocks[0].(*toolCard)
				if !isCard || card.state != toolRunning {
					t.Fatalf("running card expected, got %#v", m.transcriptState.blocks[0])
				}
			},
		},
		// 5. ToolResultEvent 收口（完成）：同 ToolUseID upsert 原地关卡 + 有界输出尾。
		{
			name: "ToolResult 完成收口：原地关卡与输出尾",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "tu-r", Tool: "Bash",
					Input: json.RawMessage(`{"command":"go vet"}`)},
				event.ToolResultEvent{Seq: 2, Turn: 1, ToolUseID: "tu-r", Content: "vet ok\nall good"},
			},
			wantBlocks: []string{"⎿", "vet ok", "all good", "完成"},
			wantAbsent: []string{"运行中"},
			wantState: func(t *testing.T, m *tuiModel) {
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("same-ID result must upsert in place, blocks = %d", len(m.transcriptState.blocks))
				}
				if card := m.transcriptState.blocks[0].(*toolCard); card.state != toolDone {
					t.Fatalf("card must close done, state = %d", card.state)
				}
			},
		},
		// 6. ToolResultEvent 收口（失败）：失败红卡 ⊘ + 详情。
		{
			name: "ToolResult 失败红卡",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "tu-f", Tool: "Bash",
					Input: json.RawMessage(`{"command":"boom"}`)},
				event.ToolResultEvent{Seq: 2, Turn: 1, ToolUseID: "tu-f", IsError: true,
					Content: "Error: exit 1"},
			},
			wantBlocks: []string{"\x1b[38;5;167m", "⊘", "失败", "Error: exit 1"}, // T18：red 槽
			wantState: func(t *testing.T, m *tuiModel) {
				if card := m.transcriptState.blocks[0].(*toolCard); card.state != toolFailed {
					t.Fatalf("IsError must fail the card, state = %d", card.state)
				}
			},
		},
		// 7. ApprovalRequestEvent：授权横幅（独占）+ 留痕行。
		{
			name: "ApprovalRequest 授权横幅独占",
			events: []any{
				event.ApprovalRequestEvent{Seq: 1, Turn: 1, ToolUseID: "tu-b", Tool: "Write"},
			},
			wantBlocks: []string{"[审批] 工具 Write"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.approval == nil || m.approval.toolUseID != "tu-b" {
					t.Fatalf("approval card must latch, got %+v", m.approval)
				}
				if !strings.Contains(m.View().Content, "等待授权") {
					t.Fatalf("approval banner must render in the frame:\n%s", m.View().Content)
				}
			},
		},
		// 8. ApprovalResolvedEvent：清卡（幂等）+ 裁决留痕行。
		{
			name: "ApprovalResolved 清卡留痕",
			events: []any{
				event.ApprovalRequestEvent{Seq: 1, ToolUseID: "tu-c", Tool: "Bash"},
				event.ApprovalResolvedEvent{Seq: 2, ToolUseID: "tu-c", Decision: "allow", Source: "user"},
			},
			wantBlocks: []string{"[裁决] tu-c → allow/user"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.approval != nil {
					t.Fatal("resolved card must clear")
				}
			},
		},
		// 9. UsageEvent（D16）：回合回试行落转写（答案下方）+ 收口思考块 +
		//    思考 token 回填同回合块（Task 8 接缝闭环）。状态栏照旧走 Snapshot
		//    轮询（D7），两者并存不冲突。
		{
			name: "Usage 回执落转写、收口思考块并回填 token",
			events: []any{
				event.AssistantThinkingEvent{Seq: 1, Turn: 1, Thinking: "推理"},
				event.UsageEvent{Seq: 2, Turn: 1, UncachedInputTokens: 10, WriteTokens: 20,
					ReadTokens: 30, OutputTokens: 40, ThinkingTokens: 42, CostUSD: 0.5, HitRate: 0.5},
			},
			wantBlocks: []string{"✓ 回合 t1", "入10/写20/读30/出40/思42 tok", "$0.5000", "命中50%"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.thinkingIdx != -1 {
					t.Fatalf("Usage must close the thinking block, thinkingIdx = %d", m.thinkingIdx)
				}
				if len(m.transcriptState.blocks) != 2 {
					t.Fatalf("thinking + receipt = 2 blocks, got %d", len(m.transcriptState.blocks))
				}
				tb, ok := m.transcriptState.blocks[0].(thinkingBlock)
				if !ok || tb.tokens != 42 {
					t.Fatalf("thinking block must be backfilled from same-turn usage, got %#v", m.transcriptState.blocks[0])
				}
				if _, ok := m.transcriptState.blocks[1].(receipt); !ok {
					t.Fatalf("second block must be the receipt, got %#v", m.transcriptState.blocks[1])
				}
			},
		},
		// 10. NoticeEvent（info）：`· 内容`。
		{
			name:       "Notice info：· 前缀",
			events:     []any{event.NoticeEvent{Seq: 1, Level: "info", Content: "上下文压缩完成"}},
			wantBlocks: []string{"· 上下文压缩完成"},
			wantAbsent: []string{"[notice]"},
		},
		// 11. NoticeEvent（warning）：`! 内容` 色分。
		{
			name:       "Notice warning：! 前缀色分",
			events:     []any{event.NoticeEvent{Seq: 1, Level: "warning", Content: "引擎漂移"}},
			wantBlocks: []string{"! 引擎漂移", "\x1b[38;5;179m"}, // T18：yellow 槽
			wantAbsent: []string{"[notice]"},
		},
		// 12. TurnDoneEvent（完成）：折叠思考块 + 冲刷流式残留 + 收口行
		//     （设计 §6：✓ + 时长，DurationMS 来自 TurnDoneEvent）。
		{
			name: "TurnDone 完成：折叠思考、冲刷残留、收口行",
			events: []any{
				event.AssistantThinkingEvent{Seq: 1, Thinking: "想清楚"},
				event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "未完段落"},
				event.TurnDoneEvent{Seq: 3, Turn: 6, Subtype: "success", DurationMS: 4500},
			},
			wantBlocks: []string{"✓ 已思考", "未完段落", "✓ 回合完成 t6 · 4.5s"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.pendingMD != "" || m.thinkingIdx != -1 {
					t.Fatalf("turn close must flush residue and collapse thinking: pending=%q idx=%d",
						m.pendingMD, m.thinkingIdx)
				}
				if len(m.transcriptState.blocks) != 3 {
					t.Fatalf("collapse + sealed answer + close line = 3 blocks, got %d", len(m.transcriptState.blocks))
				}
			},
		},
		// 13. TurnDoneEvent（取消）：本地取消意图优先——不看 Subtype/IsError
		//     （Task 10 裁定：引擎中断后回执形态无 fixture，不可判失败）。
		{
			name: "TurnDone 取消意图：⏹ 收口不看引擎形态",
			events: []any{
				func(m *tuiModel) { m.cancelIntent = true },
				event.TurnDoneEvent{Seq: 1, Turn: 4, Subtype: "interrupted", IsError: true},
			},
			wantBlocks: []string{"⏹ 回合已取消 t4"},
			wantAbsent: []string{"回合失败", "回合完成"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.cancelIntent {
					t.Fatal("cancelIntent must be consumed by the cancelled close")
				}
			},
		},
		// 14. TurnDoneEvent（失败）：设计 §6 形态 `✗ 回合失败 tN（subtype）`。
		{
			name:       "TurnDone 失败收口",
			events:     []any{event.TurnDoneEvent{Seq: 1, Turn: 5, IsError: true, Subtype: "error_max_turns"}},
			wantBlocks: []string{"✗ 回合失败 t5（error_max_turns）"},
		},
		// 15. EngineExitedEvent：退出留痕行（设计 §6：✗ 引擎已退出（reason））；
		//     编辑器保持可用。
		{
			name: "EngineExited 留痕且编辑器保持可用",
			events: []any{
				event.EngineExitedEvent{Seq: 1, Code: 1, Reason: "process exited: exit status 1"},
				keyPress('x'),
			},
			wantBlocks: []string{"✗ 引擎已退出（process exited: exit status 1）"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.composerValue() != "x" {
					t.Fatalf("editor must stay usable after engine exit, input = %q", m.composerValue())
				}
			},
		},
		// 16. DelegationStartedEvent：委派卡 `⏺ 子代理 <id8> 启动 · 任务摘要`。
		{
			name: "DelegationStarted 委派卡启动",
			events: []any{
				event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub-12345678",
					Task: "跑全量测试", Depth: 1},
			},
			wantBlocks: []string{"\x1b[38;5;173m⏺\x1b[0m", "子代理 sub-1234… 启动 · 跑全量测试"},
			wantState: func(t *testing.T, m *tuiModel) {
				idx, ok := m.delegCards["sub-12345678"]
				if !ok || idx != 0 {
					t.Fatalf("delegation card must index by SubSessionID, idx = %d ok = %v", idx, ok)
				}
				db, isCard := m.transcriptState.blocks[0].(*delegationBlock)
				if !isCard || db.state != delegRunning {
					t.Fatalf("running delegation card expected, got %#v", m.transcriptState.blocks[0])
				}
			},
		},
		// 17. DelegationCompletedEvent：同 SubSessionID upsert 收口，含 token/成本。
		{
			name: "DelegationCompleted 完成收口含 token 与成本",
			events: []any{
				event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub-12345678", Task: "跑全量测试"},
				event.DelegationCompletedEvent{Seq: 2, SubSessionID: "sub-12345678",
					Summary: "全部通过", TotalTokens: 1500, CostUSDCents: 21},
			},
			wantBlocks: []string{"子代理 sub-1234… 完成", "全部通过", "⎿", "1500 tok · $0.21"},
			wantState: func(t *testing.T, m *tuiModel) {
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("lifecycle must upsert one card in place, blocks = %d", len(m.transcriptState.blocks))
				}
				db, ok := m.transcriptState.blocks[0].(*delegationBlock)
				if !ok || db.state != delegDone || db.summary != "全部通过" ||
					db.tokens != 1500 || db.costCents != 21 {
					t.Fatalf("completion card = %#v", m.transcriptState.blocks[0])
				}
			},
		},
		// 18. DelegationCancelledEvent：取消收口（含原因）。
		{
			name: "DelegationCancelled 取消收口",
			events: []any{
				event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub-12345678", Task: "跑全量测试"},
				event.DelegationCancelledEvent{Seq: 2, SubSessionID: "sub-12345678", Reason: "user:/agents"},
			},
			wantBlocks: []string{"子代理 sub-1234… 已取消", "user:/agents"},
			wantState: func(t *testing.T, m *tuiModel) {
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("cancelled lifecycle must upsert one card, blocks = %d", len(m.transcriptState.blocks))
				}
				if db := m.transcriptState.blocks[0].(*delegationBlock); db.state != delegCancelled {
					t.Fatalf("cancelled card state = %d", db.state)
				}
			},
		},
		// 19. DelegationFailedEvent：失败收口（红卡 + 原因）。
		{
			name: "DelegationFailed 失败收口红卡",
			events: []any{
				event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub-12345678", Task: "跑全量测试"},
				event.DelegationFailedEvent{Seq: 2, SubSessionID: "sub-12345678", Reason: "引擎崩溃"},
			},
			wantBlocks: []string{"\x1b[38;5;167m", "⊘", "子代理 sub-1234… 失败", "引擎崩溃"},
			wantState: func(t *testing.T, m *tuiModel) {
				if len(m.transcriptState.blocks) != 1 {
					t.Fatalf("failed lifecycle must upsert one card, blocks = %d", len(m.transcriptState.blocks))
				}
				if db := m.transcriptState.blocks[0].(*delegationBlock); db.state != delegFailed {
					t.Fatalf("failed card state = %d", db.state)
				}
			},
		},
		{
			// 16.（024 FR-002）EngineExited 清扫运行中工具卡为中断终态：
			// 不再永久渲染「· 运行中」；已终态卡不受影响（幂等）。
			name: "EngineExited 清扫运行中工具卡",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "toolu-9", Tool: "Bash", Input: json.RawMessage(`{"command":"sleep 100"}`)},
				event.EngineExitedEvent{Seq: 2, Code: 1, Reason: "crashed"},
			},
			wantBlocks: []string{"⊘", "引擎已退出，工具未返回"},
			wantAbsent: []string{"· 运行中"},
			wantState: func(t *testing.T, m *tuiModel) {
				card := m.transcriptState.blocks[0].(*toolCard)
				if card.state != toolFailed {
					t.Fatalf("engine-exited sweep must close running card, state = %d", card.state)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := newTestModel(t)
			for _, e := range tc.events {
				switch x := e.(type) {
				case tea.KeyPressMsg:
					m = update(t, m, x)
				case func(*tuiModel):
					x(m)
				default:
					m = update(t, m, tuiEvent{ev: e})
				}
			}
			joined := strings.Join(m.transcriptState.lines(80), "\n")
			for _, want := range tc.wantBlocks {
				if !strings.Contains(joined, want) {
					t.Errorf("transcript missing %q\n--- transcript ---\n%s", want, joined)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(joined, absent) {
					t.Errorf("transcript must not contain %q\n--- transcript ---\n%s", absent, joined)
				}
			}
			if tc.wantState != nil {
				tc.wantState(t, m)
			}
		})
	}
}
