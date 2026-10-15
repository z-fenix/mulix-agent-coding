package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// ---- 语义保留转写块（FR-001）----
//
// 块是 transcript 的语义单元（一事件/一输入/一系统行 = 一块），render(width)
// 把语义源按宽渲染为可见行（含样式）。渲染行缓存与 viewport 喂给在此收口；
// 换行缓存的增量重渲染见 wrapcache.go（023 D6 移植）。

// block 语义保留转写块：语义源按宽渲染（含样式；换行走 lipgloss——宽度感知
// 含 CJK 与 SGR 断点平衡，对照参考 transcript.go:311 wrapTranscript 同法）。
type block interface {
	render(width int) []string
}

// wrapText 语义源按宽换行为行切片（wrapcache 按块缓存此结果）。
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

// transcriptState 是 transcript 渲染关注点容器：语义块流 + 按块换行缓存
// （wrapCache，023 D6）+ bubbles viewport。
type transcriptState struct {
	blocks    []block        // 语义块流（追加为主；运行中块经 rewriteLast 原地改写）
	wrapCache                // 换行缓存（字段与方法见 wrapcache.go；023 D6 移植）
	vp        viewport.Model // 转写视口（喂给用克隆行切片，见 feedViewportContent）
}

func newTranscriptState() transcriptState {
	return transcriptState{vp: viewport.New()}
}

// append 追加语义块——不触碰缓存：sync 的 suffix 路径自动覆盖 wrapBlockCount
// 之后的块（对照参考：append 不失效，sync 兜底）。
func (ts *transcriptState) append(b block) {
	ts.blocks = append(ts.blocks, b)
}

// rewriteLast 原地改写运行中块（流式/思考/工具尾——同一语义块的增量更新，
// 块数不变），并失效该块缓存（invalidateWrapFrom，对照参考 setTranscriptBlock
// → invalidateWrapFrom）——下次 sync 只重排此块。空块流防御性视为追加。
func (ts *transcriptState) rewriteLast(b block) {
	if len(ts.blocks) == 0 {
		ts.blocks = append(ts.blocks, b)
		return
	}
	ts.blocks[len(ts.blocks)-1] = b
	ts.invalidateWrapFrom(len(ts.blocks) - 1)
}

// lines 全量行：经换行缓存同步（增量——append 只重排后缀；宽度变更/历史收缩/
// 空缓存才全量重建）后返回展平行。返回内部缓存切片——消费方只读；viewport
// 喂给经 feedViewportContent 克隆。
func (ts *transcriptState) lines(width int) []string {
	ts.syncWrappedLines(width, false)
	return ts.wrappedLines
}

// syncViewport 按宽高同步转写视口：缓存 sync（增量）→ 行列表有变才克隆喂给
// → 跟随尾部（旧行为等价：尾部跟随；上翻锁定随后续任务）。克隆契约（对照
// 参考 wrap_cache.go:77-88）：viewport 持有传入切片本体，后续缓存改写/增长
// 不得波及已持有存储。
func (ts *transcriptState) syncViewport(width, height int) {
	ts.vp.SetWidth(width)
	ts.vp.SetHeight(height)
	if ts.syncWrappedLines(width, false) {
		ts.feedViewportContent()
	}
	ts.vp.GotoBottom()
}
