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

// TestReconcileUsageFileOrderWins pins（T8.2 对账）：重复回合号按文件序取
// 后者（最近观察为准）；无重复原样保留；顺序按回合号升序输出。
func TestReconcileUsageFileOrderWins(t *testing.T) {
	recs := []Record{
		{Turn: 1, Uncached: 10, Write: 0, Read: 90},
		{Turn: 2, Uncached: 50, Write: 0, Read: 50},
		{Turn: 1, Uncached: 20, Write: 0, Read: 80}, // 重复：文件序后者为准
	}
	out := ReconcileUsage(recs)
	if len(out) != 2 {
		t.Fatalf("reconciled = %d records, want 2", len(out))
	}
	// 回合 1 取后写的 {20,80}
	if out[0].Turn != 1 || out[0].Uncached != 20 || out[0].Read != 80 {
		t.Fatalf("turn1 = %+v, want file-order winner {20,80}", out[0])
	}
	if out[1].Turn != 2 || out[1].Uncached != 50 {
		t.Fatalf("turn2 = %+v", out[1])
	}
}

// TestAggregateRecords pins：跨进程聚合口径与 Meter.Report 一致——token 各项
// 直加、cost/saving 直加、session_hit 加权（Σread/(Σuncached+Σwrite+Σread)）
// 而非逐回合均值、cold_turns 计数。
func TestAggregateRecords(t *testing.T) {
	recs := []Record{
		{Turn: 1, Uncached: 100, Write: 300, Read: 100, Output: 50, Cost: 1.5, Saving: 0.8, ExpectedCold: true},
		{Turn: 2, Uncached: 10, Write: 0, Read: 390, Output: 70, Cost: 0.2, Saving: 0.4},
	}
	rep := AggregateRecords(recs)
	if rep.Turns != 2 || rep.Uncached != 110 || rep.Write != 300 || rep.Read != 490 || rep.Output != 120 {
		t.Fatalf("aggregate = %+v", rep)
	}
	if rep.ColdTurns != 1 {
		t.Fatalf("cold turns = %d, want 1", rep.ColdTurns)
	}
	if abs(rep.Cost-1.7) > 1e-9 || abs(rep.Saving-1.2) > 1e-9 {
		t.Fatalf("cost/saving = %v/%v", rep.Cost, rep.Saving)
	}
	wantHit := float64(490) / float64(110+300+490)
	if abs(rep.SessionHit-wantHit) > 1e-9 {
		t.Fatalf("session_hit = %v, want weighted %v", rep.SessionHit, wantHit)
	}
}

// TestReadUsageReport pins：读 usage 文件 → 对账 → 聚合全链路；缺文件 =
// 零值 Report（会话尚无记账是合法状态，不是错误）。
func TestReadUsageReport(t *testing.T) {
	root := t.TempDir()
	sid := "s-agg"
	m, err := NewMeter(MeterOptions{Root: root, SessionID: sid, Pricing: testPricing()})
	if err != nil {
		t.Fatalf("new meter: %v", err)
	}
	if _, err := m.Observe(TurnUsage{InputTokens: 100, CacheWriteTokens: 200, CacheReadTokens: 300, OutputTokens: 20}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	rep, err := ReadUsageReport(root, sid)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if rep.Turns != 1 || rep.Read != 300 || rep.Write != 200 {
		t.Fatalf("report = %+v", rep)
	}
	// 缺文件：零值合法
	empty, err := ReadUsageReport(root, "s-none")
	if err != nil || empty.Turns != 0 {
		t.Fatalf("missing session = (%+v, %v), want zero/nil", empty, err)
	}
}

// TestResumeTurnOffset pins（T26 评审 Important-1 的修复机制）：resume 重开
// Meter 时以 NextTurnNumber 为偏移——同 sessionID 的记账文件续号（turn 3,4…），
// 不产生重复回合号；全量对账 Turns = 两次运行之和。
func TestResumeTurnOffset(t *testing.T) {
	root := t.TempDir()
	sid := "s-resume"
	m1, err := NewMeter(MeterOptions{Root: root, SessionID: sid, Pricing: testPricing()})
	if err != nil {
		t.Fatalf("meter1: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := m1.Observe(TurnUsage{InputTokens: 10, CacheReadTokens: 30, OutputTokens: 5}); err != nil {
			t.Fatalf("observe1: %v", err)
		}
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("close1: %v", err)
	}
	next, err := NextTurnNumber(root, sid)
	if err != nil {
		t.Fatalf("next turn: %v", err)
	}
	if next != 2 {
		t.Fatalf("NextTurnNumber = %d, want 2", next)
	}
	// 空/缺文件 → 0
	if empty, err := NextTurnNumber(root, "s-none"); err != nil || empty != 0 {
		t.Fatalf("NextTurnNumber(missing) = (%d, %v), want (0, nil)", empty, err)
	}
	// resume：偏移续号
	m2, err := NewMeter(MeterOptions{Root: root, SessionID: sid, Pricing: testPricing(), TurnOffset: next})
	if err != nil {
		t.Fatalf("meter2: %v", err)
	}
	rec, err := m2.Observe(TurnUsage{InputTokens: 10, CacheReadTokens: 30, OutputTokens: 5})
	if err != nil {
		t.Fatalf("observe2: %v", err)
	}
	if rec.Turn != 3 {
		t.Fatalf("resumed turn = %d, want 3", rec.Turn)
	}
	if _, err := m2.Observe(TurnUsage{InputTokens: 10, CacheReadTokens: 30, OutputTokens: 5}); err != nil {
		t.Fatalf("observe2b: %v", err)
	}
	if err := m2.Close(); err != nil {
		t.Fatalf("close2: %v", err)
	}
	rep, err := ReadUsageReport(root, sid)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.Turns != 4 {
		t.Fatalf("reconciled turns = %d, want 4 (no duplicates)", rep.Turns)
	}
}
