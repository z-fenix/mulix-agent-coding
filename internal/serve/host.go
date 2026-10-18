// host.go——sessionHost 交互令牌状态机 + hostManager(2026-10-18-web-ui-mvp
// Task 5)。
//
// 交互模型:一个会话同时恰有一个交互端(interactive)持有者,其余连接降级
// readonly(旁观);持有者断开进入宽限(gracePeriod,默认 10s),期内任何
// client 重连 claim 即取消离线计时;宽限到点无人认领 → gone 关闭——经
// Task 4 watchInteractiveGone 驱动既有 bridge.CancelAll fail-closed deny
// 全部待审。红线:无交互端绝不静默放行,不给 internal/control、
// internal/guard 增改一行(治理语义零改动)。
package serve

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"mulix/internal/control"
)

// sessionRole 是 claim 的角色结果。
type sessionRole int

const (
	roleReadonly    sessionRole = iota // 被他人持有:旁观
	roleInteractive                    // 交互端持有者
)

// gracePeriod 是持有者断开后的宽限时长(期内重连取消离线;到点 gone 关闭)。
// 包级 var 是测试缝(guard defaultHookTimeoutMS 同惯例;生产不改写)。
var gracePeriod = 10 * time.Second

// sessionSummary 是会话列表条目(list 返回;Task 7 API 消费)。
type sessionSummary struct {
	ID           string
	Workspace    string
	Status       string // ready | not_ready | failed | running
	LastActivity time.Time
	Open         bool // 本进程内已打开(hostManager 在册)
}

// assembleFn 是控制器装配缝(Task 6 注入真实装配;本任务测试用 FakeHost
// 最小桩):为 host 装配 *Controller 与审批桥。装配闭包负责驱动
// NewSession/Resume(resume 语义经 h.resume 传递)。
type assembleFn func(ctx context.Context, h *sessionHost) (*control.Controller, *control.UserApprovalBridge, error)

// sessionHost 是单会话的宿主状态机:控制器 + webSink + 交互权持有/宽限。
type sessionHost struct {
	ID        string
	Workspace string
	resume    bool
	ctrl      *control.Controller
	sink      *webSink
	bridge    *control.UserApprovalBridge
	// params 是真实装配参数(Task 6 withAssembly 经变参注入;零值 = 桩装配
	// 不读)。定义在 assemble.go——装配关注点与状态机分离。
	params assembleParams
	// assembleRelease 是装配资源收口(Task 6 装配成功后注入:meter/store——
	// close 在 ctrl.Close 之后级联调用,恰一次)。nil = 桩装配(自管清理)。
	assembleRelease func()

	mu           sync.Mutex
	holder       string        // 交互端 client id(""=无)
	gone         chan struct{} // 宽限到点关闭(一次性);watchInteractiveGone 消费
	graceTimer   *time.Timer   // 宽限计时器(nil=未挂)
	graceGen     uint64        // 宽限代次:claim/close 取消即递增,回调比对防误关
	goneFired    bool          // gone 已因宽限到期关闭(一次性闸;host 未终态,防二次 close panic)
	lastActivity time.Time     // claim/release 刷新(idle-close 与列表消费)
	stopWatcher  func()        // watchInteractiveGone stop(幂等)
	unlock       func()        // 跨进程锁 release(Task 7 注入;nil=未持锁)
	closed       bool
	closedCh     chan struct{} // close 完成信号(manager/测试观察面)
}

// newSessionHost 构造 host:建 webSink → assemble 装配(失败即返错,无
// goroutine 泄漏)→ 启动交互端离线 watcher(Task 4 产物;close 时 stop)。
// 跨进程锁刻意不在本函数获取——HTTP 层(Task 7)在构造/claim 前调
// acquireSessionLock,成功后经 attachLock 注入 release,close 时级联释放。
// 变参 options 是装配参数注入缝(Task 6 withAssembly;变参形态保持 Task 5
// 五参调用面逐字节兼容——host_test.go 既有调用零改动)。
func newSessionHost(ctx context.Context, id, workspace string, resume bool, assemble assembleFn, options ...hostOption) (*sessionHost, error) {
	h := &sessionHost{
		ID: id, Workspace: workspace, resume: resume,
		sink:         newWebSink(),
		gone:         make(chan struct{}),
		closedCh:     make(chan struct{}),
		lastActivity: time.Now(),
	}
	for _, opt := range options {
		opt(h)
	}
	ctrl, bridge, err := assemble(ctx, h)
	if err != nil {
		return nil, err
	}
	h.ctrl, h.bridge = ctrl, bridge
	if bridge != nil {
		// 无人值守装配(bridge 空)无审批可取消——gone 仍是有状态的信号面,
		// 只是不接取消器;web UI 会话恒 attended(评审 C1 裁定),此分支
		// 是防御路径。
		h.stopWatcher = watchInteractiveGone(h.gone, bridge)
	}
	return h, nil
}

