package tui

import (
	"fmt"
	"strings"
	"testing"
)

// ---- 按块增量换行缓存（023 D6，契约四角；对照参考 DeepSeek-Reasonix
// wrap_cache.go:8-15 Contract 与 chat_tui.go:989-997 sync+feed 收口）----
//
// countingBlock 渲染计数探针：统计 render 调用次数——增量路径下前缀块不得
// 再渲染（消除 Task 4 全量重渲染的 O(blocks)/帧）。
type countingBlock struct {
	text  string
	calls *int
}

func (b countingBlock) render(width int) []string {
	*b.calls++
	return wrapText(b.text, width)
}

// TestWrapCache 钉契约四角 + rewriteLast 失效接线：
//   - suffix：append 后 sync 只重排后缀块（前缀块 render 不再被调）；
//   - invalidate：invalidateWrapFrom 丢弃块 i 起缓存，下次 sync 只重排后缀；
//   - width：宽度变更触发全量重建；
//   - flatten：flattenBlockWraps ≡ join-then-wrap（契约前提，参考
//     wrap_cache.go:99-101——块边界已是强制换行，独立换行拼接与整篇一致）；
//   - rewrite：rewriteLast 经 invalidate 只重渲染被改写块（Task 7 流式前提）。
func TestWrapCache(t *testing.T) {
	t.Run("suffix append only rewraps suffix", func(t *testing.T) {
		ts := newTranscriptState()
		calls := make([]int, 4)
		for i := 0; i < 3; i++ {
			ts.append(countingBlock{text: fmt.Sprintf("block-%d", i), calls: &calls[i]})
		}
		if !ts.syncWrappedLines(80, false) {
			t.Fatal("空缓存首次 sync 必须全量重建并返回 true（viewport 需喂给）")
		}
		if calls[0] != 1 || calls[1] != 1 || calls[2] != 1 {
			t.Fatalf("首次 sync 全量重建各块恰渲染 1 次: %v", calls[:3])
		}
		ts.append(countingBlock{text: "block-3 tail", calls: &calls[3]})
		if !ts.syncWrappedLines(80, false) {
			t.Fatal("append 后 sync 行列表有变必须返回 true")
		}
		if calls[0]+calls[1]+calls[2] != 3 {
			t.Fatalf("suffix 路径前缀块不得重渲染: %v", calls[:3])
		}
		if calls[3] != 1 {
			t.Fatalf("新块应恰渲染 1 次: %d", calls[3])
		}
		if ts.wrapBlockCount != 4 || ts.wrapWidth != 80 {
			t.Fatalf("缓存游标 = (%d 块, 宽 %d), want (4, 80)", ts.wrapBlockCount, ts.wrapWidth)
		}
		joined := strings.Join(ts.wrappedLines, "\n")
		for i := 0; i < 4; i++ {
			if !strings.Contains(joined, fmt.Sprintf("block-%d", i)) {
				t.Fatalf("展平行缺块 %d 内容:\n%q", i, joined)
			}
		}
		if ts.syncWrappedLines(80, false) {
			t.Fatal("无变化 sync 必须返回 false（不重复喂给 viewport）")
		}
		if calls[3] != 1 {
			t.Fatalf("无变化 sync 不得重渲染: %d", calls[3])
		}
	})

	t.Run("invalidateWrapFrom drops suffix", func(t *testing.T) {
		ts := newTranscriptState()
		calls := make([]int, 3)
		for i := 0; i < 3; i++ {
			ts.append(countingBlock{text: fmt.Sprintf("block-%d", i), calls: &calls[i]})
		}
		ts.syncWrappedLines(80, false)
		ts.invalidateWrapFrom(1)
		if ts.wrapBlockCount != 1 || len(ts.wrapBlockLines) != 1 {
			t.Fatalf("invalidate 后游标 = (%d, %d 组), want (1, 1)",
				ts.wrapBlockCount, len(ts.wrapBlockLines))
		}
		if len(ts.wrappedLines) != 1 {
			t.Fatalf("invalidate 后展平行 = %d, want 1（仅前缀块 0）", len(ts.wrappedLines))
		}
		if !ts.syncWrappedLines(80, false) {
			t.Fatal("invalidate 后 sync 必须重排后缀并返回 true")
		}
		if calls[0] != 1 {
			t.Fatalf("前缀块不得重渲染: %v", calls)
		}
		if calls[1] != 2 || calls[2] != 2 {
			t.Fatalf("后缀块应各重渲染恰 1 次: %v", calls)
		}
		// 越界收口：index ≥ wrapBlockCount 为 no-op；负值钳 0（全弃）。
		ts.invalidateWrapFrom(5)
		if ts.wrapBlockCount != 3 {
			t.Fatalf("越界 invalidate 必须 no-op: %d", ts.wrapBlockCount)
		}
		ts.invalidateWrapFrom(-1)
		if ts.wrapBlockCount != 0 || len(ts.wrappedLines) != 0 {
			t.Fatalf("负值 invalidate 应清空缓存: (%d, %d)", ts.wrapBlockCount, len(ts.wrappedLines))
		}
		if !ts.syncWrappedLines(80, false) {
			t.Fatal("清空后 sync 必须重建并返回 true")
		}
		if calls[0] != 2 || calls[1] != 3 || calls[2] != 3 {
			t.Fatalf("清空后全量重渲染各块恰再 1 次: %v", calls)
		}
	})

	t.Run("width change forces full rebuild", func(t *testing.T) {
		ts := newTranscriptState()
		calls := make([]int, 3)
		for i := 0; i < 3; i++ {
			ts.append(countingBlock{text: fmt.Sprintf("block-%d", i), calls: &calls[i]})
		}
		ts.syncWrappedLines(80, false)
		if !ts.syncWrappedLines(40, false) {
			t.Fatal("宽度变更必须全量重建并返回 true")
		}
		if calls[0] != 2 || calls[1] != 2 || calls[2] != 2 {
			t.Fatalf("宽度变更必须全量重建（每块共 2 次）: %v", calls)
		}
		if ts.wrapWidth != 40 {
			t.Fatalf("wrapWidth = %d, want 40", ts.wrapWidth)
		}
	})

	t.Run("flattenBlockWraps equals join-then-wrap", func(t *testing.T) {
		texts := []string{
			"alpha",
			"beta gamma delta epsilon zeta",
			"l1\nl2",
			"中文块宽度换行按列计",
		}
		const w = 12
		perBlock := make([][]string, len(texts))
		for i, s := range texts {
			perBlock[i] = wrapBlockLines(plainBlock{text: s}, w)
		}
		flat := flattenBlockWraps(perBlock)
		joined := wrapText(strings.Join(texts, "\n"), w)
		if len(flat) != len(joined) {
			t.Fatalf("flatten = %d 行, join-then-wrap = %d 行:\n%q\nvs\n%q",
				len(flat), len(joined), flat, joined)
		}
		for i := range flat {
			if flat[i] != joined[i] {
				t.Fatalf("行 %d 不等价:\nflatten %q\njoined  %q", i, flat[i], joined[i])
			}
		}
		if len(flat) <= len(texts) {
			t.Fatal("窄宽下长块必须实际换行（等价性断言才有意义）")
		}
	})

	t.Run("rewriteLast invalidates last block", func(t *testing.T) {
		ts := newTranscriptState()
		calls := make([]int, 3)
		for i := 0; i < 3; i++ {
			ts.append(countingBlock{text: fmt.Sprintf("block-%d", i), calls: &calls[i]})
		}
		ts.syncWrappedLines(80, false)
		rc := 0
		ts.rewriteLast(countingBlock{text: "block-2 rewritten", calls: &rc})
		if !ts.syncWrappedLines(80, false) {
			t.Fatal("rewrite 后 sync 行列表有变必须返回 true")
		}
		if calls[0] != 1 || calls[1] != 1 {
			t.Fatalf("rewrite 只影响被改写块，前缀不得重渲染: %v", calls[:2])
		}
		if rc != 1 {
			t.Fatalf("被改写块应恰渲染 1 次: %d", rc)
		}
		if calls[2] != 1 {
			t.Fatalf("旧块本体不得重渲染（已被替换）: %v", calls)
		}
		joined := strings.Join(ts.wrappedLines, "\n")
		if !strings.Contains(joined, "block-2 rewritten") {
			t.Fatalf("改写内容必须可见:\n%q", joined)
		}
		if len(ts.wrappedLines) != 3 {
			t.Fatalf("rewrite 不增块：展平行 = %d, want 3", len(ts.wrappedLines))
		}
	})
}
