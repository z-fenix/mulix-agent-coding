// Package control 是编排层（design §6）：Controller 接收前端命令
// （Submit/Cancel/Approve/NewSession/Resume/Snapshot），驱动引擎，向单一 Sink
// 发布类型化事件；turn 生命周期/取消/审批的唯一所有者。
// 红线：事件双路分发——sessionx 镜像写入成功是 Sink 发布的前置条件
// （先持久化后发布）；会话内回合互斥（ErrTurnRunning）。
package control

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/sessionx"
)

// ErrTurnRunning：同一会话已有活跃回合时 Submit 返回（design §6；多回合排队
// 是 P4 多入口议题）。
var ErrTurnRunning = errors.New("control: turn already running")

// ErrNoSession：会话未建立（NewSession/Resume 未成功）时操作返回。
var ErrNoSession = errors.New("control: no engine session")

// ErrNoPendingApproval：Approve 找不到对应 tool_use id 的待审请求。
var ErrNoPendingApproval = errors.New("control: no pending approval")

// Sink 是前端事件出口（design §6：单一出口；镜像先行由 Controller 保证）。
// Publish 同步调用——慢消费者对控制流形成背压（P1 刻意为之，保证顺序）。
type Sink interface {
	Publish(ev any)
}

// Options 是 NewController 参数。
type Options struct {
	// HostFactory 以组装好的 BootConfig 拉起引擎宿主（真实=cache.Compose+
	// ClaudeHost，由 cli 接线；测试=FakeHost）。
	HostFactory func(boot engine.BootConfig) (engine.Host, error)
	Sink        Sink            // 前端事件出口
	Store       *sessionx.Store // 权威镜像（Task 9）
	Meter       *cache.Meter    // 记账器（Task 13）
	Gate        *guard.Gate     // 审批门（Task 20；T23 起在控制请求路由消费）
	Config      config.Config   // 生效配置（模型/预设/命中率地板）
	Workspace   string          // 工作区（Boot 快照 C1）
	Unattended  bool            // 无人值守（镜像/事件标注）
}

// Controller 是一次引擎会话的编排器（单会话；多会话=多实例）。
// 并发模型：事件泵单 goroutine 消费引擎事件流并独占 turn 生命周期状态的
// 收口（running 复位/通道关闭只在泵侧）；Submit 只获取与等待。
type Controller struct {
	opts Options

	mu         sync.Mutex
	host       engine.Host
	engineInfo engine.EngineInfo // 首个 init 推导（幂等，以首条为准）
	turn       int64             // 当前回合序号（Submit 递增）
	running    bool
	closed     bool
	seq        int64
	exitLogged bool // 已落 EngineExited（防通道关闭后的合成事件重复）
	engineDead bool // 引擎已退出（后续 Submit 拒绝为 ErrNoSession）
	lastReport cache.Report
	sessionID  string // mulix 会话 id（构造期读一次 meta，避免快照路径磁盘 I/O）

	turnDones       map[int64]chan struct{}               // 回合收口通道（泵唯一关闭方）
	pendingApproves map[string]chan guard.ApprovalVerdict // tool_use_id → 裁决通道（T23 路由登记）

	pumpDone chan struct{} // 泵退出信号（Close 等待）
	initSeen chan struct{} // 首个 init 消费完成（NewSession 等待）
	initOnce sync.Once
	now      func() time.Time // 时钟注入（cache.json updated_at；测试冻结）
}

// NewController 校验参数并构造（fail-closed：缺组件拒绝构造）。
func NewController(opts Options) (*Controller, error) {
	if opts.HostFactory == nil || opts.Sink == nil || opts.Store == nil ||
		opts.Meter == nil || opts.Gate == nil {
		return nil, fmt.Errorf("control: options require hostfactory, sink, store, meter, gate")
	}
	sessionID := ""
	if meta, err := opts.Store.ReadMeta(); err == nil {
		sessionID, _ = meta["id"].(string)
	}
	return &Controller{
		opts:            opts,
		sessionID:       sessionID,
		turnDones:       map[int64]chan struct{}{},
		pendingApproves: map[string]chan guard.ApprovalVerdict{},
		pumpDone:        make(chan struct{}),
		initSeen:        make(chan struct{}),
		now:             time.Now,
	}, nil
}

