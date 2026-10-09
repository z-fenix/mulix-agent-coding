package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"mulix/internal/config"
)

// HookEvent 是钩子事件词表（与引擎 system/hook_* 的 hook_event 字段一致：
// SessionStart|PreToolUse|PostToolUse|Stop）。
type HookEvent string

const (
	HookSessionStart HookEvent = "SessionStart"
	HookPreToolUse   HookEvent = "PreToolUse"
	HookPostToolUse  HookEvent = "PostToolUse"
	HookStop         HookEvent = "Stop"
)

// HookPayload 是钩子回调载荷（P1 最小集）。guard 不依赖 engine——
// control 负责从 engine.HookInfo 填充本结构（分层红线）：Stdout/Stderr/
// ExitCode 的合并规则由 control 决定（Output 承载摘要）。
// JSON 形状（M16 自定义钩子的 stdin 契约）：snake_case、空字段在位——
// 与 engine.HookInfo（协议 §2.2）同构命名，脚本作者可依赖键恒存在。
type HookPayload struct {
	Event    HookEvent       `json:"event"`
	HookID   string          `json:"hook_id"`   // 引擎 hook_id
	HookName string          `json:"hook_name"` // 引擎 hook_name
	Tool     string          `json:"tool"`      // PreToolUse/PostToolUse 的工具名（可空）
	Input    json.RawMessage `json:"input"`     // PreToolUse 的工具输入（可空，null 占位）
	Output   string          `json:"output"`    // hook_response 的 output/stdout 摘要
	Outcome  string          `json:"outcome"`   // hook_response 的 outcome
}

// HookFunc 是单个钩子回调。实现应快速返回、不阻塞（P1 内置消费者均为
// 进程内动作：审计日志追加、Meter 记账、越界 notice）。
type HookFunc func(ctx context.Context, p HookPayload)

// Hooks 是钩子分发器：按事件类型顺序分发 Go 回调；单回调 panic 被隔离
// （recover 后继续后续回调），recovered 值经 OnPanic 字段上报（可空）。
// P1 串行调用（control 单线程分发）；OnPanic 与回调注册须在会话开始前
// 完成（分发读快照，运行期重配置是 P2 关注点）。
type Hooks struct {
	// OnPanic 是 panic 上报回调（可空）：ev=事件、handlerIndex=回调注册序
	//（事件内 0 起）、recovered=recover() 值。上报自身 panic 同样被吞掉
	//（隔离不因消费者故障而失效）。
	OnPanic func(ev HookEvent, handlerIndex int, recovered any)

	mu       sync.RWMutex
	handlers map[HookEvent][]HookFunc
}

// NewHooks 构造空分发器。
func NewHooks() *Hooks {
	return &Hooks{handlers: map[HookEvent][]HookFunc{}}
}

// On 注册回调（同一事件多个回调按注册顺序执行）。
func (h *Hooks) On(ev HookEvent, fn HookFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handlers[ev] = append(h.handlers[ev], fn)
}

// Dispatch 顺序分发一次钩子事件：逐回调调用，panic 逐个隔离，后续回调
// 不受影响；无回调时 no-op。
func (h *Hooks) Dispatch(ctx context.Context, p HookPayload) {
	h.mu.RLock()
	fns := make([]HookFunc, len(h.handlers[p.Event]))
	copy(fns, h.handlers[p.Event])
	h.mu.RUnlock()
	for i, fn := range fns {
		h.runOne(ctx, p, i, fn)
	}
}

// runOne 执行单个回调并隔离 panic（上报调用自身 panic 也吞掉）。
func (h *Hooks) runOne(ctx context.Context, p HookPayload, idx int, fn HookFunc) {
	defer func() {
		if r := recover(); r != nil {
			h.reportPanic(p.Event, idx, r)
		}
	}()
	fn(ctx, p)
}

// reportPanic 调用 OnPanic 上报（未设置则丢弃；上报 panic 同样隔离）。
func (h *Hooks) reportPanic(ev HookEvent, idx int, r any) {
	defer func() { _ = recover() }()
	if h.OnPanic != nil {
		h.OnPanic(ev, idx, r)
	}
}

// ---- 用户自定义钩子消费者（M16/FR-111/FR-112；design 002 §7） ----

// defaultHookTimeoutMS 是规则 timeout_ms 缺省（0）时的生效超时——M14 的
// 「缺省 0 = 消费侧解释默认」同款语义（上限 hookMaxTimeoutMS 在构造期收口）。
const defaultHookTimeoutMS = 10000

// hookMaxTimeoutMS 是单条自定义钩子规则的超时上限（30s，design 002 §7）。
const hookMaxTimeoutMS = 30000

// HookConsumer 是带错误返回的钩子消费者契约（HookFunc 的错误返回形态）。
// guard 既有扩展点是 HookFunc(ctx, HookPayload)（control 在四时点分发）；
// 接线层以 `func(ctx, p) { _ = c.Handle(ctx, p) }` 适配——丢弃错误即得 P1
// 既定 fail-open（FR-017：单个消费者异常不得影响回合）。
type HookConsumer interface {
	Handle(ctx context.Context, p HookPayload) error
}

