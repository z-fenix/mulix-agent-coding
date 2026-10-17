package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"mulix/internal/event"
)

// ---- 思考块：有界尾窗与折叠（023 D14 / FR-004，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）internal/cli：
//   - reasoningViewMax（chat_tui.go:2345）/ reasoningTailLines（chat_tui.go:2348）：
//     思考流渲染进有界尾窗——全链思考逐 delta 全量重渲染是 O(n²)（2k token 的
//     思考搅动 ~4.7GB），只渲染尾窗让每个 delta 有界。
//   - streamReasoning（chat_tui.go:2353-2365）：头丢弃 + RuneStart 对齐的字节窗。
//   - reasoningBlockLines（transcript_copy.go:309）：思考文本按宽换行、逐行 dim、
//     maxLines>0 时只保留尾部视觉行。
//   - commitReasoning（chat_tui.go:2975）：块收口折叠为单行摘要。
// adapted：
//  1. 块形态——参考以 reasoningLineIdx/reasoningTextIdx 分置「标记行 + 文本块」
//     两个下标；本仓为单语义块（thinkingBlock 经 rewriteLast 整块原地改写）：
//     流式渲染尾窗文本，收口折叠为单行摘要，无独立标记行。
//  2. token 口径（D14）——AssistantThinkingEvent.EstimatedTokens 恒零（control
//     层不提供分片级 token，controller.go:1293-1295 硬编码 0），刻意不读不渲染；
//     真实 thinking token 在回合末 UsageEvent.ThinkingTokens，经
//     noteThinkingTokens 回填（Task 17 回合回执接线）。
//  3. 流式期只渲染尾窗文本（无秒数头）；Ns 在收口时按本地钟差定格。

// reasoningViewMax 思考尾窗字节上限（对照参考 chat_tui.go:2345）。
const reasoningViewMax = 4096

// reasoningTailLines 思考尾窗行数上限（对照参考 chat_tui.go:2348）。
const reasoningTailLines = 12

// verboseThinking 思考全文开关（会话级，Ctrl+O 切换——023 Story4 Task 21；
// 对照参考 chat_tui.go:178 showReasoning「Ctrl+O / /verbose: show raw thinking
// text」）。包级变量与 activeTheme 同形（块渲染无模型访问路径）；true 时折叠
// 块渲染 tail 驻留全文，false 时折叠块渲染单行摘要。流式期尾窗有界渲染不受
// 此开关影响（D14 有界成本纪律只豁免收口后的一次性展开，渲染结果入换行缓存）。
var verboseThinking bool

// thinkingBlock 思考块：流式渲染有界尾窗（tail），收口折叠为单行摘要。
// collapsed 块的 tail 承载全文（展开面 = Ctrl+O verboseThinking——尾窗只约束
// 流式渲染成本，不约束收口后的驻留内容）；turn 为回合归属（noteThinkingTokens
// 回填的守卫凭据——用量只落同回合块）。
type thinkingBlock struct {
	tail      string
	startedAt time.Time
	endedAt   time.Time
	tokens    int64
	turn      int64
	collapsed bool
}

func (b thinkingBlock) render(width int) []string {
	inner := max(width-2, 1)
	if b.collapsed && !verboseThinking {
		return []string{"  " + dimLine("▸ "+b.summary())}
	}
	lines := wrapText(strings.TrimRight(b.tail, "\n"), inner)
	if !b.collapsed && len(lines) > reasoningTailLines { // 流式期仍走尾窗（对照参考
		lines = lines[len(lines)-reasoningTailLines:] // reasoningBlockLines 的 maxLines）
	}
	// 流式标记行（025 FR-004）：对照基线 `▎ thinking…`——首行标记 + 尾窗文本，
	// 收口后折叠行接管。
	out := []string{"  " + dimLine("▎ thinking…")}
	for _, l := range lines {
		out = append(out, "  "+dimLine(l))
	}
	return out
}

// summary 折叠行：思考了 N 秒 (Ctrl+O)。Ns = 首末思考事件到达的本地钟差
// （非回合时长按比例摊派）。025 FR-004：形态对齐基线 `▸ 思考了 0 秒 (Ctrl+O)`
// ——tok 槽移除（reasoning 数据仍在回执行；noteThinkingTokens 回填链保留，
// 供将来需要时取用）。
func (b thinkingBlock) summary() string {
	secs := int64(b.endedAt.Sub(b.startedAt).Seconds())
	if secs < 0 { // 防御：缺省时间戳的块不得渲染负数
		secs = 0
	}
	return fmt.Sprintf("思考了 %d 秒 (Ctrl+O)", secs)
}

// dimLine 弱化槽（dim）色分：思考文本与折叠摘要（对照参考 reasoningBlockLines
// 的 dim——T18 接入主题弱化槽）。
func dimLine(s string) string { return activeTheme.dim(s) }

// utf8SafeTail 有界尾窗：保尾截断——超 maxBytes 从头部丢字节（丢到 RuneStart
// 边界，不劈半字符），超 maxLines 丢头部行；maxBytes/maxLines <= 0 表示不设
// 该界。（对照参考 streamReasoning 的头丢弃 + RuneStart 对齐。）
func utf8SafeTail(s string, maxBytes, maxLines int) string {
	if maxBytes > 0 && len(s) > maxBytes {
		drop := len(s) - maxBytes
		for drop < len(s) && !utf8.RuneStart(s[drop]) {
			drop++
		}
		s = s[drop:]
	}
	if maxLines > 0 {
		lines := strings.Split(s, "\n")
		if len(lines) > maxLines {
			s = strings.Join(lines[len(lines)-maxLines:], "\n")
		}
	}
	return s
}

