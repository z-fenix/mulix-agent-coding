// Package subagent 是 kernel 之上的子代理编排器（design 002 §3）：Manager 状态机、
// 深度/并发限额、委派链事件发布与取消/收口级联。
//
// 红线：只可 import mulix/internal/event 与 mulix/internal/kernel。SessionFactory
// 缝隙由 cli 组合根实现（Task 4）——本包不创建 store、不 import control/sessionx
// （防 import 环）。事件发布由注入的 Publish 承担（父会话侧保证先持久化后发布）。
package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"mulix/internal/event"
	"mulix/internal/kernel"
)

// DefaultMaxDepth 与 DefaultMaxConcurrent 是限额未配置（<=0）时的默认值。
const (
	DefaultMaxDepth      = 3
	DefaultMaxConcurrent = 4
)

// SessionHandle 是一次子代理会话的句柄（由 SessionFactory 返回）。
type SessionHandle interface {
	ID() string
	Done() <-chan struct{} // 回合收口（TurnDone 或引擎退出）时关闭
	Err() error            // 收口原因（nil = 正常完成）
	Summary() string       // 最终 assistant 文本（回合收口时可用）
	TotalTokens() int64
	CostUSDCents() int64
	Cancel(ctx context.Context) error
	Close(ctx context.Context) error
}

// SessionFactory 由 cli（组合根）注入：为子代理挂载插件集（store/meter/gate/
// engine provider——引擎只是插件之一）并返回会话句柄。subagent 包不 import
// control（防 import 环）——工厂闭包在 cli 侧实现（Task 4）。
type SessionFactory func(ctx context.Context, task string) (SessionHandle, error)

// Options 是 Manager 的构造参数。
type Options struct {
	Root          string         // sessions 根（与主会话同池）
	MaxDepth      int            // 默认 3
	MaxConcurrent int            // 默认 4
	Factory       SessionFactory // 必填：子会话组装工厂（cli 组合根注入）
	Publish       func(ev any)   // 必填：父会话事件发布（control 注入；红线：先持久化后发布由父侧保证）
	Now           func() time.Time
}

// Status 是子代理委派的生命周期状态。
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
	StatusFailed    Status = "failed"
)

// Info 是 List 返回的子代理快照（值拷贝，调用方可安全持有与修改）。
type Info struct {
	SubSessionID string
	ParentID     string
	Task         string
	Status       Status
	Depth        int
}

// subEntry 是一个子代理的登记项。
type subEntry struct {
	id       string
	handle   SessionHandle
	parentID string
	task     string
	depth    int
	status   Status
	childCtx *kernel.Context // 每 spawn 一个子作用域：OnDispose(handle.Close)
}

// Manager 编排子代理会话生命周期：状态机 + 限额 + 事件发布 + 级联取消。
type Manager struct {
	factory SessionFactory
	publish func(ev any)
	now     func() time.Time
	root    string

	maxDepth      int
	maxConcurrent int

	seq atomic.Int64 // 委派事件共享计数器，从 1 起

	mu    sync.Mutex
	subs  map[string]*subEntry
	order []string // spawn 顺序（List / Close 的确定性遍历）
}

// NewManager 校验必填项并应用默认限额。
func NewManager(opts Options) (*Manager, error) {
	if opts.Root == "" {
		return nil, errors.New("subagent: Options.Root is required")
	}
	if opts.Factory == nil {
		return nil, errors.New("subagent: Options.Factory is required")
	}
	if opts.Publish == nil {
		return nil, errors.New("subagent: Options.Publish is required")
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxConcurrent := opts.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = DefaultMaxConcurrent
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		factory:       opts.Factory,
		publish:       opts.Publish,
		now:           now,
		root:          opts.Root,
		maxDepth:      maxDepth,
		maxConcurrent: maxConcurrent,
		subs:          make(map[string]*subEntry),
	}, nil
}

// Spawn 委派一个子代理：先判深度（depth >= MaxDepth → 错误），再判并发
// （running >= MaxConcurrent → 错误），经 parentID 祖先链判环后调用工厂。
// 成功即登记为 running、发布 DelegationStarted，并启动终态收口 goroutine。
func (m *Manager) Spawn(ctx context.Context, parentID, task string, depth int) (string, error) {
	m.mu.Lock()
	if depth >= m.maxDepth {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: depth limit exceeded: depth %d >= max %d", depth, m.maxDepth)
	}
	running := m.runningLocked()
	if running >= m.maxConcurrent {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: concurrent limit exceeded: %d running >= max %d", running, m.maxConcurrent)
	}
	if err := m.checkCycleLocked(parentID); err != nil {
		m.mu.Unlock()
		return "", err
	}
	m.mu.Unlock()

	handle, err := m.factory(ctx, task)
	if err != nil {
		return "", fmt.Errorf("subagent: session factory: %w", err)
	}
	subID := handle.ID()

	m.mu.Lock()
	if _, dup := m.subs[subID]; dup {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: duplicate sub session id %q", subID)
	}
	// kernel 关系（design §3.0）：每 spawn 一个子 Context，handle.Close 注册进
	// 它——终态收口/取消时 Dispose 级联触发（幂等），先子后父语义由 kernel 保证。
	childCtx := kernel.NewContext(nil)
	childCtx.OnDispose(func() { _ = handle.Close(ctx) })
	m.subs[subID] = &subEntry{
		id:       subID,
		handle:   handle,
		parentID: parentID,
		task:     task,
		depth:    depth,
		status:   StatusRunning,
		childCtx: childCtx,
	}
	m.order = append(m.order, subID)
	m.mu.Unlock()

	m.publish(event.DelegationStartedEvent{
		Seq:          m.nextSeq(),
		SubSessionID: subID,
		Task:         task,
		Depth:        depth,
	})

	go func() {
		<-handle.Done()
		m.reap(subID)
	}()
	return subID, nil
}

