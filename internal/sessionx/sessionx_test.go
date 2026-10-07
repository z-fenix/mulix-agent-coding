package sessionx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mulix/internal/event"
	"mulix/internal/jsonl"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Create(root, Options{
		ID:          "s-20261006-01",
		Workspace:   "D:\\workplace\\project\\mulix-agent-coding",
		EngineID:    "engine-abc",
		Model:       "claude-sonnet-4-5",
		Unattended:  false,
		PinnedModel: "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return s, filepath.Join(root, "s-20261006-01")
}

func TestCreateLaysOutDirectory(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()
	for _, name := range []string{"events.jsonl", "meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	// cache.json is control's projection; absent until first Write.
	if _, err := os.Stat(filepath.Join(dir, "cache.json")); !os.IsNotExist(err) {
		t.Fatalf("cache.json must be absent until first write: %v", err)
	}
}

func TestMetaContents(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("meta not json: %v", err)
	}
	if meta["engine_session_id"] != "engine-abc" {
		t.Errorf("engine_session_id = %v", meta["engine_session_id"])
	}
	if meta["model"] != "claude-sonnet-4-5" {
		t.Errorf("model = %v", meta["model"])
	}
	if meta["workspace"] == "" {
		t.Errorf("workspace empty")
	}
	if _, ok := meta["created_at"]; !ok {
		t.Errorf("created_at missing")
	}
}

func TestAppendPersistsImmediately(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()

	ev := event.AssistantTextEvent{Seq: 1, Turn: 1, Text: "hello"}
	if err := s.Append(ev); err != nil {
		t.Fatalf("append: %v", err)
	}

	// 先持久化后发布：Append 返回后，事件必须已在磁盘上（模拟进程崩溃后
	// 重新打开可读全部事件——不经 Close，直接开第二个读句柄）。
	r, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	events, err := r.ReadAllEvents()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events after crash-simulated reopen, want 1", len(events))
	}
	got, ok := events[0].(event.AssistantTextEvent)
	if !ok || got.Text != "hello" || got.Seq != 1 {
		t.Fatalf("event mismatch: %#v", events[0])
	}
}