// claim 请求交互权:空闲(或同 client 重入)→ interactive 并记持有者;
// 被他人持有 → readonly(nil——降级旁观非错误)。宽限期内任何 claim(含
// 不同 client 接管)取消 pending 离线计时——重连即交互端就位,离线取消器
// 不得误伤其待审请求(fail-closed 语义只对「无人」生效)。
//
// gone 已触发后再 claim 的语义(终审 Critical 裁定):照常授予交互权——
// gone 是一次性离线信号,触发即已消费(CancelAll 已跑过),宿主并未终态,
// 交互权与 claim 状态机无关;后续再次 release 到期由 goneFired 幂等兜住,
// 不再二次 close(TestGoneFiredThenReclaimNoDoubleClose 钉死)。
func (h *sessionHost) claim(clientID string) (sessionRole, error) {
	if clientID == "" {
		return roleReadonly, errors.New("serve: empty client id")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return roleReadonly, errors.New("serve: session closed")
	}
	if h.graceTimer != nil { // 宽限期内重连:取消 pending 离线计时
		h.graceTimer.Stop()
		h.graceTimer = nil
		h.graceGen++ // 使任何已 fired 阻塞在锁上的回调失效
	}
	h.lastActivity = time.Now()
	if h.holder == "" || h.holder == clientID {
		h.holder = clientID
		return roleInteractive, nil
	}
	return roleReadonly, nil
}

// release 交还交互权:仅持有者的 release 生效(非持有者 no-op)。持有者
// release → 清持有 + 挂宽限计时器:期内 reclaim 取消(claim 侧);到点
// 无人认领 → close(gone) 恰一次(one-shot,之后通道保持关闭)。
func (h *sessionHost) release(clientID string) {
	if clientID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.holder != clientID {
		return // 非持有者/已关闭:no-op
	}
	h.holder = ""
	h.lastActivity = time.Now()
	d := gracePeriod // mu 内读包级 var:与测试写侧同 goroutine 序,无竞态
	h.graceGen++
	gen := h.graceGen
	h.graceTimer = time.AfterFunc(d, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		// 代次复核:仅当自己仍是当前代才关闭——防「已 fired 阻塞在锁上的
		// 旧回调」在 Stop() 失败后误关(claim/close 先拿锁递增代次的窗口),
		// 也防被后续 release 的新计时器串扰。
		if h.graceGen != gen {
			return
		}
		h.graceTimer = nil
		if h.goneFired {
			return // 一次性:gone 已因更早的宽限到期关闭,宿主未终态——再 close 即 panic(终审 Critical)
		}
		h.goneFired = true
		close(h.gone)
	})
}

// interactiveGone 返回交互端离线信号:持有者断开且宽限内无人重连 → 关闭
// (one-shot)。消费面 = watchInteractiveGone → bridge.CancelAll
// (fail-closed deny 全部待审;Task 4)。
func (h *sessionHost) interactiveGone() <-chan struct{} { return h.gone }

// attachLock 注入跨进程锁 release(Task 7 接线:HTTP 层 acquireSessionLock
// 成功后挂到 host;close 时级联释放)。重复注入以最后一次为准;nil 清除。
func (h *sessionHost) attachLock(release func()) {
	h.mu.Lock()
	h.unlock = release
	h.mu.Unlock()
}

// close 收口(幂等):宽限计时器取消 → 离线 watcher stop → ctrl.Close
// (等泵退出)→ 装配资源级联收口(meter/store——cli sessionHandle.close 同序)
// → 锁释放(仅当持锁)。先停 watcher 再关控制器:close 后 gone 无论是否被
// 宽限到点关闭,都不再触发 CancelAll。
func (h *sessionHost) close(ctx context.Context) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	stopWatcher := h.stopWatcher
	h.stopWatcher = nil
	timer := h.graceTimer
	h.graceTimer = nil
	h.graceGen++ // 使已 fired 阻塞在锁上的宽限回调失效(与 claim 同纪律)
	unlock := h.unlock
	h.unlock = nil
	release := h.assembleRelease
	close(h.closedCh)
	h.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	if stopWatcher != nil {
		stopWatcher()
	}
	err := h.ctrl.Close(ctx)
	if release != nil {
		release()
	}
	if unlock != nil {
		unlock()
	}
	return err
}

// summary 组列表条目:status 自 ctrl.Snapshot() 派生(READY→ready /
// NOT_READY→not_ready / FAILED→failed;回合在飞 → running 覆盖)。
func (h *sessionHost) summary() sessionSummary {
	h.mu.Lock()
	id, ws, open, last := h.ID, h.Workspace, !h.closed, h.lastActivity
	h.mu.Unlock()
	status, running := "not_ready", false
	if h.ctrl != nil { // ctrl 构造后不可变;newSessionHost 成功保证非 nil
		snap := h.ctrl.Snapshot()
		running = snap.Running
		switch snap.Ready {
		case "READY":
			status = "ready"
		case "FAILED":
			status = "failed"
		}
	}
	if running {
		status = "running"
	}
	return sessionSummary{ID: id, Workspace: ws, Status: status, LastActivity: last, Open: open}
}