// streamThinking 思考分片入块（D14）：首个分片开块并打本地钟戳，后续分片
// rewrite 同块（Task 4 rewriteLast——缓存失效内建）。EstimatedTokens 刻意不读
// ——恒零字段，渲染它是死 UI（D14）。尾窗逐分片有界推进（输入恒 ≤
// reasoningViewMax + 单分片，与参考的增量窗同形），全文另路驻留供收口升格。
func (m *tuiModel) streamThinking(e event.AssistantThinkingEvent) {
	now := time.Now()
	if m.thinkingIdx < 0 {
		m.thinkStart = now
		m.thinkingTurn = e.Turn // 活块回合归属（Task 17 回填守卫凭据）
		m.thinkingFull, m.thinkingTail = "", ""
	}
	m.thinkEnd = now
	m.thinkingFull += e.Thinking
	m.thinkingTail = utf8SafeTail(m.thinkingTail+e.Thinking, reasoningViewMax, reasoningTailLines)
	b := thinkingBlock{tail: m.thinkingTail, startedAt: m.thinkStart, endedAt: m.thinkEnd, turn: m.thinkingTurn}
	if m.thinkingIdx >= 0 && m.thinkingIdx == len(m.transcriptState.blocks)-1 {
		m.transcriptState.rewriteLast(b)
		return
	}
	m.transcriptState.append(b) // 防御兜底：活块被顶离末位时退化为新块，不冲他块
	m.thinkingIdx = len(m.transcriptState.blocks) - 1
}

// closeThinking 思考块收口折叠（D14）：下一非思考事件或回合收口触发——块
// 折叠为单行「✓ 已思考 Ns[ · N tok]」，Ns 定格在末次思考事件到达（endedAt
// 只随思考事件推进，不按回合时长摊派）；tail 升格为全文驻块（Task 21 展开
// 面）。收口前到账的回执 token（thinkingPending）于此落槽。
func (m *tuiModel) closeThinking() {
	if m.thinkingIdx < 0 {
		return
	}
	b := thinkingBlock{
		tail:      m.thinkingFull,
		startedAt: m.thinkStart,
		endedAt:   m.thinkEnd,
		tokens:    m.thinkingPending,
		turn:      m.thinkingTurn,
		collapsed: true,
	}
	m.thinkingPending = 0
	m.thinkingFull, m.thinkingTail = "", ""
	if m.thinkingIdx == len(m.transcriptState.blocks)-1 {
		m.transcriptState.rewriteLast(b)
	} else { // 防御兜底（与 streamThinking 同形）：活块离位时追加，不冲他块
		m.transcriptState.append(b)
	}
	m.thinkingIdx = -1
}

// noteThinkingTokens 回填思考 token（Task 17 D16 接线：UsageEvent → 回执 +
// 回填）：UsageEvent.ThinkingTokens 到达时调用。块流式未收口 → 记 pending
// （收口时落槽——usage 先于 TurnDone 发布，controller.go:1357→1369）；已收口
// → 回填同回合最近一个思考块（turn 守卫：自尾搜索首个思考块，回合失配即止
// ——本回合无思考块时不得把用量挂到别回合约，典型如 tN+1 无思考、tN 的块
// 仍在块流尾）。0 视为未回填哨兵——槽位省略不渲染（TestNoZeroTokenRender）。
func (m *tuiModel) noteThinkingTokens(turn, tokens int64) {
	if tokens <= 0 {
		return
	}
	if m.thinkingIdx >= 0 { // 活块即当前回合流——无需守卫
		m.thinkingPending = tokens
		return
	}
	for i := len(m.transcriptState.blocks) - 1; i >= 0; i-- {
		if tb, ok := m.transcriptState.blocks[i].(thinkingBlock); ok {
			if tb.turn != turn { // 回合守卫：首个思考块即失配 → 本回合无思考块
				return
			}
			if tb.tokens == tokens { // 幂等：重复回执不重复失效缓存
				return
			}
			tb.tokens = tokens
			m.transcriptState.blocks[i] = tb
			m.transcriptState.invalidateWrapFrom(i)
			return
		}
	}
}

// toggleThinkingVerbose Ctrl+O 思考全文开关（023 Story4 Task 21）：无思考块
// 时 no-op（brief：目标外不动作——不切开关、不留痕）；有则翻转包级开关并全量
// 失效换行缓存（折叠/展开面随渲染即变），留痕行报告新态（对照参考
// toggleVerboseReasoning 的 notify——本仓通知走转写留痕）。
func (m *tuiModel) toggleThinkingVerbose() {
	has := false
	for _, b := range m.transcriptState.blocks {
		if _, ok := b.(thinkingBlock); ok {
			has = true
			break
		}
	}
	if !has {
		return
	}
	verboseThinking = !verboseThinking
	m.transcriptState.invalidateWrapFrom(0)
	if verboseThinking {
		m.appendLine("[思考] 已展开思考全文")
		return
	}
	m.appendLine("[思考] 已折叠为摘要")
}
