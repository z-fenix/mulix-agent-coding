package tui

// ---- 按块增量换行缓存（023 D6，MIT 改编）----
//
// 本文件改编自 DeepSeek-Reasonix（MIT License, © 2026 Reasonix Contributors,
// https://github.com/esengine/DeepSeek-Reasonix）internal/cli/wrap_cache.go：
// 契约逐函数对齐（clearWrapCache / syncWrappedLines / rebuildWrappedLinesFull /
// feedViewportContent / wrapBlockLines / flattenBlockWraps / invalidateWrapFrom）。
// 两处适配：
//  1. 块渲染——参考对字符串块调 wrapTranscript 换行；本仓块是语义块（block
//     接口），wrapBlockLines 改调 block.render(width)（换行仍走 lipgloss 宽度
//     渲染——对照参考 transcript.go:306-311 wrapTranscript：x/ansi Hardwrap 被
//     明确否定，跨行断点的敞开样式会渗入填充与下一行，正是「不撕裂样式」钉点）。
//  2. 接收者——参考方法挂 *chatTUI（字段平铺在模型本体）；本仓缓存状态收敛为
//     wrapCache 结构内嵌 transcriptState（块流与 viewport 所在），方法挂
//     *transcriptState，签名与参考一致。
//
// Contract（对照参考 wrap_cache.go:8-15）：
//   - wrapWidth 是缓存行所用的内容宽度；
//   - wrapBlockCount 是前缀已完整反映进 wrapBlockLines / wrappedLines 的块数；
//   - invalidateWrapFrom(i) 丢弃块 i 起的缓存，下次 sync 只重排后缀（流式改写、
//     工具尾）；
//   - 全量重建保留给宽度变更 / 历史收缩 / 空缓存——绝不因常规 dirty 标志触发。

// wrapCache 换行缓存状态（字段与参考一致；内嵌 transcriptState）。
type wrapCache struct {
	wrappedLines   []string   // 展平行缓存（viewport 喂给前克隆，见 feedViewportContent）
	wrapWidth      int        // 缓存行所用的内容宽度
	wrapBlockCount int        // 前缀已完整反映的块数
	wrapBlockLines [][]string // 逐块换行结果（前 wrapBlockCount 组与 wrappedLines 前缀一致）
}

// clearWrapCache 清空换行缓存（历史重置/会话切换用）。
func (ts *transcriptState) clearWrapCache() {
	ts.wrappedLines = nil
	ts.wrapWidth = 0
	ts.wrapBlockCount = 0
	ts.wrapBlockLines = nil
}

// syncWrappedLines 使 wrapBlockLines/wrappedLines 与 ts.blocks 在 contentW 下
// 一致。forceFull 全量重建；否则只换行 wrapBlockCount 之后的块（invalidateWrapFrom
// 之后的后缀路径）。行列表有变（viewport 需喂给）返回 true。
func (ts *transcriptState) syncWrappedLines(contentW int, forceFull bool) bool {
	if contentW <= 0 {
		contentW = 1
	}
	n := len(ts.blocks)
	if forceFull || contentW != ts.wrapWidth || n < ts.wrapBlockCount {
		return ts.rebuildWrappedLinesFull(contentW)
	}
	// Heal a desynced block slice (should not happen if invalidate is used).
	if len(ts.wrapBlockLines) != ts.wrapBlockCount {
		if ts.wrapBlockCount > len(ts.wrapBlockLines) {
			ts.wrapBlockCount = len(ts.wrapBlockLines)
		} else {
			ts.wrapBlockLines = ts.wrapBlockLines[:ts.wrapBlockCount]
		}
		ts.wrappedLines = flattenBlockWraps(ts.wrapBlockLines)
	}
	if n == ts.wrapBlockCount {
		return false
	}
	// Suffix-only: re-wrap mutated/new blocks from wrapBlockCount..n.
	for i := ts.wrapBlockCount; i < n; i++ {
		blockLines := wrapBlockLines(ts.blocks[i], contentW)
		ts.wrapBlockLines = append(ts.wrapBlockLines, blockLines)
	}
	// Rebuild the flat list from the (prefix-stable + new suffix) block wraps
	// into a fresh slice so the viewport never aliases a growing backing array.
	ts.wrappedLines = flattenBlockWraps(ts.wrapBlockLines)
	ts.wrapBlockCount = n
	ts.wrapWidth = contentW
	return true
}

func (ts *transcriptState) rebuildWrappedLinesFull(contentW int) bool {
	if contentW <= 0 {
		contentW = 1
	}
	n := len(ts.blocks)
	ts.wrapBlockLines = make([][]string, n)
	for i := range n {
		ts.wrapBlockLines[i] = wrapBlockLines(ts.blocks[i], contentW)
	}
	// Prefer per-block flatten over join-then-wrap so streaming suffix rebuilds
	// stay consistent with append-only updates (both use wrapBlockLines).
	ts.wrappedLines = flattenBlockWraps(ts.wrapBlockLines)
	ts.wrapBlockCount = n
	ts.wrapWidth = contentW
	return true
}

// feedViewportContent 把换行缓存推入 bubbles viewport（不重新拼接整篇为单串）。
// 行切片克隆——后续缓存增长不得改写 viewport 仍持有的存储。
func (ts *transcriptState) feedViewportContent() {
	if len(ts.wrappedLines) == 0 {
		ts.vp.SetContentLines(nil)
		return
	}
	lines := make([]string, len(ts.wrappedLines))
	copy(lines, ts.wrappedLines)
	ts.vp.SetContentLines(lines)
}

// wrapBlockLines 单块按宽换行为行切片（块渲染走 block.render——lipgloss 宽度
// 渲染收口于各块型）。空渲染防御性给单空行（对照参考 wrap_cache.go:91-97）。
func wrapBlockLines(b block, width int) []string {
	wrapped := b.render(width)
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

// flattenBlockWraps 拼接逐块换行行组。transcript 的块边界已是强制换行
// （块间独立换行再拼接 ≡ 整篇 join 后换行——契约前提，参考 wrap_cache.go:99-101）。
func flattenBlockWraps(blocks [][]string) []string {
	if len(blocks) == 0 {
		return nil
	}
	n := 0
	for _, b := range blocks {
		n += len(b)
	}
	out := make([]string, 0, n)
	for _, b := range blocks {
		out = append(out, b...)
	}
	return out
}

// invalidateWrapFrom 丢弃块 index 起的换行缓存，下次 syncWrappedLines 只重排
// 后缀。rewriteLast 与任何运行中槽位的原地改写（流式答案、工具尾）用它。
func (ts *transcriptState) invalidateWrapFrom(index int) {
	if index < 0 {
		index = 0
	}
	if index >= ts.wrapBlockCount {
		return
	}
	ts.wrapBlockLines = ts.wrapBlockLines[:index]
	ts.wrapBlockCount = index
	ts.wrappedLines = flattenBlockWraps(ts.wrapBlockLines)
}