// ScriptConsumer 是用户自定义钩子消费者：把 [[hooks.custom.rules]] 规则按
// 时点分组，Handle 时对匹配规则逐条 exec 脚本（argv 直传不经 shell），
// stdin 传入 payload 的完整事件 JSON。
//
// 失败语义（既定矩阵，design 002 §9 对超时/崩溃同款单行裁定）：规则超时或
// 崩溃 → 该规则降级（效果不生效）+ OnWarning 留痕 + Handle 返回错误；同
// 事件的其余规则照常执行（逐规则隔离）；经 HookFunc 适配层丢弃错误后主
// 流程继续（FR-017）。stdout 是信息面：exit 0 即成功——P1 hook_response 的
// output/stdout 在 control 侧只透传不解释，mulix 消费者没有引擎反馈通道
// （P1 无 hook_callback 应答面），故不做 stdout 决策解码；exit 2 阻断属
// 引擎侧钩子协议，不适用。
//
// 并发：control P1 串行分发（Hooks.Dispatch 单线程），Handle 无锁；OnWarning
// 与构造须在会话开始前完成（与 Hooks.OnPanic 同一约束）。
type ScriptConsumer struct {
	// OnWarning 是降级留痕回调（可空）：规则超时/崩溃时以事件、规则声明序
	//（NewScriptConsumer 入参全量 0 起）与细节文案上报；接线层将其落为
	// NoticeEvent（warning）留痕。上报自身 panic 被吞掉（留痕失效不得放大
	// 为主流程故障——与 Hooks.reportPanic 二级 recover 同向）。
	OnWarning func(ev HookEvent, ruleIndex int, detail string)

	rules map[HookEvent][]scriptRule
}

// scriptRule 是构造期校验/归一后的单条规则（index = 声明序，告警定位用）。
type scriptRule struct {
	decl    config.HookRule
	timeout time.Duration
	index   int
}

// NewScriptConsumer 构造自定义钩子消费者——启动期校验的唯一收口（FR-111
// fail-fast）：event 必须 ∈ P1 四时点、command 非空、0 ≤ timeout_ms ≤ 30000
// （0 = 缺省 defaultHookTimeoutMS）。任何规则非法即报错拒绝启动，错误点名
// 规则序与违规字段；空规则集合法（no-op 消费者）。
func NewScriptConsumer(rules []config.HookRule) (HookConsumer, error) {
	c := &ScriptConsumer{rules: map[HookEvent][]scriptRule{}}
	for i, r := range rules {
		ev := HookEvent(r.Event)
		switch {
		case ev != HookSessionStart && ev != HookPreToolUse &&
			ev != HookPostToolUse && ev != HookStop:
			return nil, fmt.Errorf("guard: hook rule %d: event %q must be one of SessionStart|PreToolUse|PostToolUse|Stop", i, r.Event)
		case len(r.Command) == 0:
			return nil, fmt.Errorf("guard: hook rule %d (%s): command must not be empty", i, r.Event)
		case r.TimeoutMS < 0 || r.TimeoutMS > hookMaxTimeoutMS:
			return nil, fmt.Errorf("guard: hook rule %d (%s): timeout_ms %d must be 0 (default) or in 1..%d", i, r.Event, r.TimeoutMS, hookMaxTimeoutMS)
		}
		timeout := time.Duration(r.TimeoutMS) * time.Millisecond
		if r.TimeoutMS == 0 {
			timeout = defaultHookTimeoutMS * time.Millisecond
		}
		c.rules[ev] = append(c.rules[ev], scriptRule{decl: r, timeout: timeout, index: i})
	}
	return c, nil
}

// Handle 对 payload 时点的全部匹配规则逐条执行脚本（声明序）：单规则失败
// 不中断其余规则（逐规则隔离），返回首个错误；无匹配规则时 no-op。
func (c *ScriptConsumer) Handle(ctx context.Context, p HookPayload) error {
	rs := c.rules[p.Event]
	if len(rs) == 0 {
		return nil
	}
	payloadJSON, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("guard: hook payload marshal: %w", err)
	}
	var firstErr error
	for _, r := range rs {
		if err := c.runRule(ctx, p.Event, r, payloadJSON); err != nil {
			c.warn(p.Event, r.index, err.Error())
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// runRule 执行单条规则：exec.CommandContext（argv 直传——Command[0] 为可
// 执行、其余为逐字参数，os/exec 只做 CreateProcess 级引号重组（Windows），
// 不经任何 shell 解释，评审 Focus：argv 注入）、stdin = 完整事件 JSON、
// context 超时收口。stdout 是信息面，直接落 DevNull（P1 不解释、不做决策
// 解码，且避免失控脚本的输出膨胀内存）；stderr 仅作失败诊断（时长被规则
// 超时封顶）。超时（规则 deadline 触发的 kill）与崩溃（非零退出/启动失败）
// 都返回错误——调用方负责告警留痕与降级。脚本不读 stdin 提前退出属正常
// （os/exec 对 stdin 拷贝的 EPIPE/BROKEN_PIPE 按 exit 码豁免，不误报失败）。
func (c *ScriptConsumer) runRule(ctx context.Context, ev HookEvent, r scriptRule, payloadJSON []byte) error {
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, r.decl.Command[0], r.decl.Command[1:]...)
	cmd.Stdin = bytes.NewReader(payloadJSON)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("guard: hook rule %d (%s): timeout after %s", r.index, ev, r.timeout)
	}
	return fmt.Errorf("guard: hook rule %d (%s): execution failed: %v (stderr=%q)",
		r.index, ev, runErr, stderr.String())
}

// warn 上报降级留痕（OnWarning 未设置则丢弃；上报自身 panic 吞掉）。
func (c *ScriptConsumer) warn(ev HookEvent, ruleIndex int, detail string) {
	defer func() { _ = recover() }()
	if c.OnWarning != nil {
		c.OnWarning(ev, ruleIndex, detail)
	}
}
