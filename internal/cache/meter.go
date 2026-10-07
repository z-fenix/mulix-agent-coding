package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"mulix/internal/jsonl"
)

// Pricing 是每百万 token 定价（design §8.2 cost/saving 公式；定价可配置——
// 零值 = 成本/节省恒 0，模型定价表由后续任务的配置接入提供）。
type Pricing struct {
	InputPerMTok  float64 // $/M input tokens（uncached 基准价）
	OutputPerMTok float64 // $/M output tokens
}

// TurnUsage 是引擎单回合 usage 快照（本回合口径，来自 result.usage；
// Anthropic 语义：input_tokens 不含缓存命中/写入，总输入 = input + cache_creation + cache_read）。
type TurnUsage struct {
	InputTokens      int64 `json:"input_tokens"`       // uncached
	CacheWriteTokens int64 `json:"cache_write_tokens"` // cache_creation_input_tokens
	CacheReadTokens  int64 `json:"cache_read_tokens"`  // cache_read_input_tokens
	OutputTokens     int64 `json:"output_tokens"`
	ThinkingTokens   int64 `json:"thinking_tokens"` // output 细目
}

// Record 是单回合记账（JSONL 一行；FR-009：全部四类 token 计数落盘）。
type Record struct {
	Turn         int64   `json:"turn"`
	TS           string  `json:"ts,omitempty"` // RFC3339 观察时刻（审计/基准关联；旧记录无此字段）
	Uncached     int64   `json:"uncached"`
	Write        int64   `json:"write"`
	Read         int64   `json:"read"`
	Output       int64   `json:"output"`
	Thinking     int64   `json:"thinking"`
	Hit          float64 `json:"hit"`           // read/(uncached+write+read)；分母 0 时 0
	Cost         float64 `json:"cost"`          // §8.2 公式成本（可配置定价）
	Saving       float64 `json:"saving"`        // 相对全未命中的节省估算
	ExpectedCold bool    `json:"expected_cold"` // TTL 感知标注（§8.2）
}

// Report 是会话累计汇总（Report()；session_hit 为加权口径，非逐回合均值）。
type Report struct {
	Turns      int64   `json:"turns"`
	Uncached   int64   `json:"uncached"`
	Write      int64   `json:"write"`
	Read       int64   `json:"read"`
	Output     int64   `json:"output"`
	SessionHit float64 `json:"session_hit"`
	Cost       float64 `json:"cost"`
	Saving     float64 `json:"saving"`
	ColdTurns  int64   `json:"cold_turns"`
}

// MeterOptions 是 NewMeter 参数。
type MeterOptions struct {
	Root          string // 记账根目录；文件 = <root>/usage/<session>.jsonl
	SessionID     string
	Pricing       Pricing
	ColdAfterIdle time.Duration // 默认 4m（design §8.2：5min TTL − 1min 余量）
	// Now 时钟注入（测试用）；nil = time.Now。
	Now func() time.Time
}

// Meter 是 §8.2 监控层：逐回合记账（JSONL 持久）+ 会话累计 + TTL 感知。
// 非并发安全（单写者；与 sessionx.Store 同约定）。
type Meter struct {
	path          string
	pricing       Pricing
	coldAfterIdle time.Duration
	now           func() time.Time
	writer        *jsonl.Writer
	turn          int64
	lastActivity  time.Time
	haveActivity  bool
	sum           Report
	records       []Record
}

// NewMeter 创建记账器并打开 JSONL 追加句柄（不存在则建目录）。
func NewMeter(opts MeterOptions) (*Meter, error) {
	if opts.Root == "" || opts.SessionID == "" {
		return nil, fmt.Errorf("cache: meter requires root and session id")
	}
	cold := opts.ColdAfterIdle
	if cold <= 0 {
		cold = 4 * time.Minute
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	dir := filepath.Join(opts.Root, "usage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: mkdir usage: %w", err)
	}
	path := filepath.Join(dir, opts.SessionID+".jsonl")
	w, err := jsonl.OpenWriter(path)
	if err != nil {
		return nil, fmt.Errorf("cache: open usage jsonl: %w", err)
	}
	return &Meter{
		path:          path,
		pricing:       opts.Pricing,
		coldAfterIdle: cold,
		now:           now,
		writer:        w,
	}, nil
}

// MarkActivity 记录用户/系统交互时刻（TTL 基准；Observe 也会刷新）。
func (m *Meter) MarkActivity(t time.Time) {
	m.lastActivity = t
	m.haveActivity = true
}