// NewSession 拉起引擎（Boot 组装 → HostFactory → Start → 事件泵 → 等首个
// init）。BootConfig 组装在 control（design §7.2）：cache.Compose 产出 C1/C2，
// 权限模式按预设映射，setting-sources 显式含 user（实测红线）。
func (c *Controller) NewSession(ctx context.Context) error {
	return c.start(ctx, engine.BootConfig{})
}

// Resume 以既有引擎 session id 续链（engine.lock 校验与恢复链事件入镜像是
// Task 24 的交付；本任务提供最小续链：resume 参数进 Boot）。
func (c *Controller) Resume(ctx context.Context, engineSessionID string) error {
	boot := engine.BootConfig{}
	boot.ResumeEngineSessionID = engineSessionID
	return c.start(ctx, boot)
}

func (c *Controller) start(ctx context.Context, boot engine.BootConfig) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("control: controller closed")
	}
	if c.host != nil {
		c.mu.Unlock()
		return errors.New("control: session already started")
	}
	c.mu.Unlock()

	artifact, err := c.composeBoot(boot)
	if err != nil {
		return fmt.Errorf("control: compose boot: %w", err)
	}
	host, err := c.opts.HostFactory(artifact)
	if err != nil {
		return fmt.Errorf("control: host factory: %w", err)
	}
	if err := host.Start(ctx, artifact); err != nil {
		return fmt.Errorf("control: engine start: %w", err)
	}

	c.mu.Lock()
	c.host = host
	c.mu.Unlock()
	go c.pump(host)

	select {
	case <-c.initSeen: // 首个 init 已消费（TurnStarted 的 engine id 可靠）
		return nil
	case <-c.pumpDone: // 引擎在 init 前就退出——快速失败，不等 ctx 超时
		return errors.New("control: engine exited before init")
	case <-ctx.Done():
		return fmt.Errorf("control: waiting init: %w", ctx.Err())
	}
}

// composeBoot 组装引擎拉起参数（P1 固定集；registry 缺省 = 空清单——
// T23 注册 mulix 权限工具后由 cli 注入真实 registry）。
func (c *Controller) composeBoot(base engine.BootConfig) (engine.BootConfig, error) {
	artifact, err := cache.Compose(c.opts.Config, c.opts.Workspace, emptyRegistry{})
	if err != nil {
		return engine.BootConfig{}, err
	}
	base.AppendSystemPrompt = artifact.AppendSystemPrompt
	base.MCPConfigJSON = artifact.MCPConfigJSON
	base.AllowedTools = artifact.AllowedTools
	base.DisallowedTools = artifact.DisallowedTools
	base.PermissionMode = artifact.PermissionMode
	base.SettingSources = []string{"user"} // 实测红线：必须显式含 user
	base.Workspace = c.opts.Workspace
	base.Model = c.opts.Config.DefaultModel
	return base, nil
}

// emptyRegistry 是 P1 的空注册面（T23/T25 接入 mulix 内建 MCP server）。
type emptyRegistry struct{}

func (emptyRegistry) MCPServers() []cache.MCPServer { return nil }

// Submit 提交一回合用户输入。会话内回合互斥：已有活跃回合返回 ErrTurnRunning。
// 阻塞至回合收口（TurnDone 经镜像+Sink 发布）或 ctx 取消（引擎侧取消是
// Cancel 的职责；ctx 取消只放弃等待）。
func (c *Controller) Submit(ctx context.Context, text string) error {
	return c.submit(ctx, engine.UserMessage{Text: text})
}

