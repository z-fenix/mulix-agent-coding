package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"mulix/internal/event"
	"mulix/internal/subagent"
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
				if len(m.transcriptState.blocks) != 2 {
					t.Fatalf("头行+答文(残留冲刷原地复写) = 2 blocks, got %d", len(m.transcriptState.blocks))
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
			wantBlocks: []string{"推理进行中", "\x1b[38;5;244m"}, // T18：dim 槽（dsh dark 244）
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
		// 4. ToolUseRequestedEvent：工具卡出现（运行中），● 动词(主参)。
		{
			name: "ToolUseRequested 运行中工具卡",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "tu-a", Tool: "Bash",
					Input: json.RawMessage(`{"command":"go test ./..."}`)},
			},
			wantBlocks: []string{"\x1b[38;5;111m●\x1b[0m", "go test ./...", "运行中"}, // T18：accent 槽（deepseek 默认）；符号表：运行 ●
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
			wantBlocks: []string{"\x1b[38;5;114m✓\x1b[0m", "\x1b[38;5;238m⎿\x1b[0m", "vet ok", "all good", "完成"}, // 符号表：完成 ✓ green 槽；连接槽 ⎿ border 槽（T4 deferred minor 归位）
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
		// 6. ToolResultEvent 收口（失败）：失败红卡 ✗ + 详情（⊘ 保留给委派失败）。
		{
			name: "ToolResult 失败红卡",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, ToolUseID: "tu-f", Tool: "Bash",
					Input: json.RawMessage(`{"command":"boom"}`)},
				event.ToolResultEvent{Seq: 2, Turn: 1, ToolUseID: "tu-f", IsError: true,
					Content: "Error: exit 1"},
			},
			wantBlocks: []string{"\x1b[38;5;203m", "✗", "失败", "Error: exit 1"}, // T18：red 槽；符号表：失败 ✗
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
			wantBlocks: []string{"────", "本轮  142 tok · in 10 · cached 30 · new 20 · out 40 · reasoning 42 · ≈$0.5000"},
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
			wantBlocks: []string{"! 引擎漂移", "\x1b[38;5;214m"}, // T18：yellow 槽
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
			wantBlocks: []string{"▸ 思考了", "未完段落"},
			wantAbsent: []string{"回合完成"},
			wantState: func(t *testing.T, m *tuiModel) {
				if m.pendingMD != "" || m.thinkingIdx != -1 {
					t.Fatalf("turn close must flush residue and collapse thinking: pending=%q idx=%d",
						m.pendingMD, m.thinkingIdx)
				}
				if len(m.transcriptState.blocks) != 2 {
					t.Fatalf("折叠思考+冲刷残留 = 2 blocks（无 Usage 无回执；无 TurnStarted 不发头行）, got %d", len(m.transcriptState.blocks))
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
		// 16. DelegationStartedEvent：委派卡 `● 子代理 <id8> 启动 · 任务摘要`。
		{
			name: "DelegationStarted 委派卡启动",
			events: []any{
				event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub-12345678",
					Task: "跑全量测试", Depth: 1},
			},
			wantBlocks: []string{"\x1b[38;5;111m●\x1b[0m", "子代理 sub-1234… 启动 · 跑全量测试"}, // 符号表：运行 ●（deepseek 默认 accent）
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
			wantBlocks: []string{"\x1b[38;5;114m✓\x1b[0m", "子代理 sub-1234… 完成", "全部通过", "\x1b[38;5;238m⎿\x1b[0m", "1500 tok · $0.21"}, // 符号表：完成 ✓ green 槽；连接槽 ⎿ border 槽（T4 deferred minor 归位——委派完成尾同符号同槽）
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
			wantBlocks: []string{"\x1b[38;5;203m", "⊘", "子代理 sub-1234… 失败", "引擎崩溃"},
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
			// 不再永久渲染「· 运行中」；已终态卡不受影响（幂等——二次
			// EngineExited 零改写）；多卡场景缓存失效须覆盖全部被改块。
			name: "EngineExited 清扫运行中工具卡",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "toolu-8", Tool: "Bash", Input: json.RawMessage(`{"command":"boom"}`)},
				event.ToolResultEvent{Seq: 2, Turn: 1, ToolUseID: "toolu-8", Content: "boom", IsError: true},
				event.ToolUseRequestedEvent{Seq: 3, Turn: 1, ToolUseID: "toolu-9", Tool: "Bash", Input: json.RawMessage(`{"command":"sleep 100"}`)},
				event.ToolUseRequestedEvent{Seq: 4, Turn: 1, ToolUseID: "toolu-7", Tool: "Bash", Input: json.RawMessage(`{"command":"sleep 99"}`)},
				// 中段 sync（真实 UI 每帧都做，且用 transcriptContentWidth——
				// 与 finalize 同宽，否则宽度差触发全量重建会洗掉缓存失效漏洞
				// 的现场；024 终审 Important-1 的判别力所在。T18：口径对齐
				// mainWidth——finalize 的换行宽即 transcriptContentWidth(mainWidth)，
				// 窄档 W=80 下与 m.width 同值，行为不变）。
				func(m *tuiModel) {
					flattenTranscript(&m.transcriptState, transcriptContentWidth(m.mainWidth()))
				},
				event.EngineExitedEvent{Seq: 5, Code: 1, Reason: "crashed"},
				event.EngineExitedEvent{Seq: 6, Code: 1, Reason: "crashed"},
			},
			wantBlocks: []string{"\x1b[38;5;203m✗\x1b[0m", "引擎已退出，工具未返回", "boom"}, // 符号表：工具失败 ✗（清扫落失败态；SGR 形态防收口行 ✗ 误配）
			wantAbsent: []string{"· 运行中"},
			wantState: func(t *testing.T, m *tuiModel) {
				// 3 卡 + 2 退出留痕行（每次 EngineExited 各一行——幂等仅针对
				// 卡片清扫，留痕行如实重复）。
				if len(m.transcriptState.blocks) != 5 {
					t.Fatalf("blocks = %d, want 5（3 卡 + 2 退出行）", len(m.transcriptState.blocks))
				}
				for i, want := range []toolState{toolFailed, toolFailed, toolFailed} {
					if card := m.transcriptState.blocks[i].(*toolCard); card.state != want {
						t.Fatalf("block %d state = %d, want %d", i, card.state, want)
					}
				}
				if got := m.transcriptState.blocks[0].(*toolCard).failedDetail; got != "boom" {
					t.Fatalf("pre-failed card detail must be untouched, got %q", got)
				}
			},
		},
		// ---- Task 18（矩阵补全）：新面逐行 —— diff 卡 bg 槽 / 面板页签 / 布局档位 ----
		//
		// 判别性口径：色断言钉 SGR 复合段（错误实现必红）、档位行钉列位与签名
		// 在场/离场；前序任务已绿的行如实记台账（不硬造红）。符号行（●/✓/✗ 三
		// 态）T4 已钉（行 4/5/6 + EngineExited 清扫行 + 委派卡行）——本批不再重
		// 复造行，仅把连接槽 ⎿ 升级为 border 槽复合断言（T4 deferred minor 归位）。

		// 22. diff 卡 bg 槽（写类）：Write 请求即渲染 diff（D9 请求即渲染），
		//     增行整行 48;5;22 底 + 114 marker（dsh dark 手选）；纯新增无删行带。
		{
			name: "diff 卡 bg 槽（写类）：Write 卡增行整行槽色",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "tu-dw", Tool: "Write",
					Input: json.RawMessage(`{"file_path":"new.txt","content":"one\ntwo\n"}`)},
			},
			wantBlocks: []string{
				"\x1b[48;5;22m\x1b[38;5;114m", // diffRow 复合段：增行 bg 槽 + marker 前景
				"✎", "写",
			},
			wantAbsent: []string{"\x1b[48;5;52m"}, // Write 整文件纯新增——删行带不得出现
			wantState: func(t *testing.T, m *tuiModel) {
				// 标记与代码段被行内回铺 SGR 分隔（Task 6 换装）——内容连续性按
				// 剥离文本判定（diffview_test.go 同口径）。
				plain := ansi.Strip(strings.Join(flattenTranscript(&m.transcriptState,
					transcriptContentWidth(m.mainWidth())), "\n"))
				for _, want := range []string{"+ one", "+ two"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("Write diff rows missing %q:\n%s", want, plain)
					}
				}
			},
		},
		// 23. diff 卡 bg 槽（改写类）：Edit 请求 → 增/删双侧槽色同卡在场。
		{
			name: "diff 卡 bg 槽（改写类）：Edit 卡增删双侧槽色",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "tu-de", Tool: "Edit",
					Input: json.RawMessage(`{"file_path":"src/app.go","old_string":"old line","new_string":"new line"}`)},
			},
			wantBlocks: []string{
				"\x1b[48;5;22m\x1b[38;5;114m", // 增行：22 底 + 114 marker
				"\x1b[48;5;52m\x1b[38;5;203m", // 删行：52 底 + 203 marker
			},
			wantState: func(t *testing.T, m *tuiModel) {
				plain := ansi.Strip(strings.Join(flattenTranscript(&m.transcriptState,
					transcriptContentWidth(m.mainWidth())), "\n"))
				for _, want := range []string{"+ new line", "- old line"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("Edit diff rows missing %q:\n%s", want, plain)
					}
				}
			},
		},
		// 24. 面板页签行（变更·空态）：active accent+下划线合成段、inactive dim
		//     段、空态指引（T12 Ruling：页签显示名字面钉点路由本矩阵）。
		{
			name: "面板页签（变更·空态）：字面与槽位钉点",
			wantState: func(t *testing.T, m *tuiModel) {
				v := m.panelView(32)
				for _, want := range []string{
					"\x1b[38;5;111;4m变更\x1b[0m",                                                     // active：accent+SGR4 单条合成
					"\x1b[38;5;244m代理\x1b[0m", "\x1b[38;5;244m诊断\x1b[0m", "\x1b[38;5;244m缓存\x1b[0m", // inactive dim
					"暂无变更文件", "写类工具变更后在此汇总",
				} {
					if !strings.Contains(v, want) {
						t.Fatalf("panelView missing %q:\n%q", want, v)
					}
				}
			},
		},
		// 25. 面板页签行（变更·内容态）：写类收口沿聚合条目行（path + green/red
		//     计数槽）；条目在场时空态指引离场。
		{
			name: "面板页签（变更·内容态）：聚合条目行与计数槽",
			events: []any{
				event.ToolUseRequestedEvent{Seq: 1, Turn: 1, ToolUseID: "tu-dp", Tool: "Edit",
					Input: json.RawMessage(`{"file_path":"src/main.go","old_string":"a","new_string":"b"}`)},
				event.ToolResultEvent{Seq: 2, Turn: 1, ToolUseID: "tu-dp", Content: "ok"},
			},
			wantState: func(t *testing.T, m *tuiModel) {
				v := m.panelView(32)
				for _, want := range []string{"src/main.go", "\x1b[38;5;114m+1\x1b[0m", "\x1b[38;5;203m-1\x1b[0m"} {
					if !strings.Contains(v, want) {
						t.Fatalf("changes tab must render the aggregated entry (%q):\n%q", want, v)
					}
				}
				if strings.Contains(v, "暂无变更文件") {
					t.Fatalf("entries present must retire the empty state:\n%q", v)
				}
			},
		},
		// 26. 面板页签行（agents·未接线）：直渲口径空态（stub 无 Manager）。
		{
			name: "面板页签（agents）：子代理未启用空态",
			events: []any{
				func(m *tuiModel) { m.panel.tab = tabAgents },
			},
			wantState: func(t *testing.T, m *tuiModel) {
				v := m.panelView(32)
				for _, want := range []string{"\x1b[38;5;111;4m代理\x1b[0m", "子代理未启用", "[esc] 返回"} {
					if !strings.Contains(v, want) {
						t.Fatalf("agents tab missing %q:\n%q", want, v)
					}
				}
			},
		},
		// 27. 面板页签行（agents·列表行）：真实 Manager + stub 句柄——表头、行载
		//     任务、键位提示（TestTUIAgentsTabEmpty 持有「暂无子代理」接线空态）。
		{
			name: "面板页签（agents）：列表行与键位提示",
			events: []any{
				func(m *tuiModel) {
					mgr, err := subagent.NewManager(subagent.Options{
						Root: t.TempDir(),
						Factory: func(context.Context, string) (subagent.SessionHandle, error) {
							return &agentsStubHandle{id: "stub-mx", done: make(chan struct{})}, nil
						},
						Publish: func(any) {},
					})
					if err != nil {
						t.Fatalf("manager: %v", err)
					}
					if _, err := mgr.Spawn(context.Background(), "p", "子任务甲", 0); err != nil {
						t.Fatalf("spawn: %v", err)
					}
					m.ctrl.(*stubController).sub = mgr
					m.panel.tab = tabAgents
				},
			},
			wantState: func(t *testing.T, m *tuiModel) {
				v := m.panelView(32)
				for _, want := range []string{"\x1b[38;5;111;4m代理\x1b[0m", "ID | 状态 | 任务 | 深度", "子任务甲", agentsTabHint} {
					if !strings.Contains(v, want) {
						t.Fatalf("agents tab missing %q:\n%q", want, v)
					}
				}
			},
		},
		// 28. 面板页签行（doctor·内容行）：doctorLines 双呈现（FR-012——转写腿
		//     行为不变，行 10 已持；页签腿随 doctorLines 在场）。
		{
			name: "面板页签（doctor）：内容行呈现",
			events: []any{
				func(m *tuiModel) {
					m.doctorLines = []string{"引擎锁：已锁定", "缓存根：可用"}
					m.panel.tab = tabDoctor
				},
			},
			wantState: func(t *testing.T, m *tuiModel) {
				v := m.panelView(32)
				for _, want := range []string{"\x1b[38;5;111;4m诊断\x1b[0m", "引擎锁：已锁定", "缓存根：可用"} {
					if !strings.Contains(v, want) {
						t.Fatalf("doctor tab missing %q:\n%q", want, v)
					}
				}
			},
		},
		// 29. 面板页签行（cache·指引）：无数据（tempdir 零报告）→ 空态指引字面
		//     （「/cache 查看缓存汇总」——panelEmptyState 唯一文案源）。
		{
			name: "面板页签（cache）：无数据指引",
			events: []any{
				func(m *tuiModel) { m.panel.tab = tabCache },
			},
			wantState: func(t *testing.T, m *tuiModel) {
				v := m.panelView(32)
				for _, want := range []string{"\x1b[38;5;111;4m缓存\x1b[0m", "暂无用量数据", "/cache 查看缓存汇总"} {
					if !strings.Contains(v, want) {
						t.Fatalf("cache tab missing %q:\n%q", want, v)
					}
				}
			},
		},
		// 30. 布局档位 W=150（宽档三栏）：侧栏列（列 0–15 品牌）+ 主列 + 面板列
		//     （列 118–149 页签行）齐在场；主列宽换行判别——122 显示宽 notice 须
		//     在主列内容宽 101 处折行（T10 deferred minor 收口：harness 扁平化对
		//     齐 mainWidth——仍按 m.width=149 扁平化则整行不折、本断言必红）。
		{
			name: "布局档位 W=150：三栏构成与主列宽换行（mainWidth 对齐）",
			events: []any{
				tea.WindowSizeMsg{Width: 150, Height: 24},
				event.NoticeEvent{Seq: 1, Level: "info", Content: strings.Repeat("长", 60)},
			},
			wantAbsent: []string{"· " + strings.Repeat("长", 60)}, // 122 显示宽 > 101——整行在场即扁平化宽度错档
			wantState: func(t *testing.T, m *tuiModel) {
				if got := m.mainWidth(); got != 102 {
					t.Fatalf("W=150 全开 mainWidth = %d, want 102（150−16−32）", got)
				}
				lines := strings.Split(m.frame(), "\n")
				if len(lines) != 24 {
					t.Fatalf("帧高漂移：%d != 24", len(lines))
				}
				first := ansi.Strip(lines[0])
				if side := ansi.Cut(first, 0, 16); !strings.Contains(side, "◆ mulix") {
					t.Fatalf("首行列 0–15 缺侧栏品牌行：%q", side)
				}
				if panel := ansi.Cut(first, 118, 150); !strings.Contains(panel, "变更") || !strings.Contains(panel, "缓存") {
					t.Fatalf("首行列 118–149 缺面板页签行：%q", panel)
				}
			},
		},
		// 31. 布局档位 W=120（中档双栏）：侧栏在场（16 列）、面板几何让位（恒 0
		//     ——页签行合成段离场；状态行「缓存 本次命中」不含页签复合段，不误
		//     伤）。mainWidth = 120−16。
		{
			name:   "布局档位 W=120：双栏（侧栏在场、面板让位）",
			events: []any{tea.WindowSizeMsg{Width: 120, Height: 24}},
			wantState: func(t *testing.T, m *tuiModel) {
				if got := m.mainWidth(); got != 104 {
					t.Fatalf("W=120 中档 mainWidth = %d, want 104（120−16）", got)
				}
				frame := m.frame()
				first := ansi.Strip(strings.Split(frame, "\n")[0])
				if side := ansi.Cut(first, 0, 16); !strings.Contains(side, "◆ mulix") {
					t.Fatalf("中档首行缺侧栏品牌行：%q", side)
				}
				if banned := "\x1b[38;5;111;4m变更\x1b[0m"; strings.Contains(frame, banned) {
					t.Fatalf("中档面板列必须离场（页签行在场）：\n%s", frame)
				}
			},
		},
		// 32. 布局档位 W=90（窄档单列）：几何退化 (0,W,0)——侧栏品牌与面板页签
		//     合成段齐离场；每行显示宽 ≤ 90（单列收口）。
		{
			name:   "布局档位 W=90：窄档单列（侧栏面板齐让位）",
			events: []any{tea.WindowSizeMsg{Width: 90, Height: 24}},
			wantState: func(t *testing.T, m *tuiModel) {
				if got := m.mainWidth(); got != 90 {
					t.Fatalf("W=90 窄档 mainWidth = %d, want 90（恒等于 W）", got)
				}
				frame := m.frame()
				for _, banned := range []string{"◆ mulix", "\x1b[38;5;111;4m变更\x1b[0m"} {
					if strings.Contains(frame, banned) {
						t.Fatalf("窄档单列不得渲染侧栏/面板列（%q 在场）：\n%s", banned, frame)
					}
				}
				for i, l := range strings.Split(frame, "\n") {
					if w := ansi.StringWidth(l); w > 90 {
						t.Fatalf("行 %d 显示宽 %d > 90（单列超宽）", i, w)
					}
				}
			},
		},
		// 33. 布局档位 W=80（窄档基线）：TestFrameThreeTierComposition 持有逐行
		//     等价钉点；矩阵行持档位构成面（单列、帧高 24、mainWidth=W）。
		{
			name: "布局档位 W=80：窄档基线单列",
			wantState: func(t *testing.T, m *tuiModel) {
				if got := m.mainWidth(); got != 80 {
					t.Fatalf("W=80 窄档 mainWidth = %d, want 80", got)
				}
				frame := m.frame()
				for _, banned := range []string{"◆ mulix", "\x1b[38;5;111;4m变更\x1b[0m"} {
					if strings.Contains(frame, banned) {
						t.Fatalf("窄档基线不得渲染侧栏/面板列（%q 在场）：\n%s", banned, frame)
					}
				}
				if n := len(strings.Split(frame, "\n")); n != 24 {
					t.Fatalf("帧高漂移：%d != 24", n)
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
				case tea.WindowSizeMsg: // 布局档位行：宽度驱动的真实路径（Update 接线）
					m = update(t, m, x)
				case func(*tuiModel):
					x(m)
				default:
					m = update(t, m, tuiEvent{ev: e})
				}
			}
			joined := strings.Join(flattenTranscript(&m.transcriptState, transcriptContentWidth(m.mainWidth())), "\n")
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