// Observe 记账一个回合：按 §8.2 公式计算 hit/cost/saving，标注 expected_cold
// （距上次活动 ≥ cold_after_idle 的首个回合），追加 JSONL 并刷新活动基准。
// 活动基准刷新于 Observe 进入时刻（now）——接线要求 usage 到达即调用 Observe，
// 此时回合视为刚结束的活动；若调用点后移，冷标注会误伤下一回合（Task 22 注意）。
// 返回记录与写盘错误（记账失败不阻断会话——调用方决定如何呈现）。
func (m *Meter) Observe(u TurnUsage) (Record, error) {
	now := m.now()
	cold := m.haveActivity && now.Sub(m.lastActivity) >= m.coldAfterIdle

	rec := Record{
		Turn:         m.turn + 1,
		TS:           now.UTC().Format(time.RFC3339),
		Uncached:     u.InputTokens,
		Write:        u.CacheWriteTokens,
		Read:         u.CacheReadTokens,
		Output:       u.OutputTokens,
		Thinking:     u.ThinkingTokens,
		ExpectedCold: cold,
	}
	if denom := u.InputTokens + u.CacheWriteTokens + u.CacheReadTokens; denom > 0 {
		rec.Hit = float64(u.CacheReadTokens) / float64(denom)
	}
	pIn := m.pricing.InputPerMTok / 1e6
	pOut := m.pricing.OutputPerMTok / 1e6
	rec.Cost = float64(u.InputTokens)*pIn +
		float64(u.CacheWriteTokens)*1.25*pIn +
		float64(u.CacheReadTokens)*0.1*pIn +
		float64(u.OutputTokens)*pOut
	rec.Saving = float64(u.CacheReadTokens) * (pIn - 0.1*pIn)

	payload, err := json.Marshal(rec)
	if err == nil {
		err = m.writer.Append(payload)
	}
	// 无论写盘成败，内存累计照常推进（Report 反映已观察事实；JSONL 为审计源）
	m.turn++
	m.records = append(m.records, rec)
	m.sum.Turns++
	m.sum.Uncached += rec.Uncached
	m.sum.Write += rec.Write
	m.sum.Read += rec.Read
	m.sum.Output += rec.Output
	m.sum.Cost += rec.Cost
	m.sum.Saving += rec.Saving
	if cold {
		m.sum.ColdTurns++
	}
	m.MarkActivity(now)
	return rec, err
}

// Report 返回会话累计（内存累计口径——与 JSONL 一致；跨进程聚合用 ReadUsage）。
func (m *Meter) Report() (Report, error) {
	rep := m.sum
	denom := rep.Uncached + rep.Write + rep.Read
	if denom > 0 {
		rep.SessionHit = float64(rep.Read) / float64(denom)
	}
	return rep, nil
}

// Records 返回本会话已观察记录的副本（追加序；Guard.Evaluate 的会话内数据源）。
func (m *Meter) Records() []Record {
	out := make([]Record, len(m.records))
	copy(out, m.records)
	return out
}

// Close 关闭 JSONL 句柄（幂等）。
func (m *Meter) Close() error {
	if m.writer == nil {
		return nil
	}
	err := m.writer.Close()
	m.writer = nil
	return err
}

// ReadUsage 离线读回记账文件（Task 26 mulix cache / Task 29 doctor 聚合用）。
// 文件不存在返回 (nil, nil)。
func ReadUsage(path string) ([]Record, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache: read usage: %w", err)
	}
	var recs []Record
	for _, line := range splitLines(raw) {
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("cache: parse usage %s: %w", path, err)
		}
		recs = append(recs, r)
	}
	return recs, nil
}

// splitLines 按 '\n' 切分并保留末尾无换行的最后一行。
func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			out = append(out, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		out = append(out, raw[start:])
	}
	return out
}

// ReconcileUsage 跨进程对账（Task 26）：重复回合号按文件序取后者（最近
// 观察为准——JSONL 只追加，重写/合并工具可能产生同回合多行），输出按回合号
// 升序。无重复时原样保留（顺序归一）。
func ReconcileUsage(recs []Record) []Record {
	byTurn := map[int64]Record{}
	turns := make([]int64, 0, len(recs))
	for _, r := range recs { // 文件序遍历：后行覆盖同行号
		if _, seen := byTurn[r.Turn]; !seen {
			turns = append(turns, r.Turn)
		}
		byTurn[r.Turn] = r
	}
	slices.Sort(turns)
	out := make([]Record, 0, len(turns))
	for _, t := range turns {
		out = append(out, byTurn[t])
	}
	return out
}

// AggregateRecords 聚合记账记录为会话汇总（口径与 Meter.Report 一致：token
// 各项/cost/saving 直加；session_hit 加权 Σread/(Σuncached+Σwrite+Σread)，
// 非逐回合均值；cold_turns 计数）。逐行 Cost/Saving 是 Observe 时刻按当时
// 定价冻结的值——历史记录保留旧定价（审计语义正确：事后调价不重写历史）。
func AggregateRecords(recs []Record) Report {
	var rep Report
	for _, r := range recs {
		rep.Turns++
		rep.Uncached += r.Uncached
		rep.Write += r.Write
		rep.Read += r.Read
		rep.Output += r.Output
		rep.Cost += r.Cost
		rep.Saving += r.Saving
		if r.ExpectedCold {
			rep.ColdTurns++
		}
	}
	if denom := rep.Uncached + rep.Write + rep.Read; denom > 0 {
		rep.SessionHit = float64(rep.Read) / float64(denom)
	}
	return rep
}

// ReadUsageReport 离线全量汇总（跨进程权威口径）：ReadUsage → Reconcile →
// Aggregate。文件缺失/空 = 零值 Report（会话尚无记账是合法状态）。
// mulix_usage 工具与 doctor 聚合统一走此入口——内存 Meter.Report 是进程内
// 口径，Resume 后不回放历史，跨进程数据以本函数为准。
func ReadUsageReport(root, sessionID string) (Report, error) {
	recs, err := ReadUsage(filepath.Join(root, "usage", sessionID+".jsonl"))
	if err != nil {
		return Report{}, err
	}
	return AggregateRecords(ReconcileUsage(recs)), nil
}
