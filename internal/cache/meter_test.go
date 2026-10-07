package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPInPerM = 3.0   // $/M input tokens
const testPOutPerM = 15.0 // $/M output tokens

func testPricing() Pricing {
	return Pricing{InputPerMTok: testPInPerM, OutputPerMTok: testPOutPerM}
}

func newTestMeter(t *testing.T) (*Meter, string, *time.Time) {
	t.Helper()
	root := t.TempDir()
	session := "s-meter"
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	m, err := NewMeter(MeterOptions{
		Root:          root,
		SessionID:     session,
		Pricing:       testPricing(),
		ColdAfterIdle: 4 * time.Minute,
		Now:           func() time.Time { return *nowHolder(&now) },
	})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	return m, filepath.Join(root, "usage", session+".jsonl"), &now
}

// nowHolder returns a pointer to t (helper to keep the closure one-liner tidy).
func nowHolder(p *time.Time) *time.Time { return p }

func TestObserveFormulas(t *testing.T) {
	m, path, _ := newTestMeter(t)
	defer m.Close()
	rec, err := m.Observe(TurnUsage{
		InputTokens:      1000,
		CacheWriteTokens: 2000,
		CacheReadTokens:  4000,
		OutputTokens:     500,
		ThinkingTokens:   100,
	})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if rec.Uncached != 1000 || rec.Write != 2000 || rec.Read != 4000 || rec.Output != 500 || rec.Thinking != 100 {
		t.Fatalf("token counts wrong: %+v", rec)
	}
	// hit = read/(uncached+write+read) = 4000/7000
	if want := 4000.0 / 7000.0; abs(rec.Hit-want) > 1e-9 {
		t.Fatalf("hit = %v, want %v", rec.Hit, want)
	}
	// cost = uncached·P_in + write·1.25·P_in + read·0.1·P_in + output·P_out
	wantCost := 1000*3e-6 + 2000*1.25*3e-6 + 4000*0.1*3e-6 + 500*15e-6
	if abs(rec.Cost-wantCost) > 1e-9 {
		t.Fatalf("cost = %v, want %v", rec.Cost, wantCost)
	}
	// saving = read·0.9·P_in
	wantSaving := 4000 * 0.9 * 3e-6
	if abs(rec.Saving-wantSaving) > 1e-9 {
		t.Fatalf("saving = %v, want %v", rec.Saving, wantSaving)
	}
	// 记账文件已落（同进程内即可读）
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read usage jsonl: %v", err)
	}
	if got := len(strings.Split(strings.TrimRight(string(raw), "\n"), "\n")); got != 1 {
		t.Fatalf("usage jsonl lines = %d, want 1", got)
	}
}

func TestObserveHitZeroDenominator(t *testing.T) {
	m, _, _ := newTestMeter(t)
	defer m.Close()
	rec, err := m.Observe(TurnUsage{OutputTokens: 10})
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if rec.Hit != 0 {
		t.Fatalf("hit with zero denominator must be 0, got %v", rec.Hit)
	}
}

