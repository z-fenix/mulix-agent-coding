package cache

import (
	"testing"
	"time"
)

func rec(turn int64, uncached, write, read int64, cold bool) Record {
	r := Record{Turn: turn, Uncached: uncached, Write: write, Read: read, ExpectedCold: cold}
	if denom := uncached + write + read; denom > 0 {
		r.Hit = float64(read) / float64(denom)
	}
	return r
}

// TestGuardWindowWeightedHit pins: 窗口命中率 = Σread/Σ(uncached+write+read)
// （加权，与 session 口径一致）；低于 floor → breach。
func TestGuardWindowWeightedHit(t *testing.T) {
	g := NewGuard(GuardOptions{}) // 默认 floor 0.70 / window 10
	// 10 个有效回合：read 合计 2000，分母合计 5000 → 0.40 < 0.70 → breach
	var recs []Record
	for i := 1; i <= 10; i++ {
		recs = append(recs, rec(int64(i), 300, 0, 200, false))
	}
	notice, breach := g.Evaluate(recs)
	if !breach {
		t.Fatal("0.40 window hit must breach floor 0.70")
	}
	if abs(notice.WindowHit-0.40) > 1e-9 {
		t.Fatalf("window hit = %v, want 0.40", notice.WindowHit)
	}
	if notice.WindowTurns != 10 || notice.FirstTurn != 1 || notice.LastTurn != 10 {
		t.Fatalf("notice wrong: %+v", notice)
	}
}

// TestGuardExcludesExpectedCold pins: expected_cold 回合完全不进窗口
// （不参与命中率，也不占 10 个名额）。
func TestGuardExcludesExpectedCold(t *testing.T) {
	g := NewGuard(GuardOptions{})
	// 12 回合：#3、#11 冷；有效 10 个，Σread 7000 / Σdenom 10000 → 0.70 == floor
	var recs []Record
	for i := 1; i <= 12; i++ {
		cold := i == 3 || i == 11
		recs = append(recs, rec(int64(i), 300, 0, 700, cold))
	}
	_, breach := g.Evaluate(recs)
	if breach {
		t.Fatal("0.70 window hit must NOT breach floor 0.70 (boundary)")
	}
	// 再加一个冷回合也不改变窗口（仍取最近 10 个有效）
	recs = append(recs, rec(13, 300, 0, 0, true))
	notice, breach := g.Evaluate(recs)
	if breach {
		t.Fatal("cold tail must not change the valid window")
	}
	if notice.WindowTurns != 10 || notice.LastTurn != 12 {
		t.Fatalf("cold turns must be excluded from window: %+v", notice)
	}
}

// TestGuardFloorBoundary pins: hit == floor 不告警（< floor 才告警）；
// 只低一点就告警。
func TestGuardFloorBoundary(t *testing.T) {
	g := NewGuard(GuardOptions{})
	eq := make([]Record, 0, 10)
	for i := 1; i <= 10; i++ {
		eq = append(eq, rec(int64(i), 300, 0, 700, false)) // 7000/10000 = 0.70
	}
	if _, breach := g.Evaluate(eq); breach {
		t.Fatal("hit == floor must not breach")
	}
	just := make([]Record, 0, 10)
	for i := 1; i <= 10; i++ {
		just = append(just, rec(int64(i), 301, 0, 699, false)) // 6990/10000 = 0.699
	}
	if _, breach := g.Evaluate(just); !breach {
		t.Fatal("0.699 < 0.70 must breach")
	}
}

// TestGuardRequiresFullWindow pins: 有效回合不足 window 时不评估
// （预热期 read=0 是正常态，不产生误报）。
func TestGuardRequiresFullWindow(t *testing.T) {
	g := NewGuard(GuardOptions{})
	var recs []Record
	for i := 1; i <= 9; i++ {
		recs = append(recs, rec(int64(i), 100, 0, 0, false)) // 9 个 0 命中
	}
	if _, breach := g.Evaluate(recs); breach {
		t.Fatal("partial window (9 valid) must not evaluate")
	}
	recs = append(recs, rec(10, 100, 0, 0, false)) // 第 10 个有效 → 0% 命中
	if _, breach := g.Evaluate(recs); !breach {
		t.Fatal("full window of zero hits must breach")
	}
}

// TestGuardConfigurable pins: floor/window 可配置（cache.hit_rate_floor /
// 窗口大小），零值取默认。
func TestGuardConfigurable(t *testing.T) {
	// 自定义：window 3, floor 0.5 → 3 个有效回合即可评估
	g := NewGuard(GuardOptions{Floor: 0.5, WindowSize: 3})
	recs := []Record{
		rec(1, 100, 0, 40, false), // 0.4
		rec(2, 100, 0, 40, false),
		rec(3, 100, 0, 40, false), // Σ 120/300 = 0.4 < 0.5
	}
	if _, breach := g.Evaluate(recs); !breach {
		t.Fatal("custom floor 0.5 must breach at 0.4")
	}
	// 零值默认
	def := NewGuard(GuardOptions{})
	if def.floor != 0.70 || def.window != 10 {
		t.Fatalf("defaults = floor %v window %v, want 0.70/10", def.floor, def.window)
	}
}

// TestGuardZeroDenominator pins: 窗口全零分母（病态空输入）→ 不告警、无 NaN。
func TestGuardZeroDenominator(t *testing.T) {
	g := NewGuard(GuardOptions{})
	var recs []Record
	for i := 1; i <= 10; i++ {
		recs = append(recs, rec(int64(i), 0, 0, 0, false))
	}
	notice, breach := g.Evaluate(recs)
	if breach {
		t.Fatal("zero-denominator window must not breach")
	}
	if notice.WindowHit != 0 {
		t.Fatalf("window hit = %v, want 0 (no NaN)", notice.WindowHit)
	}
}

// TestMeterRecords pins Meter.Records()：Guard 的会话内 Record 流来源。
func TestMeterRecords(t *testing.T) {
	root := t.TempDir()
	m, err := NewMeter(MeterOptions{Root: root, SessionID: "s-guard", Pricing: testPricing(), ColdAfterIdle: 4 * time.Minute})
	if err != nil {
		t.Fatalf("meter: %v", err)
	}
	defer m.Close()
	if _, err := m.Observe(TurnUsage{InputTokens: 100, CacheReadTokens: 70}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if _, err := m.Observe(TurnUsage{InputTokens: 100, CacheReadTokens: 0}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	recs := m.Records()
	if len(recs) != 2 || recs[0].Read != 70 || recs[1].Read != 0 {
		t.Fatalf("records wrong: %+v", recs)
	}
	// 值语义：外部改动不影响 Meter 内部
	recs[0].Read = 999
	if m.Records()[0].Read != 70 {
		t.Fatal("Records must return a copy")
	}
}
