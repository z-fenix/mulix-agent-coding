package sessionx

import (
	"testing"

	"mulix/internal/event"
)

// TestBidirectionalMappingRoundTrip pins: mulix id → engine id (meta.json),
// engine id → mulix session (reverse lookup for --resume <engine-id>).
func TestBidirectionalMappingRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-map", Workspace: "w", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.Close()

	// 引擎 init 到达前：映射为空。
	if got, err := s.EngineID(); err != nil || got != "" {
		t.Fatalf("before backfill: EngineID=%q err=%v", got, err)
	}
	if _, found, err := FindByEngineID(root, "engine-1"); err != nil || found {
		t.Fatalf("before backfill: FindByEngineID found=%v err=%v", found, err)
	}

	// 回填（Task 9 SetEngineID；映射随写随查）。
	if err := s.SetEngineID("engine-1"); err != nil {
		t.Fatalf("set engine id: %v", err)
	}
	got, err := s.EngineID()
	if err != nil || got != "engine-1" {
		t.Fatalf("after backfill: EngineID=%q err=%v", got, err)
	}

	// 双向：engine → mulix。
	info, found, err := FindByEngineID(root, "engine-1")
	if err != nil || !found {
		t.Fatalf("reverse lookup failed: found=%v err=%v", found, err)
	}
	if info.ID != "s-map" {
		t.Fatalf("reverse lookup returned wrong session: %+v", info)
	}

	// 未命中的 engine id：not found（非错误）。
	if _, found, err := FindByEngineID(root, "engine-404"); err != nil || found {
		t.Fatalf("miss must be not-found without error: found=%v err=%v", found, err)
	}

	// 空 engine id：直接未命中（防静默错配到尚未回填的会话）。
	if _, found, err := FindByEngineID(root, ""); err != nil || found {
		t.Fatalf("empty engine id must be not-found without error: found=%v err=%v", found, err)
	}
}

// TestEngineIDFromOptions pins: EngineID set at creation time is readable
// without a backfill (Create(Options.EngineID) is Task 9's field).
func TestEngineIDFromOptions(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-pre", Workspace: "w", EngineID: "engine-pre", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.Close()
	got, err := s.EngineID()
	if err != nil || got != "engine-pre" {
		t.Fatalf("EngineID=%q err=%v", got, err)
	}
	info, found, err := FindByEngineID(root, "engine-pre")
	if err != nil || !found || info.ID != "s-pre" {
		t.Fatalf("reverse lookup from options-seeded id: found=%v err=%v info=%+v", found, err, info)
	}
}

// TestReplayThenRemap pins the recovery-chain remap: after replay, a resumed
// engine may mint a NEW engine session id (--fork-session semantics) — the
// mapping updates and the reverse lookup tracks the CURRENT id.
func TestReplayThenRemap(t *testing.T) {
	root := t.TempDir()
	s, err := Create(root, Options{ID: "s-remap", Workspace: "w", EngineID: "engine-old", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer s.Close()
	if err := s.Append(event.TurnStartedEvent{Seq: 1, SessionID: "s-remap", EngineSessionID: "engine-old", PinnedModel: "m"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// 恢复：回放后 fork 出新 engine id。
	if _, _, err := s.Replay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := s.SetEngineID("engine-new"); err != nil {
		t.Fatalf("remap: %v", err)
	}
	got, err := s.EngineID()
	if err != nil || got != "engine-new" {
		t.Fatalf("remapped EngineID=%q err=%v", got, err)
	}
	if _, found, _ := FindByEngineID(root, "engine-new"); !found {
		t.Fatal("current engine id must resolve")
	}
}
