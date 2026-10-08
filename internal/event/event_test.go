package event

import (
	"encoding/json"
	"testing"
)

// roundTrip marshals v, unmarshals into a fresh T, marshals again, and
// asserts both marshalings are identical byte-for-byte (JSON round-trip
// stability: field names, omitempty behavior, and value fidelity).
func roundTrip[T any](t *testing.T, v T) {
	t.Helper()
	first, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back T
	if err := json.Unmarshal(first, &back); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, first)
	}
	second, err := json.Marshal(back)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("round-trip not stable:\n first: %s\nsecond: %s", first, second)
	}
}

func TestKindConstants(t *testing.T) {
	kinds := map[Kind]string{
		TurnStarted:       "turn_started",
		AssistantText:     "assistant_text",
		AssistantThinking: "assistant_thinking",
		ToolUseRequested:  "tool_use_requested",
		ToolResult:        "tool_result",
		ApprovalRequest:   "approval_request",
		ApprovalResolved:  "approval_resolved",
		Usage:             "usage",
		Notice:            "notice",
		TurnDone:          "turn_done",
		EngineExited:      "engine_exited",
	}
	for kind, want := range kinds {
		if string(kind) != want {
			t.Errorf("kind %q != %q", kind, want)
		}
	}
}

// TestDelegationKindsAndJSONTags 钉住 P2 委派链事件（Task 1）：Kind 常量值 +
// json tag 拼写（golden 断言——tag 漂移即破坏镜像 1:1 红线）。
func TestDelegationKindsAndJSONTags(t *testing.T) {
	kinds := map[Kind]string{
		DelegationStarted:   "delegation_started",
		DelegationCompleted: "delegation_completed",
		DelegationCancelled: "delegation_cancelled",
		DelegationFailed:    "delegation_failed",
	}
	for kind, want := range kinds {
		if string(kind) != want {
			t.Errorf("kind %q != %q", kind, want)
		}
	}
	goldens := []struct {
		ev   any
		want string
	}{
		{DelegationStartedEvent{Seq: 1, SubSessionID: "sub1", Task: "add tests", Depth: 1}, `{"seq":1,"sub_session_id":"sub1","task":"add tests","depth":1}`},
		{DelegationCompletedEvent{Seq: 2, SubSessionID: "sub1", Summary: "done", TotalTokens: 100, CostUSDCents: 7}, `{"seq":2,"sub_session_id":"sub1","summary":"done","total_tokens":100,"cost_usd_cents":7}`},
		{DelegationCancelledEvent{Seq: 3, SubSessionID: "sub1", Reason: "user"}, `{"seq":3,"sub_session_id":"sub1","reason":"user"}`},
		{DelegationFailedEvent{Seq: 4, SubSessionID: "sub1", Reason: "engine crash"}, `{"seq":4,"sub_session_id":"sub1","reason":"engine crash"}`},
	}
	for _, g := range goldens {
		got, err := json.Marshal(g.ev)
		if err != nil {
			t.Fatalf("marshal %T: %v", g.ev, err)
		}
		if string(got) != g.want {
			t.Errorf("%T tag drift:\n got:  %s\n want: %s", g.ev, got, g.want)
		}
	}
}

func TestTurnStartedRoundTrip(t *testing.T) {
	roundTrip(t, TurnStartedEvent{Seq: 1, SessionID: "s-1", EngineSessionID: "e-1", PinnedModel: "m", Unattended: true})
}

func TestAssistantTextRoundTrip(t *testing.T) {
	roundTrip(t, AssistantTextEvent{Seq: 2, Turn: 1, Text: "hello 世界"})
}

func TestAssistantThinkingRoundTrip(t *testing.T) {
	roundTrip(t, AssistantThinkingEvent{Seq: 3, Turn: 1, Thinking: "…", EstimatedTokens: 42})
}

func TestToolUseRequestedRoundTrip(t *testing.T) {
	roundTrip(t, ToolUseRequestedEvent{Seq: 4, Turn: 1, ToolUseID: "call_1", Tool: "Write", Input: json.RawMessage(`{"file_path":"a.txt"}`)})
}

func TestToolResultRoundTrip(t *testing.T) {
	roundTrip(t, ToolResultEvent{Seq: 5, Turn: 1, ToolUseID: "call_1", Content: "ok", IsError: true})
}

func TestApprovalRequestRoundTrip(t *testing.T) {
	roundTrip(t, ApprovalRequestEvent{
		Seq: 6, Turn: 1, ToolUseID: "call_1", Tool: "Write",
		Input:   json.RawMessage(`{"path":"a"}`),
		Timeout: 120,
	})
}

func TestApprovalResolvedRoundTrip(t *testing.T) {
	roundTrip(t, ApprovalResolvedEvent{Seq: 7, ToolUseID: "call_1", Decision: "allow", Source: "session", ResolvedBy: "tui"})
}

func TestUsageRoundTrip(t *testing.T) {
	roundTrip(t, UsageEvent{
		Seq: 8, Turn: 1,
		UncachedInputTokens: 100, WriteTokens: 20, ReadTokens: 30, OutputTokens: 50, ThinkingTokens: 10,
		CostUSD: 0.109, HitRate: 0.25,
	})
}

func TestNoticeRoundTrip(t *testing.T) {
	roundTrip(t, NoticeEvent{Seq: 9, Level: "warning", Content: "engine drift", IsMeta: true})
}

func TestTurnDoneRoundTrip(t *testing.T) {
	roundTrip(t, TurnDoneEvent{Seq: 10, Turn: 1, Subtype: "success", IsError: false, DurationMS: 6575, ResultText: "done"})
}

func TestEngineExitedRoundTrip(t *testing.T) {
	roundTrip(t, EngineExitedEvent{Seq: 11, Code: 0, Reason: "completed"})
}

// TestTurnStartedGolden pins the actual JSON field spellings (the round-trip
// test above pins tag consistency; this pins tag identity).
func TestTurnStartedGolden(t *testing.T) {
	got, err := json.Marshal(TurnStartedEvent{Seq: 1, SessionID: "s-1", EngineSessionID: "e-1", PinnedModel: "m", Unattended: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"seq":1,"session_id":"s-1","engine_session_id":"e-1","pinned_model":"m","unattended":true}`
	if string(got) != want {
		t.Fatalf("golden mismatch:\n got:  %s\n want: %s", got, want)
	}
}

// TestUnknownFieldsTolerated pins the red line: decoding an event with extra
// unknown fields must succeed (unknown-field tolerance for forward
// compatibility with engine upgrades).
func TestUnknownFieldsTolerated(t *testing.T) {
	raw := []byte(`{"seq":12,"session_id":"s","engine_session_id":"e","pinned_model":"m","unattended":true,"future_field":{"x":1}}`)
	var ev TurnStartedEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unknown fields must be tolerated: %v", err)
	}
	if ev.SessionID != "s" || ev.EngineSessionID != "e" {
		t.Fatalf("known fields lost: %+v", ev)
	}
}