func (c *Controller) submit(ctx context.Context, msg engine.UserMessage) error {
	c.mu.Lock()
	if c.closed || c.host == nil || c.engineDead {
		// 引擎退出后拒绝新回合（fail-closed）：镜像以 EngineExited 收尾，
		// 不再追加孤立的 turn_started。
		c.mu.Unlock()
		return ErrNoSession
	}
	if c.running {
		c.mu.Unlock()
		return ErrTurnRunning
	}
	c.running = true
	c.turn++
	turn := c.turn
	host := c.host
	c.mu.Unlock()

	c.publish(event.TurnStartedEvent{
		Seq:             c.nextSeq(),
		SessionID:       c.sessionID,
		EngineSessionID: c.EngineSessionID(),
		PinnedModel:     c.opts.Config.DefaultModel,
		Unattended:      c.opts.Unattended,
	})
	if err := host.Send(ctx, msg); err != nil {
		// Send 失败：泵不会有 result——本侧收口（无等待方，通道无需关闭）。
		// 注意：本回合序号已消费（TurnStarted/send_failed 已入镜像），
		// Meter 的 Record.Turn 由此与 control 回合号发散（Task 26 对账注意）。
		c.publish(event.TurnDoneEvent{
			Seq: c.nextSeq(), Turn: turn, Subtype: "send_failed", IsError: true,
			ResultText: err.Error(),
		})
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		return fmt.Errorf("control: send: %w", err)
	}

	// 等泵处理到 result（收口通道由泵在 TurnDone 发布后关闭——唯一关闭方）。
	select {
	case <-c.turnDoneCh(turn):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cancel 取消当前回合（控制协议 interrupt；引擎已发起的副作用按引擎语义
// 处理，镜像如实记录——design §6）。
func (c *Controller) Cancel(ctx context.Context) error {
	c.mu.Lock()
	host := c.host
	c.mu.Unlock()
	if host == nil {
		return ErrNoSession
	}
	return host.Interrupt(ctx)
}

// Approve 对待审请求交付人工裁决（T23 的 can_use_tool 路由登记 pending；
// 本方法在 T22 即为 API 面——无 pending 时 ErrNoPendingApproval）。
func (c *Controller) Approve(toolUseID string, allow bool, resolvedBy string) error {
	c.mu.Lock()
	ch, ok := c.pendingApproves[toolUseID]
	c.mu.Unlock()
	if !ok {
		return ErrNoPendingApproval
	}
	ch <- guard.ApprovalVerdict{Allow: allow, ResolvedBy: resolvedBy}
	return nil
}

// Mirror 返回权威镜像（TUI/测试读取事件流与投影；写入仍归 control）。
func (c *Controller) Mirror() *sessionx.Store { return c.opts.Store }

// Meter 返回记账器。**并发红线**：Meter 单写者（泵），跨 goroutine 只读
// Report 会与 Observe 竞态——外部读取一律走 Snapshot()（泵侧更新的
// lastReport 快照），不得直接调 Meter.Report()。
func (c *Controller) Meter() *cache.Meter { return c.opts.Meter }

// Snapshot 是状态栏快照（TUI 数据源）。
type Snapshot struct {
	SessionID       string       // mulix 会话 id
	EngineSessionID string       // 引擎 session id（首个 init）
	Model           string       // 引擎侧模型名
	CLIVersion      string       // 引擎 CLI 版本
	Turn            int64        // 回合序号
	Running         bool         // 是否有活跃回合
	MeterReport     cache.Report // 记账汇总（泵侧快照，不触碰 Meter 内部）
	Dir             string       // 会话镜像目录
}

// Snapshot 返回当前状态快照（锁内纯内存读取，无磁盘 I/O、无 Meter 访问）。
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		SessionID:       c.sessionID,
		EngineSessionID: c.engineInfo.SessionID,
		Model:           c.engineInfo.Model,
		CLIVersion:      c.engineInfo.CLIVersion,
		Turn:            c.turn,
		Running:         c.running,
		MeterReport:     c.lastReport,
		Dir:             c.opts.Store.Dir(),
	}
}