// TestTTLColdBoundary pins §8.2: 空闲 ≥ cold_after_idle 后首个回合标 expected_cold
// （3m59s 热 / 4m01s 冷）。
func TestTTLColdBoundary(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	clock := base

	m, err := NewMeter(MeterOptions{
		Root: root, SessionID: "s-ttl", Pricing: testPricing(),
		ColdAfterIdle: 4 * time.Minute,
		Now:           func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	defer m.Close()
	// 回合 1（无历史活动：首个回合不标冷）
	rec1, err := m.Observe(TurnUsage{InputTokens: 100})
	if err != nil {
		t.Fatalf("observe 1: %v", err)
	}
	if rec1.ExpectedCold {
		t.Fatal("first turn must not be cold")
	}
	m.MarkActivity(clock) // 用户在回合 1 结束后交互

	// 3m59s 后：热
	clock = base.Add(3*time.Minute + 59*time.Second)
	rec2, err := m.Observe(TurnUsage{InputTokens: 100})
	if err != nil {
		t.Fatalf("observe 2: %v", err)
	}
	if rec2.ExpectedCold {
		t.Fatal("3m59s idle must stay hot")
	}
	m.MarkActivity(clock)

	// 4m01s 后：冷
	clock = base.Add(8*time.Minute + 1*time.Second)
	rec3, err := m.Observe(TurnUsage{InputTokens: 100})
	if err != nil {
		t.Fatalf("observe 3: %v", err)
	}
	if !rec3.ExpectedCold {
		t.Fatal("4m01s idle must be cold")
	}
}

func TestUsageJSONLRoundTripAndAppend(t *testing.T) {
	root := t.TempDir()
	m1, err := NewMeter(MeterOptions{Root: root, SessionID: "s-rt", Pricing: testPricing(), ColdAfterIdle: 4 * time.Minute})
	if err != nil {
		t.Fatalf("meter 1: %v", err)
	}
	if _, err := m1.Observe(TurnUsage{InputTokens: 10, OutputTokens: 5}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, err := m1.Observe(TurnUsage{InputTokens: 20, CacheReadTokens: 30, OutputTokens: 7}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("close meter 1: %v", err)
	}
	path := filepath.Join(root, "usage", "s-rt.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var recs []Record
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line not json: %v (%s)", err, line)
		}
		recs = append(recs, r)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Uncached != 10 || recs[1].Read != 30 {
		t.Fatalf("records wrong: %+v", recs)
	}

	// 重开（新 Meter 实例同会话文件）→ 追加不截断。
	m2, err := NewMeter(MeterOptions{Root: root, SessionID: "s-rt", Pricing: testPricing(), ColdAfterIdle: 4 * time.Minute})
	if err != nil {
		t.Fatalf("meter 2: %v", err)
	}
	defer m2.Close()
	if _, err := m2.Observe(TurnUsage{InputTokens: 40}); err != nil {
		t.Fatalf("observe after reopen: %v", err)
	}
	raw2, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(raw2), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("after reopen got %d lines, want 3 (append, not truncate)", len(lines))
	}
}

func TestReportSessionCumulative(t *testing.T) {
	root := t.TempDir()
	m, err := NewMeter(MeterOptions{Root: root, SessionID: "s-rep", Pricing: testPricing(), ColdAfterIdle: 4 * time.Minute})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	defer m.Close()
	// 回合 1：uncached 1000, read 3000, output 100
	if _, err := m.Observe(TurnUsage{InputTokens: 1000, CacheReadTokens: 3000, OutputTokens: 100}); err != nil {
		t.Fatalf("observe 1: %v", err)
	}
	// 回合 2：uncached 500, write 1500, read 3500, output 200
	if _, err := m.Observe(TurnUsage{InputTokens: 500, CacheWriteTokens: 1500, CacheReadTokens: 3500, OutputTokens: 200}); err != nil {
		t.Fatalf("observe 2: %v", err)
	}
	rep, err := m.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Turns != 2 {
		t.Fatalf("turns = %d", rep.Turns)
	}
	if rep.Uncached != 1500 || rep.Write != 1500 || rep.Read != 6500 || rep.Output != 300 {
		t.Fatalf("sums wrong: %+v", rep)
	}
	// session_hit = 6500 / (1500+1500+6500) = 6500/9500（加权，非均值）
	if want := 6500.0 / 9500.0; abs(rep.SessionHit-want) > 1e-9 {
		t.Fatalf("session_hit = %v, want %v", rep.SessionHit, want)
	}
	// Σcost 与 Σsaving
	wantCost := (1000*3e-6 + 3000*0.1*3e-6 + 100*15e-6) + (500*3e-6 + 1500*1.25*3e-6 + 3500*0.1*3e-6 + 200*15e-6)
	if abs(rep.Cost-wantCost) > 1e-9 {
		t.Fatalf("Σcost = %v, want %v", rep.Cost, wantCost)
	}
	wantSaving := (3000 + 3500) * 0.9 * 3e-6
	if abs(rep.Saving-wantSaving) > 1e-9 {
		t.Fatalf("Σsaving = %v, want %v", rep.Saving, wantSaving)
	}
}

func TestReportEmptySession(t *testing.T) {
	m, _, _ := newTestMeter(t)
	defer m.Close()
	rep, err := m.Report()
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Turns != 0 || rep.SessionHit != 0 {
		t.Fatalf("empty session report wrong: %+v", rep)
	}
}

func TestReadUsageOffline(t *testing.T) {
	root := t.TempDir()
	m, err := NewMeter(MeterOptions{Root: root, SessionID: "s-off", Pricing: testPricing(), ColdAfterIdle: 4 * time.Minute})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	defer m.Close()
	if _, err := m.Observe(TurnUsage{InputTokens: 42}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	recs, err := ReadUsage(filepath.Join(root, "usage", "s-off.jsonl"))
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if len(recs) != 1 || recs[0].Uncached != 42 {
		t.Fatalf("offline read wrong: %+v", recs)
	}
	// ts 往返：观察时刻进记账（审计/基准关联）
	if recs[0].TS == "" {
		t.Fatal("record must carry a ts")
	}
	if _, err := time.Parse(time.RFC3339, recs[0].TS); err != nil {
		t.Fatalf("ts not RFC3339: %v (%s)", err, recs[0].TS)
	}
	// 缺失文件：(nil, nil)
	missRecs, err := ReadUsage(filepath.Join(root, "usage", "missing.jsonl"))
	if err != nil || missRecs != nil {
		t.Fatalf("missing file must be (nil, nil), got (%v, %v)", missRecs, err)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
