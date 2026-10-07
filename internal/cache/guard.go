package cache

// GuardOptions 是 NewGuard 参数（零值 = design §8.3 默认）。
type GuardOptions struct {
	Floor      float64 // 告警阈值（config cache.hit_rate_floor，默认 0.70）
	WindowSize int     // 滑动窗口的有效回合数（默认 10）
}

// Guard 是 §8.3 治理层的命中率地板告警（纯评估，无状态；不中断会话——
// breach 只产出结构化 Notice，事件化/TUI 呈现由 control/TUI 接线）。
type Guard struct {
	floor  float64
	window int
}

// Notice 是一次地板告警的结构化产物（镜像事件/TUI 卡片的数据源）。
type Notice struct {
	WindowTurns int     `json:"window_turns"`
	WindowHit   float64 `json:"window_hit"`
	Floor       float64 `json:"floor"`
	FirstTurn   int64   `json:"first_turn"`
	LastTurn    int64   `json:"last_turn"`
}

// NewGuard 构造 Guard：floor <= 0 或 window <= 0 取 §8.3 默认（0.70 / 10）。
// 注意：因此无法经配置表达「floor=0 = 从不告警」；如需禁用告警是 P2 议题
// （设计未定义禁用开关，勿静默改动此语义）。
func NewGuard(opts GuardOptions) Guard {
	g := Guard{floor: 0.70, window: 10}
	if opts.Floor > 0 {
		g.floor = opts.Floor
	}
	if opts.WindowSize > 0 {
		g.window = opts.WindowSize
	}
	return g
}

// Evaluate 评估 Record 流（调用方保证按回合顺序传入；Meter.Records 与
// ReadUsage 均为追加序）。规则（design §8.3）：
//   - expected_cold 回合完全排除（不参与命中率，也不占窗口名额）；
//   - 有效回合不足窗口大小时不评估（预热期 read=0 是正常态，防误报）；
//   - 窗口命中率 = Σread/Σ(uncached+write+read)（加权，与 session 口径一致）；
//   - 窗口总分母为 0（病态空输入）不评估；
//   - 命中率 < floor 才告警（== floor 不告警）。
func (g Guard) Evaluate(recs []Record) (Notice, bool) {
	valid := make([]Record, 0, len(recs))
	for _, r := range recs {
		if !r.ExpectedCold {
			valid = append(valid, r)
		}
	}
	if len(valid) < g.window {
		return Notice{}, false
	}
	window := valid[len(valid)-g.window:]

	var sumRead, sumDenom int64
	for _, r := range window {
		sumRead += r.Read
		sumDenom += r.Uncached + r.Write + r.Read
	}
	if sumDenom == 0 {
		return Notice{}, false
	}
	hit := float64(sumRead) / float64(sumDenom)
	notice := Notice{
		WindowTurns: len(window),
		WindowHit:   hit,
		Floor:       g.floor,
		FirstTurn:   window[0].Turn,
		LastTurn:    window[len(window)-1].Turn,
	}
	return notice, hit < g.floor
}
