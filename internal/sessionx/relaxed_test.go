package sessionx

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mulix/internal/event"
	"mulix/internal/jsonl"
)

// TestReadEventsRelaxedClean 干净镜像：好行照读（decode 还原类型化事件）、
// 顺序 = 行序、零警告。
func TestReadEventsRelaxedClean(t *testing.T) {
	s, _ := newTestStore(t)
	defer s.Close()
	if err := s.Append(event.TurnStartedEvent{Seq: 1, SessionID: "s-20261006-01", PinnedModel: "claude-sonnet-4-5"}); err != nil {
		t.Fatalf("append turn started: %v", err)
	}
	if err := s.Append(event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "hello"}); err != nil {
		t.Fatalf("append assistant text: %v", err)
	}
	events, warnings, err := s.ReadEventsRelaxed()
	if err != nil {
		t.Fatalf("read relaxed: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	ts, ok := events[0].(event.TurnStartedEvent)
	if !ok || ts.Seq != 1 {
		t.Fatalf("event[0] mismatch: %#v", events[0])
	}
	at, ok := events[1].(event.AssistantTextEvent)
	if !ok || at.Seq != 2 || at.Text != "hello" {
		t.Fatalf("event[1] mismatch: %#v", events[1])
	}
}

// TestReadEventsRelaxedHalfLine 尾部半行（崩溃残留）降级为 1 警告（含行号），
// 好事件全部返回；红线断言：镜像字节与写后一致（os.Stat size + 逐字节对比
// ——非变异回放读，不截断、不隔离）。
func TestReadEventsRelaxedHalfLine(t *testing.T) {
	s, _ := newTestStore(t)
	defer s.Close()
	if err := s.Append(event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "good"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := s.Append(event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "still good"}); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	// 带外注入半行（截断 JSON、无换行）——Store.Append 永远产生完整行，
	// 只能第二句柄直写（与 TestAppendApprovalEventsFsync 同款并发句柄先例）。
	path := filepath.Join(s.Dir(), "events.jsonl")
	half := []byte(`{"kind":"assistant_text","ev":{"seq":3,"tur`)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for half line: %v", err)
	}
	if _, err := f.Write(half); err != nil {
		f.Close()
		t.Fatalf("write half line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close half-line handle: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	events, warnings, err := s.ReadEventsRelaxed()
	if err != nil {
		t.Fatalf("read relaxed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (good lines kept)", len(events))
	}
	g0, ok := events[0].(event.AssistantTextEvent)
	if !ok || g0.Seq != 1 || g0.Text != "good" {
		t.Fatalf("event[0] mismatch: %#v", events[0])
	}
	g1, ok := events[1].(event.AssistantTextEvent)
	if !ok || g1.Seq != 2 || g1.Text != "still good" {
		t.Fatalf("event[1] mismatch: %#v", events[1])
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
	// 警告含行号：半行是物理第 3 行（前两好行之后）。
	if !strings.Contains(warnings[0], "line 3") {
		t.Fatalf("warning %q must name line 3", warnings[0])
	}
	// 红线：非变异——size（os.Stat）+ 逐字节对比，与写后一致。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if info.Size() != int64(len(before)) {
		t.Fatalf("mirror size changed: %d -> %d", len(before), info.Size())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("mirror bytes changed (must be untouched)")
	}
}

// TestReadEventsRelaxedOversized 超限行（> MaxLineBytes=16MiB，阈值不可注入
// ——Store.Append 永远产生合法小行，只能第二句柄直写带外注入）降级为 1 警告
// （含行号），其余事件照回；镜像字节不变。
func TestReadEventsRelaxedOversized(t *testing.T) {
	s, _ := newTestStore(t)
	defer s.Close()
	if err := s.Append(event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "before"}); err != nil {
		t.Fatalf("append before: %v", err)
	}
	path := filepath.Join(s.Dir(), "events.jsonl")
	// 原始超限行：前缀 + MaxLineBytes 填充 + 后缀，总长必超阈值；带换行结尾
	// （干净尾，之后 Append 照常可用）。大测试数据落 t.TempDir()。
	big := make([]byte, 0, jsonl.MaxLineBytes+64)
	big = append(big, `{"kind":"assistant_text","ev":{"seq":2,"text":"`...)
	big = append(big, bytes.Repeat([]byte("x"), jsonl.MaxLineBytes)...)
	big = append(big, []byte("\"}}\n")...)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for oversized: %v", err)
	}
	if _, err := f.Write(big); err != nil {
		f.Close()
		t.Fatalf("write oversized: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close oversized handle: %v", err)
	}
	// 超限行之后的好事件照常追加（隔离不吞后续内容的镜像侧对照）。
	if err := s.Append(event.AssistantTextEvent{Seq: 3, Turn: 1, Text: "after"}); err != nil {
		t.Fatalf("append after: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}
	events, warnings, err := s.ReadEventsRelaxed()
	if err != nil {
		t.Fatalf("read relaxed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (before + after)", len(events))
	}
	g0, ok := events[0].(event.AssistantTextEvent)
	if !ok || g0.Seq != 1 || g0.Text != "before" {
		t.Fatalf("event[0] mismatch: %#v", events[0])
	}
	g1, ok := events[1].(event.AssistantTextEvent)
	if !ok || g1.Seq != 3 || g1.Text != "after" {
		t.Fatalf("event[1] mismatch: %#v", events[1])
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
	// 警告含行号：超限行是物理第 2 行。
	if !strings.Contains(warnings[0], "line 2") {
		t.Fatalf("warning %q must name line 2", warnings[0])
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("snapshot after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("mirror bytes changed (must be untouched)")
	}
}

// TestReadEventsRelaxedMissing 缺文件 → err（对齐 ReadAllEvents 的 open
// reader 失败路径；白盒构造无镜像 Store——Create 必然建空 events.jsonl，
// 缺文件只能是带外清理/未初始化，fail-closed 报错）。
func TestReadEventsRelaxedMissing(t *testing.T) {
	s := &Store{dir: t.TempDir()}
	events, warnings, err := s.ReadEventsRelaxed()
	if err == nil {
		t.Fatalf("missing events.jsonl must error, got nil (events=%d, warnings=%v)", len(events), warnings)
	}
}
