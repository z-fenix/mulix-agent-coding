package sessionx

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mulix/internal/event"
	"mulix/internal/jsonl"
)

// seedSession creates a store and appends the given events, then closes it
// (simulating a previous process run).
func seedSession(t *testing.T, root, id string, events []any) string {
	t.Helper()
	s, err := Create(root, Options{ID: id, Workspace: "w", EngineID: "e-" + id, Model: "m"})
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return filepath.Join(root, id)
}

// damageTail appends a half JSON line (no trailing newline) to a session's
// events.jsonl, simulating a crash mid-write.
func damageTail(t *testing.T, dir, half string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open for damage: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(half); err != nil {
		t.Fatalf("damage: %v", err)
	}
}

func TestReplayRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
		event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "hello"},
		event.TurnDoneEvent{Seq: 3, Turn: 1, Subtype: "success"},
	}
	dir := seedSession(t, root, "s-replay", want)

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	got, warnings, err := s.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("clean replay must not warn: %v", warnings)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		wj, _ := json.Marshal(want[i])
		gj, _ := json.Marshal(got[i])
		if string(wj) != string(gj) {
			t.Fatalf("event %d: got %s want %s", i, gj, wj)
		}
	}
}

func TestReplayIsolatesDamagedTail(t *testing.T) {
	root := t.TempDir()
	good := []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
		event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "keep me"},
	}
	dir := seedSession(t, root, "s-damaged", good)
	damageTail(t, dir, `{"kind":"turn_done","ev":{"seq":3`)

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open (damaged tail must not block opening): %v", err)
	}
	defer s.Close()
	got, warnings, err := s.Replay()
	if err != nil {
		t.Fatalf("replay must not fail on damaged tail: %v", err)
	}
	// 好数据不丢
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (good data survives)", len(got))
	}
	if txt := got[1].(event.AssistantTextEvent).Text; txt != "keep me" {
		t.Fatalf("good data corrupted: %+v", got[1])
	}
	// 告警且不阻塞
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), ".damaged") {
		t.Fatalf("isolation must warn naming .damaged: %v", warnings)
	}
	// 半行转储到 .damaged（字节精确：半行原样 + 换行）
	quarantined, err := os.ReadFile(filepath.Join(dir, "events.jsonl.damaged"))
	if err != nil {
		t.Fatalf(".damaged missing: %v", err)
	}
	if string(quarantined) != `{"kind":"turn_done","ev":{"seq":3`+"\n" {
		t.Fatalf("half line not quarantined byte-exact: %q", string(quarantined))
	}
	// 主日志修复干净：追加-回放闭环可用
	if err := s.Append(event.TurnDoneEvent{Seq: 10, Turn: 1, Subtype: "success"}); err != nil {
		t.Fatalf("append after isolation: %v", err)
	}
	got2, warnings2, err := s.Replay()
	if err != nil {
		t.Fatalf("replay after isolation: %v", err)
	}
	if len(warnings2) != 0 {
		t.Fatalf("replay after isolation must be clean: %v", warnings2)
	}
	if len(got2) != 3 {
		t.Fatalf("after isolation got %d events, want 3", len(got2))
	}
}

func TestReplayTwiceAfterIsolation(t *testing.T) {
	root := t.TempDir()
	dir := seedSession(t, root, "s-twice", []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
	})
	damageTail(t, dir, `{"kind":"notice`)

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if _, _, err := s.Replay(); err != nil {
		t.Fatalf("replay 1: %v", err)
	}
	// 第二次隔离：新的损坏尾部照常转储，.damaged 保留历史
	damageTail(t, dir, `{"kind":"notice","ev":{"seq":9`)
	got, warnings, err := s.Replay()
	if err != nil {
		t.Fatalf("replay 2: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("replay 2 got %d events, want 1", len(got))
	}
	if len(warnings) == 0 {
		t.Fatal("second isolation must warn again")
	}
	// 第二次隔离后主日志必须真正被修复（Repair 以实际文件状态为准）：
	// 追加-回放闭环可用，且新记录没有熔进半行。
	if err := s.Append(event.TurnDoneEvent{Seq: 20, Turn: 1, Subtype: "success"}); err != nil {
		t.Fatalf("append after second isolation: %v", err)
	}
	got2, _, err := s.Replay()
	if err != nil {
		t.Fatalf("replay after second isolation: %v", err)
	}
	if len(got2) != 2 {
		t.Fatalf("after second isolation got %d events, want 2", len(got2))
	}
	last, ok := got2[1].(event.TurnDoneEvent)
	if !ok || last.Seq != 20 {
		t.Fatalf("appended event corrupted by stale half-line: %#v", got2[1])
	}
	quarantined, err := os.ReadFile(filepath.Join(dir, "events.jsonl.damaged"))
	if err != nil {
		t.Fatalf(".damaged missing: %v", err)
	}
	if strings.Count(string(quarantined), "\n") < 2 {
		t.Fatalf(".damaged must accumulate isolated tails: %q", string(quarantined))
	}
}