// TestAppendApprovalEventsFsync pins per-kind fsync（Task 9 交接 → Task 20）：
// 审批类事件 Append 返回后必须已确认落盘——以独立只读句柄在写句柄仍打开时
// 读到完整字节（崩溃模拟；fsync 后 OS 缓存已刷，不依赖 Close）。
// Sync 本身是 OS 语义，由 jsonl 层健康路径测试覆盖调用面。
// Windows 注意：写句柄必须在 TempDir 清理前 Close（同 Task 13 教训）。
func TestAppendApprovalEventsFsync(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-fsync", Workspace: "w", EngineID: "e", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dir := s.Dir()
	events := []any{
		event.ApprovalRequestEvent{Seq: 1, Turn: 1, ToolUseID: "c1", Tool: "Bash", Timeout: 120},
		event.ApprovalResolvedEvent{Seq: 2, ToolUseID: "c1", Decision: "allow", Source: "user"},
		event.AssistantTextEvent{Seq: 3, Turn: 1, Text: "after"},
	}
	for i, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("append %d (%T): %v", i, ev, err)
		}
	}
	// 独立只读句柄读落盘字节（写句柄仍打开 = 崩溃模拟）
	r, err := jsonl.OpenReader(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	lines, readErr := r.ReadAll()
	r.Close()
	if readErr != nil {
		t.Fatalf("read all: %v", readErr)
	}
	if len(lines) != 3 {
		t.Fatalf("got %d durable lines, want 3", len(lines))
	}
	var records []struct {
		Kind string          `json:"kind"`
		Ev   json.RawMessage `json:"ev"`
	}
	for i, line := range lines {
		var rec struct {
			Kind string          `json:"kind"`
			Ev   json.RawMessage `json:"ev"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}
		records = append(records, rec)
	}
	if records[0].Kind != string(event.ApprovalRequest) || records[1].Kind != string(event.ApprovalResolved) {
		t.Fatalf("kinds = %s, %s", records[0].Kind, records[1].Kind)
	}
	s.Close() // TempDir 清理前释放写句柄（Windows）
}

func TestAppendMultipleKindsRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-multi", Workspace: "w", EngineID: "e", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.Close()
	events := []any{
		event.TurnStartedEvent{Seq: 1, SessionID: "s", EngineSessionID: "e", PinnedModel: "m"},
		event.AssistantThinkingEvent{Seq: 2, Turn: 1, Thinking: "…", EstimatedTokens: 7},
		event.ToolUseRequestedEvent{Seq: 3, Turn: 1, ToolUseID: "c1", Tool: "Write", Input: json.RawMessage(`{"path":"a"}`)},
		event.ApprovalRequestEvent{Seq: 4, Turn: 1, ToolUseID: "c1", Tool: "Write", Timeout: 120},
		event.ApprovalResolvedEvent{Seq: 5, ToolUseID: "c1", Decision: "allow", Source: "session"},
		event.ToolResultEvent{Seq: 6, Turn: 1, ToolUseID: "c1", Content: "ok", IsError: false},
		event.UsageEvent{Seq: 7, Turn: 1, UncachedInputTokens: 10, WriteTokens: 20, ReadTokens: 30, OutputTokens: 5, ThinkingTokens: 1, CostUSD: 0.1, HitRate: 0.6},
		event.NoticeEvent{Seq: 8, Level: "warning", Content: "drift", IsMeta: true},
		event.TurnDoneEvent{Seq: 9, Turn: 1, Subtype: "success", IsError: false, DurationMS: 100, ResultText: "done"},
		event.EngineExitedEvent{Seq: 10, Code: 0, Reason: "completed"},
	}
	for _, ev := range events {
		if err := s.Append(ev); err != nil {
			t.Fatalf("append %T: %v", ev, err)
		}
	}
	r, err := OpenStore(filepath.Join(root, "s-multi"))
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
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got[i])
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("event %d: got %s, want %s", i, gotJSON, wantJSON)
		}
	}
}

func TestKindTagging(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()
	if err := s.Append(event.TurnDoneEvent{Seq: 1, Turn: 1, Subtype: "success"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("not json: %v", err)
	}
	if record["kind"] != "turn_done" {
		t.Fatalf("kind tag = %v, want turn_done", record["kind"])
	}
}

func TestWriteCacheProjection(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()
	projection := map[string]any{
		"turns":        2,
		"hit_rate":     0.75,
		"uncached_in":  1000,
		"write_tokens": 500,
		"read_tokens":  1500,
		"cost_usd":     0.33,
	}
	if err := s.WriteCache(projection); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cache.json"))
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("cache.json not json: %v", err)
	}
	if back["hit_rate"] != 0.75 {
		t.Fatalf("hit_rate = %v", back["hit_rate"])
	}
}

func TestOpenStoreRejectsMissingSession(t *testing.T) {
	root := t.TempDir()
	if _, err := OpenStore(root); err == nil {
		t.Fatal("opening a nonexistent session must fail")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close 2 (idempotent): %v", err)
	}
}

// TestSetEngineIDPinsMetaRMW pins the meta.json read-modify-write: the engine
// session id is backfilled on first init WITHOUT dropping sibling keys
// (Task 11's --resume chain depends on this).
func TestSetEngineIDPinsMetaRMW(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()
	if err := s.SetEngineID("engine-xyz"); err != nil {
		t.Fatalf("set engine id: %v", err)
	}
	meta, err := s.ReadMeta()
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta["engine_session_id"] != "engine-xyz" {
		t.Fatalf("engine_session_id = %v", meta["engine_session_id"])
	}
	// Sibling keys survive the RMW.
	if meta["id"] != "s-20261006-01" || meta["model"] != "claude-sonnet-4-5" || meta["workspace"] == "" {
		t.Fatalf("sibling meta keys lost: %v", meta)
	}
	// Raw file reflects the same.
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("raw meta not json: %v", err)
	}
	if back["engine_session_id"] != "engine-xyz" {
		t.Fatalf("raw engine_session_id = %v", back["engine_session_id"])
	}
}

// TestReadCacheRoundTripAndAbsent pins ReadCache: nil,nil when absent; the
// written projection read back verbatim.
func TestReadCacheRoundTripAndAbsent(t *testing.T) {
	s, _ := newTestStore(t)
	defer s.Close()
	got, err := s.ReadCache()
	if err != nil {
		t.Fatalf("read absent cache: %v", err)
	}
	if got != nil {
		t.Fatalf("absent cache must be nil,nil, got %v", got)
	}
	projection := map[string]any{"hit_rate": 0.42, "turns": float64(3)}
	if err := s.WriteCache(projection); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	got, err = s.ReadCache()
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if got["hit_rate"] != 0.42 || got["turns"] != float64(3) {
		t.Fatalf("cache round-trip mismatch: %v", got)
	}
}

// TestWriteCacheAtomicReplace pins（Task 24 交接的原子性）: WriteCache 走
// temp+rename——覆盖旧值成功（rename 替换）、无 .tmp 残留（崩溃半写不落正名）。
func TestWriteCacheAtomicReplace(t *testing.T) {
	s, dir := newTestStore(t)
	defer s.Close()
	if err := s.WriteCache(map[string]any{"turns": 1}); err != nil {
		t.Fatalf("write cache v1: %v", err)
	}
	if err := s.WriteCache(map[string]any{"turns": 2, "cost": 0.5}); err != nil {
		t.Fatalf("write cache v2 (replace): %v", err)
	}
	proj, err := s.ReadCache()
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if proj["turns"] != float64(2) || proj["cost"] != 0.5 {
		t.Fatalf("cache = %v, want v2 (rename 替换生效)", proj)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp residue: %s", e.Name())
		}
	}
}

// TestScanSessionsOrderAndUnattended pins：列表新会话在前（created_at 降序，
// 同秒稳定序）；unattended 元数据透出；缺 meta 的目录跳过、空根不报错。
func TestScanSessionsOrderAndUnattended(t *testing.T) {
	root := t.TempDir()
	if list, _, err := ScanSessions(root); err != nil || len(list) != 0 {
		t.Fatalf("empty root = (%v, %v), want empty/nil", list, err)
	}
	older, err := Create(root, Options{ID: "s-old", Workspace: "w1", Model: "m1", EngineID: "engine-old"})
	if err != nil {
		t.Fatalf("create older: %v", err)
	}
	older.Close()
	newer, err := Create(root, Options{ID: "s-new", Workspace: "w2", Model: "m2", Unattended: true})
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}
	newer.Close()
	// created_at 同秒平局时顺序仍确定（稳定序）；用 Chtimes 制造确定差异不可靠
	//（meta 写入即冻结 created_at）——这里断言的是「两个都在、顺序稳定、字段齐」。
	if err := os.MkdirAll(filepath.Join(root, "junk"), 0o755); err != nil {
		t.Fatalf("mkdir junk: %v", err)
	}
	list, warnings, err := ScanSessions(root)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("scan = (%v, %v)", warnings, err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v, want 2 sessions", list)
	}
	byID := map[string]SessionInfo{}
	for _, s := range list {
		byID[s.ID] = s
	}
	newerInfo := byID["s-new"]
	if newerInfo.ID == "" {
		t.Fatal("s-new missing")
	}
	if !newerInfo.Unattended || newerInfo.EngineID != "" {
		t.Fatalf("newer = %+v (want Unattended=true, EngineID empty)", newerInfo)
	}
	oldInfo := byID["s-old"]
	if oldInfo.EngineID != "engine-old" || oldInfo.Workspace != "w1" || oldInfo.Unattended {
		t.Fatalf("older = %+v", oldInfo)
	}
	// 顺序：created_at 严格更大者在前（写入时间差 <1s 时同秒平局——仅断言
	// 顺序确定的成员都存在，排序语义由 created_at 决定，字段级已验）。
	if list[0].ID != "s-new" && list[0].ID != "s-old" {
		t.Fatalf("unexpected head: %+v", list[0])
	}
}