// Close 有界关闭引擎并等待事件泵退出（幂等）。
func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	host := c.host
	c.mu.Unlock()
	if host != nil {
		if err := host.Close(ctx); err != nil {
			return err
		}
	}
	select {
	case <-c.pumpDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// ---- 事件泵（双路分发：镜像先行，Sink 后发） ----

// pump 是唯一的事件消费者：engine.Event → event.* 类型化事件 → 镜像 → Sink。
// KindInit 幂等消费（首条为准，重发忽略）；KindResult 记账 + TurnDone + 放行
// 等待方；KindTrace→Notice；KindExit→EngineExited（活跃回合先 error 收口）。
func (c *Controller) pump(host engine.Host) {
	defer close(c.pumpDone)
	for ev := range host.Events() {
		c.dispatch(host, ev)
	}
	// 引擎事件流关闭（Close/崩溃）且未见显式 exit：活跃回合 error 收口 +
	// 终止留痕（合成 reason=engine_closed；显式 KindExit 已在 onExit 处理）。
	// 收口顺序（Critical-1 修复）：关闭性发布全部完成 → running=false →
	// close(ch)——running=false 前新 Submit 拿不到互斥，镜像绝不并发写。
	c.mu.Lock()
	wasRunning := c.running
	alreadyLogged := c.exitLogged
	c.exitLogged = true
	turn := c.turn
	var ch chan struct{}
	if wasRunning {
		ch = c.turnDoneChLocked(turn)
	}
	c.mu.Unlock()
	if ch != nil {
		c.publish(event.TurnDoneEvent{
			Seq: c.nextSeq(), Turn: turn, Subtype: "engine_closed", IsError: true,
		})
	}
	if !alreadyLogged {
		c.publish(event.EngineExitedEvent{Seq: c.nextSeq(), Reason: "engine_closed"})
	}
	c.mu.Lock()
	c.engineDead = true
	c.running = false
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// dispatch 单事件路由（泵 goroutine 内串行执行）。
func (c *Controller) dispatch(host engine.Host, ev engine.Event) {
	switch ev.Kind {
	case engine.KindInit:
		c.onInit(host, ev)
	case engine.KindAssistant:
		c.onAssistant(ev)
	case engine.KindToolResult:
		c.onToolResult(ev)
	case engine.KindResult:
		c.onResult(ev)
	case engine.KindTrace:
		if ev.Trace != nil {
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "info",
				Content: ev.Trace.Message, IsMeta: true})
		}
	case engine.KindPermissionDenied:
		if ev.Denied != nil {
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
				Content: fmt.Sprintf("permission denied: %s: %s", ev.Denied.ToolName, ev.Denied.Message)})
		}
	case engine.KindExit:
		c.onExit(ev)
	default:
		// KindNoise/KindHookStarted/KindHookResponse/KindControlRequest：
		// T23 接钩子与控制请求路由；其余 informational 容忍忽略。
	}
}

// onInit 幂等消费 init：首条推导引擎身份并回填镜像 meta；重发同 id 忽略、
// 异 id 告警（以引擎为上下文权威的完整语义在 Task 24 Resume 链路）。
func (c *Controller) onInit(host engine.Host, ev engine.Event) {
	c.mu.Lock()
	first := c.engineInfo.SessionID == ""
	if first && ev.Init != nil {
		c.engineInfo = engine.EngineInfo{
			CLIVersion:  ev.Init.CLIVersion,
			SessionID:   ev.SessionID,
			Model:       ev.Init.Model,
			Fingerprint: host.Info().Fingerprint, // 指纹由宿主自述（§7.1）
		}
	}
	sameID := c.engineInfo.SessionID == ev.SessionID
	c.mu.Unlock()

	if first {
		if err := c.opts.Store.SetEngineID(ev.SessionID); err != nil {
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
				Content: "镜像 meta 回填失败: " + err.Error()})
		}
		c.initOnce.Do(func() { close(c.initSeen) })
		return
	}
	if !sameID {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
			Content: fmt.Sprintf("引擎 session id 变化: %s → %s", c.EngineSessionID(), ev.SessionID)})
	}
}

