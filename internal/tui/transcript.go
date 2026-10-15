package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// ---- 语义保留转写块（FR-001）----
//
// 块是 transcript 的语义单元（一事件/一输入/一系统行 = 一块），render(width)
// 把语义源按宽渲染为可见行（含样式）。Task 4 起替代 m.lines 平铺行作为内容
// 模型；渲染行缓存与 viewport 喂给在此收口，Task 5 换 wrapcache 增量重渲染。

// block 语义保留转写块：语义源按宽渲染（含样式；换行走 lipgloss——宽度感知
// 含 CJK 与 SGR 断点平衡，对照参考 transcript.go:311 wrapTranscript 同法）。
type block interface {
	render(width int) []string
}

// wrapText 语义源按宽换行为行切片（Task 5 起由 wrapcache 按块缓存此结果）。
func wrapText(s string, width int) []string {
	if width <= 0 {
		width = 1
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(s), "\n")
}

// plainBlock 系统行块（notice/turn/审批留痕等）——旧行为迁移包装：appendLine
// 一调用一块。
type plainBlock struct{ text string }

func (b plainBlock) render(width int) []string { return wrapText(b.text, width) }

// userBlock 用户输入块（composer 提交回显接线随后续任务；本任务先立块型）。
type userBlock struct{ text string }

func (b userBlock) render(width int) []string { return wrapText(b.text, width) }

// assistantBlock 助手文本块（本任务纯文本渲染；Task 7 换 markdown）。
type assistantBlock struct{ text string }

func (b assistantBlock) render(width int) []string { return wrapText(b.text, width) }

// transcriptState 是 transcript 渲染关注点容器：语义块流 + 渲染行缓存 +
// bubbles viewport。
type transcriptState struct {
	blocks  []block        // 语义块流（追加为主；运行中块经 rewriteLast 原地改写）
	wrapped []string       // 最近一次全量渲染的行缓存（Task 5 换 wrapcache 增量）
	vp      viewport.Model // 转写视口（喂给用克隆行切片，见 syncViewport）
}

func newTranscriptState() transcriptState {
	return transcriptState{vp: viewport.New()}
}

// append 追加语义块（触发后缀重渲染——本任务 syncViewport 全量重渲染即满足
// 语义；Task 5 起 wrapcache 只重渲染后缀块）。
func (ts *transcriptState) append(b block) {
	ts.blocks = append(ts.blocks, b)
}

// rewriteLast 原地改写运行中块（流式/思考/工具尾——同一语义块的增量更新，
// 块数不变）。空块流防御性视为追加。
func (ts *transcriptState) rewriteLast(b block) {
	if len(ts.blocks) == 0 {
		ts.blocks = append(ts.blocks, b)
		return
	}
	ts.blocks[len(ts.blocks)-1] = b
}

// lines 全量行：语义块流按宽渲染展平（Task 5 起走 wrapcache）。返回内部缓存
// 切片——消费方只读；viewport 喂给在 syncViewport 内克隆。
func (ts *transcriptState) lines(width int) []string {
	out := make([]string, 0, len(ts.blocks))
	for _, b := range ts.blocks {
		out = append(out, b.render(width)...)
	}
	ts.wrapped = out
	return out
}

// syncViewport 按宽高同步转写视口：全量重渲染 → 克隆喂给 → 跟随尾部（旧行为
// 等价：尾部跟随；上翻锁定随后续任务）。克隆契约（对照参考 wrap_cache.go:80-88）：
// viewport 持有传入切片本体，后续缓存改写/增长不得波及已持有存储。
func (ts *transcriptState) syncViewport(width, height int) {
	ts.vp.SetWidth(width)
	ts.vp.SetHeight(height)
	src := ts.lines(width)
	if len(src) == 0 {
		ts.vp.SetContentLines(nil)
		return
	}
	clone := make([]string, len(src))
	copy(clone, src)
	ts.vp.SetContentLines(clone)
	ts.vp.GotoBottom()
}
