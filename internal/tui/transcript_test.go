package tui

import (
	"strings"
	"testing"
)

// ---- 转写块模型（FR-001：语义保留块 → 按宽渲染行）----

// TestBlockAppendRewrite pins：append 3 块 + rewriteLast 后 lines(80) 反映
// 改写且块数不变（运行中块原地改写——流式/思考/工具尾的块模型基础；块是
// 语义单元，渲染行按宽展平，lipgloss 行尾补齐至宽——对照参考 wrap_cache
// 的 lipgloss 换行法，plan 评审钉定）。
func TestBlockAppendRewrite(t *testing.T) {
	ts := newTranscriptState()
	ts.append(plainBlock{text: "alpha"})
	ts.append(userBlock{text: "beta"})
	ts.append(assistantBlock{text: "gamma"})
	ts.rewriteLast(plainBlock{text: "gamma-rewritten"})
	if len(ts.blocks) != 3 {
		t.Fatalf("blocks = %d, want 3（rewriteLast 必须原地改写不增块）", len(ts.blocks))
	}
	lines := ts.lines(80)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	if strings.TrimSpace(lines[0]) != "alpha" || strings.TrimSpace(lines[1]) != "beta" {
		t.Fatalf("earlier blocks must be untouched: %q / %q", lines[0], lines[1])
	}
	if strings.TrimSpace(lines[2]) != "gamma-rewritten" {
		t.Fatalf("rewriteLast not reflected in lines: %q", lines[2])
	}

	// 多行语义块逐行展平（事件→可见行的块级等价：一事件一块，行数随语义源）
	ts2 := newTranscriptState()
	ts2.append(assistantBlock{text: "l1\nl2"})
	if got := ts2.lines(80); len(got) != 2 || strings.TrimSpace(got[0]) != "l1" || strings.TrimSpace(got[1]) != "l2" {
		t.Fatalf("multiline block render = %q, want [l1 l2]", got)
	}

	// 防御：空块流上 rewriteLast 视为追加（不 panic）
	ts3 := newTranscriptState()
	ts3.rewriteLast(plainBlock{text: "first"})
	if len(ts3.blocks) != 1 || strings.TrimSpace(ts3.lines(80)[0]) != "first" {
		t.Fatalf("rewriteLast on empty transcript must append: %d blocks", len(ts3.blocks))
	}
}

// TestViewportCloneFeed pins（克隆契约，对照参考 wrap_cache.go:80-88）：
// viewport 持有喂给切片本体——喂给后改写/增长内部渲染缓存不得波及 viewport
// 已持有内容；再喂后新内容可见。
func TestViewportCloneFeed(t *testing.T) {
	ts := newTranscriptState()
	ts.append(plainBlock{text: "line-one"})
	ts.syncViewport(80, 6)
	fed := ts.wrapped
	if len(fed) == 0 {
		t.Fatal("render cache must hold the fed lines")
	}
	want := ts.vp.GetContent()
	fed[0] = "POLLUTED" // 原地改写内部缓存
	if got := ts.vp.GetContent(); got != want {
		t.Fatalf("viewport content mutated through internal cache:\n%q", got)
	}
	before := ts.vp.TotalLineCount()
	ts.wrapped = append(ts.wrapped, "grown") // 缓存增长
	if got := ts.vp.TotalLineCount(); got != before {
		t.Fatalf("viewport line count changed on cache growth: %d → %d", before, got)
	}
	if got := ts.vp.GetContent(); !strings.Contains(got, "line-one") || strings.Contains(got, "POLLUTED") {
		t.Fatalf("viewport must keep its own copy: %q", got)
	}
	// 追加新块后再喂：新内容可见（本任务简单全量同步——Task 5 换 wrapcache）
	ts.append(plainBlock{text: "line-two"})
	ts.syncViewport(80, 6)
	if got := ts.vp.GetContent(); !strings.Contains(got, "line-two") {
		t.Fatalf("viewport must reflect appended block after sync: %q", got)
	}
	if h := ts.vp.Height(); h != 6 {
		t.Fatalf("viewport height = %d, want 6", h)
	}
}