// onAssistant 拆分 assistant 内容块：text/thinking/tool_use → 类型化事件。
func (c *Controller) onAssistant(ev engine.Event) {
	if ev.Assistant == nil {
		return
	}
	turn := c.currentTurn()
	for _, b := range ev.Assistant.Blocks {
		switch b.Type {
		case "text":
			c.publish(event.AssistantTextEvent{Seq: c.nextSeq(), Turn: turn, Text: b.Text})
		case "thinking":
			// 分片级 token 计数协议不提供（聚合在 result.usage 细目）——P1 置 0。
			c.publish(event.AssistantThinkingEvent{Seq: c.nextSeq(), Turn: turn,
				Thinking: b.Thinking, EstimatedTokens: 0})
		case "tool_use":
			c.publish(event.ToolUseRequestedEvent{Seq: c.nextSeq(), Turn: turn,
				ToolUseID: b.ID, Tool: b.Name, Input: b.Input})
		}
	}
}

// onToolResult 拆分工具结果块。
func (c *Controller) onToolResult(ev engine.Event) {
	if ev.ToolResult == nil {
		return
	}
	turn := c.currentTurn()
	for _, b := range ev.ToolResult.Blocks {
		c.publish(event.ToolResultEvent{Seq: c.nextSeq(), Turn: turn,
			ToolUseID: b.ToolUseID, Content: b.Content, IsError: b.IsError})
	}
}

// onResult 回合收口：Meter 记账 → §8.3 治理评估 → Usage/TurnDone 发布 →
// cache.json 投影 → 放行 Submit。
// 并发契约（review Critical-1）：running=false 必须在全部关闭性发布完成之后、
// close(ch) 之前设置——running 挂着时新 Submit 拿不到互斥，保证镜像单写者
// 与跨回合事件顺序。重复/迟到 result（!running）忽略并 Notice（review
// Important-1：无守卫会 close 外人通道 → 泵 panic）。
func (c *Controller) onResult(ev engine.Event) {
	if ev.Result == nil {
		return
	}
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
			Content: "回合已收口后收到重复 result，忽略"})
		return
	}
	turn := c.turn
	ch := c.turnDoneChLocked(turn)
	c.mu.Unlock()

	r := ev.Result
	rec, obErr := c.opts.Meter.Observe(cache.TurnUsage{
		InputTokens:      r.Usage.InputTokens,
		CacheWriteTokens: r.Usage.CacheWriteTokens,
		CacheReadTokens:  r.Usage.CacheReadTokens,
		OutputTokens:     r.Usage.OutputTokens,
		ThinkingTokens:   r.Usage.Details.ThinkingTokens,
	})
	c.mu.Lock()
	c.lastReport, _ = c.opts.Meter.Report() // 泵是 Meter 唯一写者，快照供 Snapshot
	c.mu.Unlock()
	c.publish(event.UsageEvent{
		Seq: c.nextSeq(), Turn: turn,
		UncachedInputTokens: rec.Uncached, WriteTokens: rec.Write, ReadTokens: rec.Read,
		OutputTokens: rec.Output, ThinkingTokens: rec.Thinking,
		CostUSD: rec.Cost, HitRate: rec.Hit,
	})
	if obErr != nil {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: "记账写盘失败（会话继续）: " + obErr.Error()})
	}
	c.evaluateCacheGuard()
	c.writeCacheProjection()
	c.publish(event.TurnDoneEvent{
		Seq: c.nextSeq(), Turn: turn, Subtype: r.SubType, IsError: r.IsError,
		DurationMS: r.DurationMS, ResultText: r.ResultText,
	})
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
	close(ch)
}

// evaluateCacheGuard 在每次记账后评估 §8.3 命中率地板（告警 → Notice 镜像 +
// 投影标记；不中断会话）。
func (c *Controller) evaluateCacheGuard() {
	g := cache.NewGuard(cache.GuardOptions{Floor: c.opts.Config.Cache.HitRateFloor})
	notice, breach := g.Evaluate(c.opts.Meter.Records())
	if !breach {
		return
	}
	c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
		Content: fmt.Sprintf("缓存命中率 %.1f%% 低于地板 %.1f%%（近 %d 回合，t%d–t%d）",
			notice.WindowHit*100, notice.Floor*100, notice.WindowTurns, notice.FirstTurn, notice.LastTurn)})
}

