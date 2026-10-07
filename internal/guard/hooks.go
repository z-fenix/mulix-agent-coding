package guard

import (
	"context"
	"encoding/json"
	"sync"
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
type HookPayload struct {
	Event    HookEvent
	HookID   string          // 引擎 hook_id
	HookName string          // 引擎 hook_name
	Tool     string          // PreToolUse/PostToolUse 的工具名（可空）
	Input    json.RawMessage // PreToolUse 的工具输入（可空）
	Output   string          // hook_response 的 output/stdout 摘要
	Outcome  string          // hook_response 的 outcome
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