// Poll 返回子代理当前状态；对已收口（Done 关闭）的 running 子代理就地完成终态
// 迁移（与后台 goroutine 同一判据，首个终态获胜）。未知 id → 错误。
func (m *Manager) Poll(ctx context.Context, subID string) (Status, error) {
	m.reap(subID)
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.subs[subID]
	if !ok {
		return "", fmt.Errorf("subagent: unknown sub session %q", subID)
	}
	return e.status, nil
}

// Cancel 取消一个运行中的子代理：终态记为 cancelled（首个终态获胜）、调用
// handle.Cancel、发布 DelegationCancelled、Dispose 子 Context（handle.Close 随
// 级联执行）。仅 running 可取消；未知/已终态 → 错误。
func (m *Manager) Cancel(ctx context.Context, subID, reason string) error {
	m.mu.Lock()
	e, ok := m.subs[subID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("subagent: unknown sub session %q", subID)
	}
	if e.status != StatusRunning {
		m.mu.Unlock()
		return fmt.Errorf("subagent: sub session %q is %s, cancel requires running", subID, e.status)
	}
	e.status = StatusCancelled
	m.mu.Unlock()

	// 句柄取消失败不回滚状态：本地终态已定，收口级联照常执行，错误如实上报。
	err := e.handle.Cancel(ctx)
	m.publish(event.DelegationCancelledEvent{
		Seq:          m.nextSeq(),
		SubSessionID: subID,
		Reason:       reason,
	})
	e.childCtx.Dispose()
	return err
}

// List 返回全部子代理快照（spawn 顺序的值拷贝）。
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	infos := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		if e, ok := m.subs[id]; ok {
			infos = append(infos, Info{
				SubSessionID: e.id,
				ParentID:     e.parentID,
				Task:         e.task,
				Status:       e.status,
				Depth:        e.depth,
			})
		}
	}
	return infos
}

// Close 级联取消全部运行中子代理（spawn 逆序遍历——与 kernel 先子后父的收口语义对齐）。
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		if e, ok := m.subs[m.order[i]]; ok && e.status == StatusRunning {
			ids = append(ids, e.id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Cancel(ctx, id, "manager_closed")
	}
}

// reap 把已收口（Done 关闭）的 running 子代理迁移到终态：Err()==nil → completed，
// 否则 failed；发布对应事件并 Dispose 子 Context。幂等——非 running 直接返回，
// 保证首个终态获胜（与 Poll 的就地迁移、Cancel 竞争时同为这一判据）。
func (m *Manager) reap(subID string) {
	m.mu.Lock()
	e, ok := m.subs[subID]
	if !ok || e.status != StatusRunning {
		m.mu.Unlock()
		return
	}
	select {
	case <-e.handle.Done():
	default:
		m.mu.Unlock()
		return
	}
	err := e.handle.Err()
	var ev any
	if err == nil {
		e.status = StatusCompleted
		ev = event.DelegationCompletedEvent{
			Seq:          m.nextSeq(),
			SubSessionID: subID,
			Summary:      e.handle.Summary(),
			TotalTokens:  e.handle.TotalTokens(),
			CostUSDCents: e.handle.CostUSDCents(),
		}
	} else {
		e.status = StatusFailed
		ev = event.DelegationFailedEvent{
			Seq:          m.nextSeq(),
			SubSessionID: subID,
			Reason:       err.Error(),
		}
	}
	m.mu.Unlock()

	m.publish(ev)
	e.childCtx.Dispose()
}

// runningLocked 统计 running 数（调用方持锁）。
func (m *Manager) runningLocked() int {
	n := 0
	for _, id := range m.order {
		if e, ok := m.subs[id]; ok && e.status == StatusRunning {
			n++
		}
	}
	return n
}

// checkCycleLocked 沿 parentID 祖先链回溯判环（registry 内已登记的子代理才参与；
// 链到主会话/未知父即终止）。调用方持锁。
func (m *Manager) checkCycleLocked(parentID string) error {
	e, ok := m.subs[parentID]
	if !ok {
		return nil
	}
	seen := map[string]bool{parentID: true}
	cur := e
	for {
		p, ok := m.subs[cur.parentID]
		if !ok {
			return nil
		}
		if seen[p.id] {
			return fmt.Errorf("subagent: delegation cycle detected at %q", p.id)
		}
		seen[p.id] = true
		cur = p
	}
}

// nextSeq 返回委派事件共享计数器的下一个值（从 1 起）。
func (m *Manager) nextSeq() int64 { return m.seq.Add(1) }