// writeCacheProjection 把 Meter 汇总 + §8.3 标记写入 cache.json
// （design §10：索引 sidecar 由 control 写）。
func (c *Controller) writeCacheProjection() {
	rep, _ := c.opts.Meter.Report()
	g := cache.NewGuard(cache.GuardOptions{Floor: c.opts.Config.Cache.HitRateFloor})
	gn, breach := g.Evaluate(c.opts.Meter.Records())
	var breachAny any = nil
	if breach {
		breachAny = map[string]any{
			"window_turns": gn.WindowTurns, "window_hit": gn.WindowHit,
			"floor": gn.Floor, "first_turn": gn.FirstTurn, "last_turn": gn.LastTurn,
		}
	}
	proj := map[string]any{
		"turns": rep.Turns, "uncached": rep.Uncached, "write": rep.Write,
		"read": rep.Read, "output": rep.Output, "session_hit": rep.SessionHit,
		"cost": rep.Cost, "saving": rep.Saving, "cold_turns": rep.ColdTurns,
		"hit_rate_floor":  c.opts.Config.Cache.HitRateFloor,
		"hit_rate_breach": breachAny,
		"updated_at":      c.now().UTC().Format(time.RFC3339),
	}
	if err := c.opts.Store.WriteCache(proj); err != nil {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: "cache.json 投影写入失败: " + err.Error()})
	}
}

// onExit 引擎进程退出：活跃回合先 error 收口，再落 EngineExited（恰一次）。
// 收口顺序同 onResult（review Critical-1）：关闭性发布全部完成 →
// running=false/engineDead → close(ch)。
func (c *Controller) onExit(ev engine.Event) {
	c.mu.Lock()
	turn := c.turn
	var ch chan struct{}
	if c.running {
		ch = c.turnDoneChLocked(turn)
	}
	already := c.exitLogged
	c.exitLogged = true
	c.mu.Unlock()
	if ch != nil {
		c.publish(event.TurnDoneEvent{
			Seq: c.nextSeq(), Turn: turn, Subtype: "engine_exited", IsError: true,
		})
	}
	if already {
		c.mu.Lock()
		c.engineDead = true
		c.running = false
		c.mu.Unlock()
		if ch != nil {
			close(ch)
		}
		return
	}
	code, reason := 0, "unknown"
	if ev.Exit != nil {
		code, reason = ev.Exit.Code, ev.Exit.Reason
	}
	c.publish(event.EngineExitedEvent{Seq: c.nextSeq(), Code: code, Reason: reason})
	c.mu.Lock()
	c.engineDead = true
	c.running = false
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// publish 双路分发：镜像先行（红线：Append 成功才发布），失败降级为 Notice
// （镜像坏尾时泵不得崩溃、前端仍知悉）。
func (c *Controller) publish(ev any) {
	if err := c.opts.Store.Append(ev); err != nil {
		c.opts.Sink.Publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: "镜像写入失败: " + err.Error()})
		return
	}
	c.opts.Sink.Publish(ev)
}

func (c *Controller) nextSeq() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return c.seq
}

func (c *Controller) currentTurn() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turn
}

// EngineSessionID 返回引擎 session id（init 前为空）。
func (c *Controller) EngineSessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.engineInfo.SessionID
}

// turnDoneCh 返回指定回合的收口通道（不存在则登记；只增不删——回合数有界，
// 通道由泵唯一关闭）。
func (c *Controller) turnDoneCh(turn int64) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnDoneChLocked(turn)
}

func (c *Controller) turnDoneChLocked(turn int64) chan struct{} {
	ch, ok := c.turnDones[turn]
	if !ok {
		ch = make(chan struct{})
		c.turnDones[turn] = ch
	}
	return ch
}