// idleExpired 判定可自动收口:未关闭、无交互持有者、回合未在飞,且距最近
// 活动超过 idle。
func (h *sessionHost) idleExpired(idle time.Duration) bool {
	h.mu.Lock()
	candidate := !h.closed && h.holder == ""
	h.mu.Unlock()
	if !candidate {
		return false
	}
	if h.ctrl != nil && h.ctrl.Snapshot().Running {
		return false // 回合在飞:绝不自动收口
	}
	h.mu.Lock()
	last := h.lastActivity
	h.mu.Unlock()
	return time.Since(last) > idle
}

// defaultIdleClose 是 idle ≤0 的防御兜底(WebConfig 已 fail-closed 保证
// 正数;手拼 manager 的路径不无保护)。
const defaultIdleClose = 10 * time.Minute

// idleSweepInterval 是 idle-close 扫描节拍(生产 1min;包级 var 测试缝——
// newHostManager 同步读一次,goroutine 不再触碰)。
var idleSweepInterval = time.Minute

// hostManager 是本进程打开会话的注册表:idle-close 扫描 + get/list 快照。
// 未打开的会话不在此列——由 Task 7 与 ScanSessions 启发式合并。
type hostManager struct {
	idle     time.Duration
	mu       sync.Mutex
	hosts    map[string]*sessionHost
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// newHostManager 构造并启动 idle-close 扫描 goroutine(idle=WebConfig.
// IdleCloseMinutes 的时长形态;≤0 → 兜底 10min)。
func newHostManager(idle time.Duration) *hostManager {
	if idle <= 0 {
		idle = defaultIdleClose
	}
	interval := idleSweepInterval
	m := &hostManager{
		idle:  idle,
		hosts: map[string]*sessionHost{},
		stop:  make(chan struct{}), done: make(chan struct{}),
	}
	go m.sweep(interval)
	return m
}

// add 注册 host(Task 7 构造后入册;同 ID 覆盖)。
func (m *hostManager) add(h *sessionHost) {
	m.mu.Lock()
	m.hosts[h.ID] = h
	m.mu.Unlock()
}

// remove 摘除(idle-close 收口后的清理)。
func (m *hostManager) remove(id string) {
	m.mu.Lock()
	delete(m.hosts, id)
	m.mu.Unlock()
}

// get 取在册 host(仅本进程打开的会话)。
func (m *hostManager) get(id string) (*sessionHost, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hosts[id]
	return h, ok
}

// list 返回在册 host 的摘要(ID 序——确定性输出,API 分页/快照稳定)。
func (m *hostManager) list() []sessionSummary {
	m.mu.Lock()
	ids := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	hosts := make([]*sessionHost, 0, len(ids))
	for _, id := range ids {
		hosts = append(hosts, m.hosts[id])
	}
	m.mu.Unlock()
	out := make([]sessionSummary, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, h.summary())
	}
	return out
}

// Close 停扫描 goroutine(幂等;等待退出)。
func (m *hostManager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	<-m.done
}

// closeAll 收口全部在册 host(进程关停路径——Run ctx 取消分支在 Shutdown 后
// 调用):先对持桥 host CancelAll(deny 全部待审——pending 期间 ctrl.Close 等
// 泵退出会拖满上界,与 http_test closeAllHosts 测试收口缝同纪律),再逐 host
// close;单个失败不阻断其余(与 sweep 同容忍度),错误逐条回传供调用方留痕。
// 清空注册表——收口后不再暴露死 host。幂等:已 closed 的 host.close 返 nil,
// 二次调用安全。
func (m *hostManager) closeAll(ctx context.Context) []error {
	m.mu.Lock()
	hosts := make([]*sessionHost, 0, len(m.hosts))
	for _, h := range m.hosts {
		hosts = append(hosts, h)
	}
	m.hosts = map[string]*sessionHost{}
	m.mu.Unlock()
	var errs []error
	for _, h := range hosts {
		if h.bridge != nil {
			h.bridge.CancelAll("server-shutdown")
		}
		if err := h.close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("serve: close session %s: %w", h.ID, err))
		}
	}
	return errs
}

// sweep 是 idle-close 扫描核:每节拍扫一轮,可收口的 host close 并摘除
// (锁/监听/引擎经 host.close 级联释放)。
func (m *hostManager) sweep(interval time.Duration) {
	defer close(m.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.sweepOnce()
		}
	}
}

// sweepOnce 跑一轮扫描(独立成函数:测试可直读,不依赖节拍)。
func (m *hostManager) sweepOnce() {
	m.mu.Lock()
	hosts := make([]*sessionHost, 0, len(m.hosts))
	for _, h := range m.hosts {
		hosts = append(hosts, h)
	}
	m.mu.Unlock()
	for _, h := range hosts {
		if h.idleExpired(m.idle) {
			_ = h.close(context.Background()) // 收口失败亦摘除:注册表不再持有死 host
			m.remove(h.ID)
		}
	}
}