// TestReplayQuarantineFailureKeepsHalfLine pins the ordering guarantee: if
// the .damaged dump fails (here: path occupied by a directory), Replay errors
// WITHOUT truncating — the half line stays recoverable in the main log.
func TestReplayQuarantineFailureKeepsHalfLine(t *testing.T) {
	root := t.TempDir()
	dir := seedSession(t, root, "s-qfail", []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
	})
	damageTail(t, dir, `{"kind":"notice","ev":{"seq":9`)
	// Occupy the quarantine path with a directory → open failure.
	if err := os.MkdirAll(filepath.Join(dir, "events.jsonl.damaged"), 0o755); err != nil {
		t.Fatalf("occupy: %v", err)
	}

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if _, _, err := s.Replay(); err == nil {
		t.Fatal("quarantine failure must error")
	}
	// 主日志未被截断：半行仍在文件末尾。
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read main log: %v", err)
	}
	if !strings.HasSuffix(string(raw), `{"kind":"notice","ev":{"seq":9`) {
		t.Fatalf("half line must remain in main log after failed quarantine: %q", string(raw))
	}
}

func TestScanSessions(t *testing.T) {
	root := t.TempDir()
	seedSession(t, root, "s-a", []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s-a", EngineSessionID: "e-a", PinnedModel: "m"},
	})
	seedSession(t, root, "s-b", nil)
	if err := os.MkdirAll(filepath.Join(root, "not-a-session"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	sessions, warnings, err := ScanSessions(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %+v", len(sessions), sessions)
	}
	byID := map[string]SessionInfo{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	a, ok := byID["s-a"]
	if !ok {
		t.Fatalf("s-a missing: %+v", sessions)
	}
	if a.EngineID != "e-s-a" || a.Model != "m" || a.Workspace != "w" {
		t.Fatalf("s-a meta mismatch: %+v", a)
	}
	if a.CreatedAt == "" {
		t.Fatalf("s-a created_at missing")
	}
	if _, ok := byID["s-b"]; !ok {
		t.Fatalf("s-b missing (empty session still listed): %+v", sessions)
	}
}

func TestScanSessionsWarnsOnCorruptMeta(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "s-bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bad, "meta.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sessions, warnings, err := ScanSessions(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("corrupt meta must not surface as a session: %+v", sessions)
	}
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "s-bad") {
		t.Fatalf("corrupt meta must warn naming the dir: %v", warnings)
	}
}

func TestScanSessionsEmptyRoot(t *testing.T) {
	sessions, warnings, err := ScanSessions(t.TempDir())
	if err != nil || len(sessions) != 0 || len(warnings) != 0 {
		t.Fatalf("empty root: %v %v %v", sessions, warnings, err)
	}
}

// TestDelegationEventsRoundTrip 钉住 P2 委派链事件的镜像往返（P2 Task 1）：
// Append → 重开 → ReadAllEvents → DeepEqual，与 P1 TestAppendMultipleKindsRoundTrip
// 走同一条编解码路径——红线：镜像 1:1（所有字段 JSON 往返稳定）。
func TestDelegationEventsRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-deleg", Workspace: "w", EngineID: "e", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.Close()
	events := []any{
		event.DelegationStartedEvent{Seq: 1, SubSessionID: "sub1", Task: "add tests", Depth: 1},
		event.DelegationCompletedEvent{Seq: 2, SubSessionID: "sub1", Summary: "done", TotalTokens: 100, CostUSDCents: 7},
		event.DelegationCancelledEvent{Seq: 3, SubSessionID: "sub1", Reason: "user"},
		event.DelegationFailedEvent{Seq: 4, SubSessionID: "sub1", Reason: "engine crash"},
	}
	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("append %T: %v", ev, err)
		}
	}
	r, err := OpenStore(filepath.Join(root, "s-deleg"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	got, err := r.ReadAllEvents()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d", len(got), len(events))
	}
	for i, want := range events {
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("event %d mismatch: got %#v want %#v", i, got[i], want)
		}
	}
}

// TestReplayOversizedSegmentCompacted 钉 FR-104/106 + SC-001/003:中部超限行
// 被隔离转储(有界头+元信息),主日志原地压实(好行全保留、超限段物理移除),
// 压实后文件可再次正常加载(不反复触发恢复)。
func TestReplayOversizedSegmentCompacted(t *testing.T) {
	root := t.TempDir()
	want := []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
		event.AssistantTextEvent{Seq: 2, Turn: 1, Text: "before"},
		event.TurnDoneEvent{Seq: 3, Turn: 1, Subtype: "success"},
		event.TurnStartedEvent{Seq: 4, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
	}
	dir := seedSession(t, root, "s-oversize", want)
	// 中部注入超限行(第 3 行,其后还有完整事件——压实不得吞它)。
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("z"), jsonl.MaxLineBytes+512)); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatalf("inject nl: %v", err)
	}
	f.Close()

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	events, warnings, err := s.Replay()
	if err != nil {
		t.Fatalf("replay must not hard-fail on oversized segment: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (all good events survive)", len(events))
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "oversized") {
		t.Fatalf("warnings must record oversized isolation, got: %s", joined)
	}
	// 隔离区有界转储:元信息 + 截断头(不写全量 17MB)。
	q, err := os.ReadFile(filepath.Join(dir, "events.jsonl.damaged"))
	if err != nil || !strings.Contains(string(q), `"damaged":"oversized"`) {
		t.Fatalf("quarantine must contain oversized meta (FR-103/106): %v", err)
	}
	if len(q) > jsonl.MaxLineBytes/16 {
		t.Fatalf("quarantine size = %d, must be bounded (FR-106)", len(q))
	}
	// 压实后再次加载无损坏(不反复触发);事件计数不变。
	events2, err := s.ReadAllEvents()
	if err != nil {
		t.Fatalf("post-compaction load must be clean: %v", err)
	}
	if len(events2) != len(events) {
		t.Fatalf("post-compaction events = %d, want %d", len(events2), len(events))
	}
}
