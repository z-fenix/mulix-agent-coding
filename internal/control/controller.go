// Package control 是编排层（design §6）：Controller 接收前端命令
// （Submit/Cancel/Approve/NewSession/Resume/Snapshot），驱动引擎，向单一 Sink
// 发布类型化事件；turn 生命周期/取消/审批的唯一所有者。
// 红线：事件双路分发——sessionx 镜像写入成功是 Sink 发布的前置条件
// （先持久化后发布）；会话内回合互斥（ErrTurnRunning）。
package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/engine"
	"mulix/internal/event"
	"mulix/internal/guard"
	"mulix/internal/memory"
	"mulix/internal/sessionx"
	"mulix/internal/subagent"
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
// **重入红线**：Publish 实现内不得调用会触发 publish 的 Controller 方法
// （Submit/Resume/Approve 等）——publish 的 pubMu 不可重入，同步 Sink 回调
// 再入将死锁。
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

	// Approvals 是人工裁决桥（可空=无人值守：Ask 一律 fail-closed 拒绝）。
	// attended 接线：bridge := NewUserApprovalBridge() → Gate 以 bridge 为
	// Approver 构造 → 此处传入同一 bridge。
	Approvals *UserApprovalBridge
	// Hooks 是钩子分发器（可空=不分发）。注册回调须在 NewSession 前
	//（P1 串行；运行期重配置是 P2 关注点——guard.Hooks 同注）。
	Hooks *guard.Hooks
	// Registry 是内置 MCP 工具注册面（cli 注入 *registry.Server；nil = 空面，
	// 既有测试兼容）。非空时 Boot 的 --mcp-config 携带 mulix stdio 条目，且
	// composeBoot 同步钉 --permission-prompt-tool（权限回路最后一环）。
	Registry cache.Registry
	// AllowEngineDrift 是 engine.lock 版本漂移的显式放行（FR-016；cli 的
	// --allow-engine-drift / 用户配置。放行时引擎留痕 engine_drift trace）。
	AllowEngineDrift bool

	// SubagentFactory 是子代理会话工厂（cli 组合根注入，M12/Task 4；nil =
	// 子代理整体禁用：不组装 Manager、不起回调桥、不注入
	// MULIX_SUBAGENT_CALLBACK——Boot 面与 P1 逐字节一致）。
	SubagentFactory subagent.SessionFactory
	// SubagentLimits 是子代理编排限额（零值字段由 subagent.NewManager 取
	// 默认 3/4；design 002 §3）。
	SubagentLimits SubagentLimits

	// Memory 是记忆召回源（M13/T13.2；nil = [tools] memory_search=false 的
	// cli 接线结果——零检索、零注入、零 Boot 面变化，行为与 P1 一致）。
	// cli 组装根注入 *memory.Store（进程内每会话树恰一实例；Task 5 移交：
	// <workspace>/.mulix/memory 的拼装在组合根）。
	Memory MemoryRecall

	// HookConsumers 是追加的自定义钩子消费者（M16/FR-111；cli 组合根注入
	// guard.NewScriptConsumer 产物；空 = 无自定义钩子——不构造分发器，行为
	// 与本字段引入前逐字节一致）。非空时构造期在四时点各挂适配回调
	//（HookFunc 内调 Handle，注册期冻结；Handle 对非匹配事件 no-op——全时点
	// 注册即声明面语义），Handle 错误（规则超时/崩溃降级）落为 NoticeEvent
	//（warning、IsMeta，镜像先行红线）——适配层是唯一告警出口：HookFunc 无
	// 错误返回面，control 持有唯一合规发布通道（不造第二条事件总线）。
	HookConsumers []guard.HookConsumer
}

// MemoryRecall 是记忆召回的检索缝（M13/T13.2）：Submit turn-tail 注入的唯一
// 数据源。窄接口——*memory.Store 天然满足，测试以 stub 钉注入参数；检索的
// 预算/条数策略由 control 侧消费配置（[memory] recall_top_k/recall_budget_chars）。
type MemoryRecall interface {
	Search(query string, k int, budgetChars int) []memory.Entry
}

// 记忆召回的默认参数（design 002 §4；config 包的分层默认值是权威，此处兜底
// 手拼 Config 的零值——withMemoryRecall 消费端防御）。
const (
	defaultRecallTopK        = 3
	defaultRecallBudgetChars = 2000
)

// SubagentLimits 是子代理编排限额（深度/并发；design 002 §3：默认 3/4）。
type SubagentLimits struct {
	MaxDepth      int
	MaxConcurrent int
}

// Controller 是一次引擎会话的编排器（单会话；多会话=多实例）。
// 并发模型：事件泵单 goroutine 消费引擎事件流并独占 turn 生命周期状态的
// 收口（running 复位/通道关闭只在泵侧）；Submit 只获取与等待。
type Controller struct {
	opts Options

	mu             sync.Mutex
	host           engine.Host
	engineInfo     engine.EngineInfo // 首个 init 推导（幂等，以首条为准）
	turn           int64             // 当前回合序号（Submit 递增）
	running        bool
	closed         bool
	seq            int64
	exitLogged     bool // 已落 EngineExited（防通道关闭后的合成事件重复）
	engineDead     bool // 引擎已退出（后续 Submit 拒绝为 ErrNoSession）
	lastReport     cache.Report
	lastTurnTokens cache.TurnUsage
	sessionID      string // mulix 会话 id（构造期读一次 meta，避免快照路径磁盘 I/O）

	turnDones map[int64]chan struct{}    // 回合收口通道（泵唯一关闭方）
	resolved  map[string]gateRuling      // 已裁决权限请求（引擎 tool_use_id → 裁决；重发信封直接应答，人只裁决一次）
	inflight  map[string]chan gateRuling // 裁决单飞（终评审 Important-3：并发传输同 id 只评估一次）
	approveMu sync.Mutex                 // SetAlias+Evaluate 原子化（别名配对不被并发传输交错；P1 审批串行语义）
	pubMu     sync.Mutex                 // 发布串行化（镜像 Append 互斥；见 publish 注）

	permCallback *permissionCallbackServer // 权限回路跨进程桥（真实引擎 --permission-prompt-tool 传输）
	subCallback  *subagentCallbackServer   // 子代理操作跨进程桥（M12：serve 子进程工具 → Manager）
	subMgr       *subagent.Manager         // 子代理编排器（Factory 非空且注册面含 mulix 条目时组装；Task 4 Subagent() 消费）

	// 指令文件快照与 watcher（M15/FR-109 前半）：start 成功序列尾装载/启动；
	// Close 先置 closed 再停 watcher（与回调桥同一收口纪律）。快照只供
	// watcher 与 mulix instructions 消费——绝不改 Boot 面（SC-003）。
	instructionSnaps map[string]instructionSnap
	instrWatch       *InstructionWatcher

	pumpDone chan struct{} // 泵退出信号（Close 等待）
	initSeen chan struct{} // 首个 init 消费完成（NewSession 等待）
	initOnce sync.Once
	// resume 延迟留痕（终评协议适配）：真实引擎 init 在首条消息后到达——
	// start() 的 initSeen 等待带宽限放行，resume 链日志改由 onInit 首次
	// 消费时补发（resumeLogged 防重）。
	resumeRequested string
	resumeMirrorID  string
	resumeMetaErr   error
	resumeLogged    bool
	now             func() time.Time // 时钟注入（cache.json updated_at；测试冻结）
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
	c := &Controller{
		opts:      opts,
		sessionID: sessionID,
		turnDones: map[int64]chan struct{}{},
		resolved:  map[string]gateRuling{},
		inflight:  map[string]chan gateRuling{},
		pumpDone:  make(chan struct{}),
		initSeen:  make(chan struct{}),
		now:       time.Now,
	}
	mountHookConsumers(c)
	return c, nil
}

// hookConsumerEvents 是自定义钩子消费者的挂载时点全表（P1 四时点；consumer
// 的 Handle 对无规则事件 no-op——全时点注册即声明面语义，与分发器「按事件
// 顺序分发」的快照读一致）。
var hookConsumerEvents = []guard.HookEvent{
	guard.HookSessionStart, guard.HookPreToolUse, guard.HookPostToolUse, guard.HookStop,
}

// mountHookConsumers 把 Options.HookConsumers 挂进钩子分发器（M16/FR-111，
// 构造期一次性）：无分发器则新建；每消费者 × 每时点各挂一个适配回调
// （HookFunc 内调 Handle——错误丢弃即 P1 既定 fail-open，FR-017），降级错误
// 落为 NoticeEvent（warning、IsMeta）走 publish（镜像先行红线）。注册全部
// 发生在 NewSession 之前（P1 串行分发、注册期冻结）。
func mountHookConsumers(c *Controller) {
	if len(c.opts.HookConsumers) == 0 {
		return
	}
	if c.opts.Hooks == nil {
		c.opts.Hooks = guard.NewHooks()
	}
	for _, hc := range c.opts.HookConsumers {
		for _, ev := range hookConsumerEvents {
			hc, ev := hc, ev
			c.opts.Hooks.On(ev, func(ctx context.Context, p guard.HookPayload) {
				if err := hc.Handle(ctx, p); err != nil {
					c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
						Content: fmt.Sprintf("自定义钩子降级（%s）: %v", p.Event, err)})
				}
			})
		}
	}
}

// NewSession 拉起引擎（Boot 组装 → HostFactory → Start → 事件泵 → 等首个
// init）。BootConfig 组装在 control（design §7.2）：cache.Compose 产出 C1/C2，
// 权限模式按预设映射，setting-sources 显式含 user（实测红线）。
func (c *Controller) NewSession(ctx context.Context) error {
	return c.start(ctx, engine.BootConfig{})
}

// Resume 以既有引擎 session id 续链（engine.lock 校验与恢复链事件入镜像是
// Task 24 的交付；本任务提供最小续链：resume 参数进 Boot）。
// 孤儿回合收口（T27 评审 Important-3）：上次运行中断留下的 TurnStarted 无
// TurnDone（1:1 不变量破坏）——resume 即视为放弃该回合，镜像合成收口
// （subtype=resume_reconciled，不发布：泵未启动，仅恢复不变量）；否则恢复门
// （RecoveryRequired）在本次会话收口后将永远拒绝该工作区的新会话。
func (c *Controller) Resume(ctx context.Context, engineSessionID string) error {
	if required, openTurn, err := RecoveryRequired(c.opts.Store); err == nil && required {
		if appendErr := c.opts.Store.Append(event.TurnDoneEvent{
			Seq: c.nextSeq(), Turn: openTurn, Subtype: "resume_reconciled",
		}); appendErr != nil {
			return fmt.Errorf("control: resume orphan close-out: %w", appendErr)
		}
	}
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

	// 权限回调桥（终评 Critical-1 第三层）：先起监听，再把回调地址经 Boot
	// Env 注入（引擎子进程继承 → `mulix mcp serve` 孙进程继承——评审
	// Important-1：地址绝不入 mcp-config/指纹，否则逐次启动的临时端口破坏
	// SC-003 字节稳定）。仅当注册面真的含 mulix stdio 条目时才起桥——
	// Registry 非 nil 但空面（selfCommand 回退）不起无用监听；监听失败 →
	// 不注入，serve 侧 fail-closed deny。
	if c.hasMulixRegistryEntry() {
		if pcs := c.startPermissionCallback(); pcs != nil {
			c.mu.Lock()
			c.permCallback = pcs
			c.mu.Unlock()
			boot.Env = append(boot.Env, "MULIX_PERMISSION_CALLBACK="+pcs.addr)
		}
	}

	// 子代理回调桥（M12/Task 3）：Factory 非空且注册面含 mulix 条目时组装
	// Manager 并注入 MULIX_SUBAGENT_CALLBACK——Boot Env 通道，绝不入
	// mcp-config（SC-003 字节稳定，与权限回调同一裁定；无 serve 子进程时
	// 起监听是无用暴露面，故同样受 hasMulixRegistryEntry 门控）。失败路径
	// 不阻断启动：不注入 env，工具侧 fail-closed deny（引擎不悬挂）。
	// Publish 走 c.publish——镜像 Append 成功才经 Sink 发布（先持久化后发布
	// 红线由父侧保证，design 002 subagent.Options.Publish 注）。
	if c.opts.SubagentFactory != nil && c.hasMulixRegistryEntry() {
		mgr, err := subagent.NewManager(subagent.Options{
			Root:          filepath.Dir(c.opts.Store.Dir()), // sessions 根（与主会话同池）
			MaxDepth:      c.opts.SubagentLimits.MaxDepth,
			MaxConcurrent: c.opts.SubagentLimits.MaxConcurrent,
			Factory:       c.opts.SubagentFactory,
			Publish:       c.publish,
		})
		if err != nil {
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
				Content: "子代理编排器组装失败（mulix_spawn 降级为 fail-closed deny）: " + err.Error()})
		} else if scs := c.startSubagentCallback(managerOps{m: mgr}); scs != nil {
			c.mu.Lock()
			c.subMgr = mgr
			c.subCallback = scs
			c.mu.Unlock()
			boot.Env = append(boot.Env, "MULIX_SUBAGENT_CALLBACK="+scs.addr)
		}
	}

	// 记忆会话溯源（M13/T13.2）：serve 子进程的 memory_save 用它给条目盖
	// source_session 戳（Boot Env 通道——绝不入 mcp-config，SC-003 字节稳定，
	// 与 MULIX_SUBAGENT_CALLBACK 同一裁定）。仅 Memory 已接线且注册面含 mulix
	// 条目（存在会消费它的 serve 子进程）时注入；value = 本会话镜像 id。
	if c.opts.Memory != nil && c.hasMulixRegistryEntry() {
		boot.Env = append(boot.Env, "MULIX_MEMORY_SESSION="+c.sessionID)
	}

	artifact, err := c.composeBoot(boot)
	if err != nil {
		return fmt.Errorf("control: compose boot: %w", err)
	}
	host, err := c.opts.HostFactory(artifact)
	if err != nil {
		return fmt.Errorf("control: host factory: %w", err)
	}
	if err := host.Start(ctx, artifact); err != nil {
		// Start 失败也要收掉宿主：真实宿主 Start 的 spawn 后失败路径杀进程
		// 树但不清理 --mcp-config 临时文件（cleanupTemp 只在 Close 跑）——
		// 不 Close 则每次被拒的启动泄漏一个临时文件（T27 评审 Important-1）。
		_ = host.Close(ctx)
		return fmt.Errorf("control: engine start: %w", err)
	}

	c.mu.Lock()
	c.host = host
	c.mu.Unlock()
	// 镜像 engine_session_id 快照必须在泵启动前捕获：onInit（首个 init）
	// 会无条件把引擎上报值回填 meta——之后读到的恒为新值，镜像不一致将
	// 永远不可见（评审 fix-round：原实现在 initSeen 后读取，分支成死代码）。
	mirrorEngineID, metaErr := "", error(nil)
	if boot.ResumeEngineSessionID != "" {
		meta, err := c.opts.Store.ReadMeta()
		if err != nil {
			metaErr = err
		} else {
			mirrorEngineID, _ = meta["engine_session_id"].(string)
		}
	}
	go c.pump(host)

	// init 等待（终评协议适配）：真实 claude 2.1.291 的 init 在首条 user
	// message 之后才发出（实测：stdin 开 20s 无 init；消息后 1.1s 到达）——
	// 「NewSession 等 init ← Submit 等 NewSession ← 引擎等首条消息」是三方
	// 死锁。故 initSeen 等待只保留 pumpDone 快速失败与宽限窗：宽限内 init
	// 未到（fake 引擎/即时协议不会走到这）→ 临时放行，Submit 得以发生；
	// init 随首回合到达后 onInit 照常回填 meta 并补发 resume 链日志。
	const initGrace = 2 * time.Second
	select {
	case <-c.initSeen: // 首个 init 已消费（TurnStarted 的 engine id 可靠）
		if boot.ResumeEngineSessionID != "" {
			c.logResumeChain(boot.ResumeEngineSessionID, mirrorEngineID, metaErr)
		}
	case <-c.pumpDone: // 引擎在 init 前就退出——快速失败，不等 ctx 超时
		return errors.New("control: engine exited before init")
	case <-time.After(initGrace):
		if boot.ResumeEngineSessionID != "" {
			// resume 链校验语义移到 onInit 首次消费（引擎上报后以引擎为权威）。
			// 锁保护（终评审 Important-2）：pump goroutine 的 onInit 在锁下读
			// 这三个字段——start goroutine 的写入必须同样持锁。
			c.mu.Lock()
			c.resumeRequested = boot.ResumeEngineSessionID
			c.resumeMirrorID = mirrorEngineID
			c.resumeMetaErr = metaErr
			c.mu.Unlock()
		}
	case <-ctx.Done():
		return fmt.Errorf("control: waiting init: %w", ctx.Err())
	}
	// 指令文件快照 + 变更轮询（M15/FR-109 前半）：启动成功（含 init 宽限
	// 放行）才装载与起 watcher——启动失败路径不起（无 goroutine 泄漏）。
	// 快照绝不改 Boot 面（SC-003：system prompt/mcp-config 字节与 P1 一致）。
	c.startInstructionWatcher()
	return nil
}

// logResumeChain 落 resume 链事件（design §10）：续链留痕 + 两类不一致告警
// （请求 id ≠ 引擎上报；镜像快照 ≠ 引擎上报——如崩溃后错续别的会话）。
// 不一致时以引擎为上下文权威：EngineSessionID 取 init 上报值；meta 亦已被
// onInit 回填为上报值（审计靠告警事件 + 镜像中历史 TurnStarted 的
// EngineSessionID 字段）。mirrorEngineID/metaErr 由 start() 在泵启动前捕获。
func (c *Controller) logResumeChain(requested, mirrorEngineID string, metaErr error) {
	reported := c.EngineSessionID()
	if reported == "" {
		reported = "(unknown)"
	}
	c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "info", IsMeta: true,
		Content: fmt.Sprintf("resume: 从引擎会话 %s 续链（镜像已接续记录）", reported)})
	if requested != reported {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: fmt.Sprintf("resume: 请求续链 %s，引擎上报 %s（以引擎为上下文权威）",
				requested, reported)})
	}
	if metaErr != nil {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: "resume: 读取镜像 meta 失败: " + metaErr.Error()})
	}
	if mirrorEngineID != "" && mirrorEngineID != reported {
		// 空镜像 id（从未记录过引擎会话）静默跳过本分支——fail-open 但无害：
		// 有未闭合回合的镜像几乎必然已记录 init id，且请求 id 告警仍在。
		// 「meta 已更新」措辞的前提是 onInit 回填成功——回填失败时镜像另有
		// 独立告警留痕，本条文案的偏差可接受（P1）。
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: fmt.Sprintf("resume: 镜像记录引擎会话 %s，引擎上报 %s（以引擎为准，meta 已更新）",
				mirrorEngineID, reported)})
	}
}

// permissionCallbackServer 是权限回路的跨进程桥（终评 Critical-1 第三层）：
// 真实引擎经 `mulix mcp serve` 子进程上的 mcp__mulix__permission 工具发起
// 审批（--permission-prompt-tool 传输），gate 在前台进程——回调服务器接收
// serve 子进程的 POST {tool_name, input, tool_use_id}，复用 evaluateCanUseTool
// 裁决核，应答 behavior JSON（serve 侧原样透传给引擎）。
type permissionCallbackServer struct {
	ctrl  *Controller
	srv   *http.Server
	addr  string // 含随机 token 路径（http://127.0.0.1:PORT/<token>）
	token string
}

// startPermissionCallback 启动 127.0.0.1 回调监听（会话生命周期绑定——
// Close 时关闭）。监听失败不阻断启动：composeBoot 不注入回调地址，serve
// 侧权限工具 fail-closed deny（引擎不悬挂，审批回路降级为 deny）。
// 安全面（终评审 Minor-4 裁决：P1 回环可接受 + 四项廉价加固全做）：
// 127.0.0.1 绑定 + 会话级随机 token 路径（不猜不可达）+ 请求体上限 +
// 会话关闭后拒绝处理。
func (c *Controller) startPermissionCallback() *permissionCallbackServer {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
			Content: "权限回调监听失败（审批回路降级为 fail-closed deny）: " + err.Error()})
		return nil
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		_ = l.Close()
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
			Content: "权限回调 token 生成失败（审批回路降级为 fail-closed deny）: " + err.Error()})
		return nil
	}
	pcs := &permissionCallbackServer{
		ctrl:  c,
		token: hex.EncodeToString(token[:]),
	}
	pcs.addr = "http://" + l.Addr().String() + "/" + pcs.token
	mux := http.NewServeMux()
	mux.HandleFunc("/"+pcs.token, pcs.handle)
	pcs.srv = &http.Server{Handler: mux}
	go func() { _ = pcs.srv.Serve(l) }()
	return pcs
}

// handle 处理一次审批回调：body {tool_name, input, tool_use_id} → 裁决核 →
// behavior JSON。错误路径 = 200 + deny（serve 侧 fail-closed 语义与协议
// 层解耦——引擎只见 behavior）。
func (p *permissionCallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	p.ctrl.mu.Lock()
	closed := p.ctrl.closed
	p.ctrl.mu.Unlock()
	if closed || p.srv == nil { // 会话已收口——不再产出裁决
		writeBehavior(w, map[string]any{"behavior": "deny", "message": "session closed"})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var args struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	body := http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB 上限（工具入参远小于此）
	if err := json.NewDecoder(body).Decode(&args); err != nil || args.ToolName == "" || args.ToolUseID == "" {
		writeBehavior(w, map[string]any{"behavior": "deny",
			"message": "malformed permission callback body"})
		return
	}
	if len(args.Input) == 0 {
		args.Input = json.RawMessage(`{}`)
	}
	// 与 FakeHost 传输（onControlRequest）共用裁决核——同 tool_use_id 重发
	// 取缓存裁决（singleflight），镜像 approval_request/resolved 审计一致。
	resp := p.ctrl.evaluateCanUseTool("callback-"+args.ToolUseID, args.ToolUseID,
		args.ToolName, args.Input, nil)
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp.Response, &envelope); err != nil ||
		len(envelope.Result.Content) == 0 {
		writeBehavior(w, map[string]any{"behavior": "deny", "message": "ruling envelope malformed"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(envelope.Result.Content[0].Text))
}

// writeBehavior 写 behavior JSON（回调应答的统一出口）。
func writeBehavior(w http.ResponseWriter, behavior map[string]any) {
	raw, _ := json.Marshal(behavior)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// close 收口回调监听（会话关闭时调用；幂等）。
func (p *permissionCallbackServer) close() {
	if p == nil || p.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
}

// hasMulixRegistryEntry 判定注册面是否真的含 mulix stdio 条目（决定回调桥
// 是否启动——空注册面时无 serve 子进程，起监听是无用暴露面）。
func (c *Controller) hasMulixRegistryEntry() bool {
	c.mu.Lock()
	reg := c.opts.Registry
	c.mu.Unlock()
	if reg == nil {
		return false
	}
	for _, s := range reg.MCPServers() {
		if s.Name == permissionServerName {
			return true
		}
	}
	return false
}

// ---- 子代理操作回调桥（M12/Task 3；permissionCallbackServer 同构） ----

// subagentOps 是子代理回调桥消费的窄接口（R-P2-4：回调桥的内部缝——
// *subagent.Manager 经 managerOps 适配注入 ctx 并重排参数；测试 stub 直接
// 实现。不携带逐请求 ctx——Manager 编排操作由 Close/Cancel 的级联语义收口）。
type subagentOps interface {
	Spawn(task, parentID string, depth int) (string, error)
	Poll(subID string) (subagent.Status, error)
	Cancel(subID, reason string) error
	// Summary 是已收口子代理的最终 assistant 文本（Task 3 裁定/Task 4 接线：
	// poll 应答的 summary 来源，FR-102 最终消息回收；running/未知 → ("", false)）。
	Summary(subID string) (string, bool)
}

// managerOps 把 *subagent.Manager 适配为 subagentOps：注入 Background ctx
// （取消经 Manager.Cancel 级联，不靠逐请求 ctx）并重排 Spawn 参数序。
type managerOps struct{ m *subagent.Manager }

func (a managerOps) Spawn(task, parentID string, depth int) (string, error) {
	return a.m.Spawn(context.Background(), parentID, task, depth)
}

func (a managerOps) Poll(subID string) (subagent.Status, error) {
	return a.m.Poll(context.Background(), subID)
}

func (a managerOps) Cancel(subID, reason string) error {
	return a.m.Cancel(context.Background(), subID, reason)
}

func (a managerOps) Summary(subID string) (string, bool) { return a.m.Summary(subID) }

// subagentCallbackServer 是子代理操作的跨进程桥（M12/Task 3）：serve 子进程
// 上的 mulix_spawn/poll/cancel 工具把 {op,...} POST 到本服务器，转发给前台
// 的 subagent.Manager，应答 JSON 原样透传给引擎。permissionCallbackServer
// 同构的安全面：127.0.0.1 绑定 + 会话级随机 token 路径（不猜不可达）+
// 请求体上限 + 会话关闭后拒绝处理。
type subagentCallbackServer struct {
	ctrl  *Controller
	mgr   subagentOps
	srv   *http.Server
	addr  string // http://127.0.0.1:PORT/<token>（env 注入值，与权限回调同形）
	token string
}

// startSubagentCallback 启动子代理回调监听（会话生命周期绑定——Close 时
// 关闭）。监听/token 失败不阻断启动：不注入 MULIX_SUBAGENT_CALLBACK，工具
// 侧 fail-closed deny（引擎不悬挂）。仅 Factory 非空且注册面含 mulix 条目
// 时由 start() 调用。
func (c *Controller) startSubagentCallback(mgr subagentOps) *subagentCallbackServer {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
			Content: "子代理回调监听失败（mulix_spawn 降级为 fail-closed deny）: " + err.Error()})
		return nil
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		_ = l.Close()
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning", IsMeta: true,
			Content: "子代理回调 token 生成失败（mulix_spawn 降级为 fail-closed deny）: " + err.Error()})
		return nil
	}
	scs := &subagentCallbackServer{
		ctrl:  c,
		mgr:   mgr,
		token: hex.EncodeToString(token[:]),
	}
	scs.addr = "http://" + l.Addr().String() + "/" + scs.token
	mux := http.NewServeMux()
	// 精确路径（工具 POST 的 env 值）+ 子树（token 前缀下的操作路径）——
	// 同一 handler：op 由 body 的 op 字段区分，路径后缀不参与路由。
	mux.HandleFunc("/"+scs.token, scs.handle)
	mux.HandleFunc("/"+scs.token+"/", scs.handle)
	scs.srv = &http.Server{Handler: mux}
	go func() { _ = scs.srv.Serve(l) }()
	return scs
}

// handle 处理一次子代理操作回调：body {op:"spawn"|"poll"|"cancel",
// task?, sub_session_id?, parent_id?, depth?} → Manager → 应答 JSON
// {ok, sub_session_id?, status?, error?}。错误路径 = 200 + {ok:false}
// （工具侧原样透传给引擎——业务错误与协议层解耦，权限回调同款语义）；
// 非 POST = 405（工具侧非 200 → fail-closed）。
func (s *subagentCallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	s.ctrl.mu.Lock()
	closed := s.ctrl.closed
	s.ctrl.mu.Unlock()
	if closed || s.srv == nil { // 会话已收口——不再编排子代理
		writeOpResult(w, map[string]any{"ok": false, "error": "session closed"})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var op struct {
		Op    string `json:"op"`
		Task  string `json:"task"`
		SubID string `json:"sub_session_id"`
		// ParentID/Depth 仅解码留痕（协议形状完整）：wire 值不可信——spawn 的
		// 真实 parent/depth 由前台按本会话镜像链计算（Task 3 评审移交，见下）。
		ParentID string `json:"parent_id"`
		Depth    int    `json:"depth"`
	}
	body := http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB 上限（工具入参远小于此）
	if err := json.NewDecoder(body).Decode(&op); err != nil || op.Op == "" {
		writeOpResult(w, map[string]any{"ok": false, "error": "malformed subagent callback body"})
		return
	}
	switch op.Op {
	case "spawn":
		// Task 3 评审移交（M12/Task 4 落实）：wire 的 parent_id/depth 不可信
		//（工具契约无 depth 字段，桥恒收 0）——真实委派链状态由前台按本会话
		// 镜像的 parent_id 链计算：parent = 本会话 id（本进程内 spawn 一律挂
		// 本会话），depth = 镜像链祖先数（主会话直生子代理 0，每深一层 +1）
		// ——MaxDepth 限额因此可达（e2e 钉死第三层 spawn 被拒）。
		subID, err := s.mgr.Spawn(op.Task, s.ctrl.sessionID, s.ctrl.delegationDepth())
		if err != nil {
			writeOpResult(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeOpResult(w, map[string]any{"ok": true, "sub_session_id": subID})
	case "poll":
		status, err := s.mgr.Poll(op.SubID)
		if err != nil {
			writeOpResult(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		resp := map[string]any{"ok": true, "sub_session_id": op.SubID, "status": string(status)}
		// FR-102 最终消息回收（Task 3 裁定）：completed/failed 终态应答携带
		// summary（Manager.Summary 的来源；running 不携带该键）。
		if summary, ok := s.mgr.Summary(op.SubID); ok {
			resp["summary"] = summary
		}
		writeOpResult(w, resp)
	case "cancel":
		if err := s.mgr.Cancel(op.SubID, "requested via mulix_cancel"); err != nil {
			writeOpResult(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeOpResult(w, map[string]any{"ok": true, "sub_session_id": op.SubID})
	default:
		writeOpResult(w, map[string]any{"ok": false, "error": "unsupported op: " + op.Op})
	}
}

// writeOpResult 写子代理操作应答 JSON（回调应答的统一出口）。
func writeOpResult(w http.ResponseWriter, result map[string]any) {
	raw, _ := json.Marshal(result)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// delegationDepth 计算本会话在委派链中的深度（Task 3 评审移交：MaxDepth 的
// 真实深度来源）。链路数据 = sessions 根下各镜像 meta.json 的 parent_id
// （子代理组装时由 cli 工厂写入——Task 4）：沿 parent_id 逐层回溯，祖先数即
// 本会话子代理的 spawn 深度。父目录缺失/损坏按链尽处理（保守降为浅层）；
// 迭代上限 64 防御畸形环链不致死循环。
func (c *Controller) delegationDepth() int {
	root := filepath.Dir(c.opts.Store.Dir())
	cur := c.sessionID
	for depth := 0; depth < 64; depth++ {
		parent, ok := parentIDOf(sessionx.SessionDir(root, cur))
		if !ok || parent == "" {
			return depth
		}
		cur = parent
	}
	return 64
}

// parentIDOf 读一个会话镜像 meta.json 的 parent_id（无该键/不可读 → ("", false)）。
// 直读文件而非 OpenStore：链回溯是只读检查，不取镜像写句柄。
func parentIDOf(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return "", false
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", false
	}
	p, _ := meta["parent_id"].(string)
	return p, true
}

// close 收口子代理回调监听（会话关闭时调用；幂等）。closed 判据是
// ctrl.closed（Close 先置位再收口）——挂起请求得 {ok:false,"closed"}。
func (s *subagentCallbackServer) close() {
	if s == nil || s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
}

// composeBoot 组装引擎拉起参数（P1 固定集）。registry 由 cli 注入
// （Options.Registry；nil = 空面）。注入面含 mulix 服务器时同步钉
// --permission-prompt-tool=mcp__mulix__permission——mcp-config 里没有该
// 服务器时绝不能指向它（引擎会拒绝启动；fail-closed）。
func (c *Controller) composeBoot(base engine.BootConfig) (engine.BootConfig, error) {
	reg := c.opts.Registry
	if reg == nil {
		reg = emptyRegistry{}
	}
	artifact, err := cache.Compose(c.opts.Config, c.opts.Workspace, reg)
	if err != nil {
		return engine.BootConfig{}, err
	}
	base.AppendSystemPrompt = artifact.AppendSystemPrompt
	base.MCPConfigJSON = artifact.MCPConfigJSON
	base.AllowedTools = artifact.AllowedTools
	base.DisallowedTools = artifact.DisallowedTools
	base.PermissionMode = artifact.PermissionMode
	// 外部 MCP 工具超时（M14/评审修复轮 1 接线）：Compose 以全组最小生效
	// timeout_ms 算出的引擎 env 并入 Boot Env 通道（与回调地址同面；绝不入
	// mcp-config 字节——SC-003 同款裁定）。无外部声明时 EngineEnv 为 nil →
	// append 为 no-op，P1-only boot.Env 零 delta。
	base.Env = append(base.Env, artifact.EngineEnv...)
	base.SettingSources = []string{"user"} // 实测红线：必须显式含 user
	base.Workspace = c.opts.Workspace
	base.Model = c.opts.Config.DefaultModel
	base.AllowEngineDrift = c.opts.AllowEngineDrift
	// 引擎可执行（终评 Critical-1）：config engine.path 优先，空值回退
	// "claude"（PATH 解析——exec.Command 的 LookPath 语义；与 doctor 探测
	// 同一约定）。缺此接线 ClaudeHost.Start 对空 EnginePath fail-closed，
	// 两个产品入口（TUI/run）都无法拉起真实引擎。
	base.EnginePath = c.opts.Config.Engine.Path
	if base.EnginePath == "" {
		base.EnginePath = "claude"
	}
	for _, s := range reg.MCPServers() {
		if s.Name == permissionServerName {
			base.PermissionPromptTool = "mcp__" + permissionServerName + "__" + permissionToolName
			break
		}
	}
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

// newlineFolding 把条目文本内的换行折叠为空格（评审修复轮 1 Minor-1）：
// [记忆召回] 块的 bullet 形状 = 每条目恰一行——多行原文若不折叠会伪造
// bullet 行（"\n- x" 混入）并虚增召回留痕计数。折叠只发生在注入点
// （withMemoryRecall，格式唯一所有地）；store 保留用户原文。
var newlineFolding = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// withMemoryRecall 是记忆召回的 turn-tail 注入函数（M13/T13.2；C5 注入点
// 唯一化——**唯一调用点在 submit**，Task 10 lint 扫描钉此契约：user message
// 组装只经本函数）。给定本回合用户输入：
//   - Memory 未接线（[tools] memory_search=false）→ 空串（零注入）；
//   - 命中 0 条 → 空串（静默跳过——spec Edge Cases）；
//   - 命中 → 格式化 [记忆召回]\n- <entry text>\n 块（design 002 §4），由
//     调用方追加进 engine.UserMessage.TurnTail（P1 预设计的 turn-tail 块
//     通道——引擎侧以独立 text block 附在 user message 尾部）。
//
// 返回值仅为检索结果的文本块；绝不改 AppendSystemPrompt/MCPConfigJSON/
// AllowedTools（SC-003 护栏由 memory_injection_test 钉死）。ctx 保留注入
// 语义签名（plan 契约形状），当前检索为进程内内存操作不消费它。
func (c *Controller) withMemoryRecall(ctx context.Context, text string) string {
	if c.opts.Memory == nil || strings.TrimSpace(text) == "" {
		return ""
	}
	k, budget := c.opts.Config.Memory.RecallTopK, c.opts.Config.Memory.RecallBudgetChars
	if k <= 0 {
		k = defaultRecallTopK
	}
	if budget <= 0 {
		budget = defaultRecallBudgetChars
	}
	hits := c.opts.Memory.Search(text, k, budget)
	if len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[记忆召回]\n")
	for _, h := range hits {
		b.WriteString("- ")
		b.WriteString(newlineFolding.Replace(h.Text)) // 多行折叠：每条目恰一行（评审 Minor-1）
		b.WriteString("\n")
	}
	return b.String()
}

func (c *Controller) submit(ctx context.Context, msg engine.UserMessage) error {
	// 记忆召回（M13/T13.2）：withMemoryRecall 的唯一调用点（C5 turn-tail
	// 注入点唯一化，Task 10 lint 扫描面）。
	tail := c.withMemoryRecall(ctx, msg.Text)
	if tail != "" {
		msg.TurnTail = append(msg.TurnTail, tail)
	}
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
	// 召回注入审计（SC-006：召回注入镜像可审计）：命中才留痕（0 条静默）。
	// 块形状恒为 header + 每命中一条 "- " 行，行数即命中数。
	if tail != "" {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "info", IsMeta: true,
			Content: fmt.Sprintf("记忆召回: %d 条命中，已注入本回合 turn tail", strings.Count(tail, "\n- "))})
	}
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
// 处理，镜像如实记录——design §6）。等待人工裁决的待审请求一并 fail-closed
// 拒绝（解阻塞泵，镜像 resolved 留痕）。
func (c *Controller) Cancel(ctx context.Context) error {
	c.mu.Lock()
	host := c.host
	c.mu.Unlock()
	if host == nil {
		return ErrNoSession
	}
	if c.opts.Approvals != nil {
		c.opts.Approvals.CancelAll("cancelled:interrupt")
	}
	return host.Interrupt(ctx)
}

// gateRuling 是一次权限裁决的快照（重发信封直接应答，人只裁决一次）。
type gateRuling struct {
	decision   string // allow | deny
	source     string // policy | session | user | timeout
	resolvedBy string
}

// Approve 对待审请求交付人工裁决（拒绝时 resolvedBy 即拒绝理由，随
// approval_resolved 镜像留痕并回给引擎）。无审批桥（无人值守）或无此待审
// 请求 → ErrNoPendingApproval。
func (c *Controller) Approve(toolUseID string, allow bool, resolvedBy string) error {
	if c.opts.Approvals == nil {
		return ErrNoPendingApproval
	}
	if !c.opts.Approvals.Resolve(toolUseID, guard.ApprovalVerdict{Allow: allow, ResolvedBy: resolvedBy}) {
		return ErrNoPendingApproval
	}
	return nil
}

// ApproveSession 在裁决允许的同时授予会话级授权（工具+规范化范围；
// approval_resolved{source=session} 即镜像留痕）。典型交互：审批卡片上的
// 「本会话允许当前范围」。
func (c *Controller) ApproveSession(toolUseID, resolvedBy string) error {
	if c.opts.Approvals == nil {
		return ErrNoPendingApproval
	}
	req, ok := c.opts.Approvals.PendingRequest(toolUseID)
	if !ok {
		return ErrNoPendingApproval
	}
	if !c.opts.Approvals.Resolve(toolUseID, guard.ApprovalVerdict{Allow: true, ResolvedBy: resolvedBy}) {
		return ErrNoPendingApproval
	}
	c.opts.Gate.GrantSession(req.Tool, req.Scope)
	return nil
}

// HasPendingApproval 报告某待审请求是否仍在等待裁决（TUI 卡片状态）。
func (c *Controller) HasPendingApproval(toolUseID string) bool {
	return c.opts.Approvals != nil && c.opts.Approvals.HasPending(toolUseID)
}

// Mirror 返回权威镜像（TUI/测试读取事件流与投影；写入仍归 control）。
func (c *Controller) Mirror() *sessionx.Store { return c.opts.Store }

// Subagent 返回子代理编排器（Task 4：TUI /agents 面板消费的窄 getter）。
// 未启用（Factory 为 nil、注册面无 mulix 条目或组装失败）时返回 nil。
func (c *Controller) Subagent() *subagent.Manager {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subMgr
}

// Meter 返回记账器。**并发红线**：Meter 单写者（泵），跨 goroutine 只读
// Report 会与 Observe 竞态——外部读取一律走 Snapshot()（泵侧更新的
// lastReport 快照），不得直接调 Meter.Report()。
func (c *Controller) Meter() *cache.Meter { return c.opts.Meter }

// Snapshot 是状态栏快照（TUI 数据源）。
type Snapshot struct {
	SessionID       string          // mulix 会话 id
	EngineSessionID string          // 引擎 session id（首个 init）
	Model           string          // 引擎侧模型名
	CLIVersion      string          // 引擎 CLI 版本
	Turn            int64           // 回合序号
	Running         bool            // 是否有活跃回合
	MeterReport     cache.Report    // 记账汇总（泵侧快照，不触碰 Meter 内部）
	TurnTokens      cache.TurnUsage // 最近回合用量（泵侧快照——状态栏「本回合 tokens」）
	Dir             string          // 会话镜像目录
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
		TurnTokens:      c.lastTurnTokens,
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
	cb := c.permCallback
	scs := c.subCallback
	mgr := c.subMgr
	iw := c.instrWatch
	c.mu.Unlock()
	if iw != nil {
		iw.Close() // 停止指令文件轮询（M15）——closed 先置位再收口（回调桥同纪律）
	}
	if mgr != nil {
		// 级联取消全部运行中子代理（Review Focus 孤儿回合钉：父会话收口时
		// 子镜像必须无孤儿回合——各子 ctrl.Close 的泵收口合成 TurnDone，随后
		// 才收本侧监听/引擎； grandchildren 经同一接线逐层递归）。
		mgr.Close(ctx)
	}
	if scs != nil {
		scs.close() // 子代理回调监听收口——serve 子进程的挂起操作得 {ok:false,"closed"}
	}
	if cb != nil {
		cb.close() // 先收口回调监听——serve 子进程的挂起审批快速失败（deny）
	}
	if host == nil {
		// start 未走到挂泵（构造后 NewSession/Resume 失败即此态）——无泵可
		// 等，pumpDone 永不关闭；直接返回（T27 e2e 暴露：Close 在此曾无限
		// select 阻塞）。
		return nil
	}
	if err := host.Close(ctx); err != nil {
		return err
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
			content := ev.Trace.Message
			if ev.Trace.Code != "" {
				// code 进镜像（engine_drift 等治理事件的可检索留痕）
				content = fmt.Sprintf("[%s] %s", ev.Trace.Code, ev.Trace.Message)
			}
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "info",
				Content: content, IsMeta: true})
		}
	case engine.KindPermissionDenied:
		if ev.Denied != nil {
			c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
				Content: fmt.Sprintf("permission denied: %s: %s", ev.Denied.ToolName, ev.Denied.Message)})
		}
	case engine.KindExit:
		c.onExit(ev)
	case engine.KindControlRequest:
		c.onControlRequest(host, ev)
	case engine.KindHookResponse:
		c.onHook(ev)
	default:
		// KindNoise/KindHookStarted：informational 容忍忽略（钩子仅在
		// hook_response 分发——complete record，见 onHook 注）。
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
		// resume 延迟留痕（终评协议适配）：start() 宽限放行时链校验在此补发
		c.mu.Lock()
		requested, mirrorID, metaErr, logged := c.resumeRequested, c.resumeMirrorID, c.resumeMetaErr, c.resumeLogged
		c.resumeLogged = true
		c.mu.Unlock()
		if requested != "" && !logged {
			c.logResumeChain(requested, mirrorID, metaErr)
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
	c.lastTurnTokens = c.opts.Meter.TurnTokens()
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
//
// 口径注记（T26 评审钉）：此处用内存 Meter.Report（本引擎会话实时投影），
// 不改用 cache.ReadUsageReport（跨进程权威口径，含 resume 前历史）——
// cache.json 的语义是「当前运行的 Meter 汇总投影」。两口径并存是有意设计，
// 勿在此处单方面统一（T26 报告 Ruling-6b）。
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
// （镜像坏尾时泵不得崩溃、前端仍知悉）。pubMu 串行化发布路径（评审 Task 23
// carry）：Submit（TurnStarted/send_failed）与泵（回合间事件）会并发到达
// ——sessionx/jsonl 写侧无锁，这里保证 Append 互斥且与 Sink 顺序一致。
// 注意：Seq 在进入本函数前已分配（调用点 c.nextSeq()），跨 goroutine 的
// 镜像 seq 因此可能非严格单调（如 seq6 先于 seq5 落盘）——审计读取按行序，
// 无消费方依赖 seq 排序。
func (c *Controller) publish(ev any) {
	c.pubMu.Lock()
	defer c.pubMu.Unlock()
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

// ---- 控制请求路由（Task 23 / 权限回路） ----

// permissionServerName / permissionToolName 是 P1 权限回路的 MCP 路由常量
// （Task 3 Ruling：--permission-prompt-tool 指向 mulix 服务器的 permission
// 工具；其他 MCP 服务器/方法 P1 不代理，一律 error 应答防引擎悬挂）。
const (
	permissionServerName = "mulix"
	permissionToolName   = "permission"
)

// canUseToolCall 是 can_use_tool 的 MCP tools/call 解析（Task 3 Ruling：
// arguments = {tool_name, input, tool_use_id}；应答 content JSON =
// {behavior: allow|deny, …}）。
type canUseToolCall struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  struct {
		Name      string `json:"name"`
		Arguments struct {
			ToolName  string          `json:"tool_name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
		} `json:"arguments"`
	} `json:"params"`
}

// onControlRequest 处理一条控制信封：路由裁决并把应答按原信封 request_id
// 回给引擎（协议 §5：重发携带新 request_id，宿主逐信封应答）。
func (c *Controller) onControlRequest(host engine.Host, ev engine.Event) {
	cr := ev.Control
	if cr == nil {
		return
	}
	resp := c.handleControlRequest(cr)
	if err := host.RespondControl(context.Background(), cr.RequestID, resp); err != nil {
		c.publish(event.NoticeEvent{Seq: c.nextSeq(), Level: "warning",
			Content: "控制应答失败: " + err.Error()})
	}
}

// handleControlRequest 归一路由：权限回路 → MCP result（success）；不认识
// 的 subtype/服务器/方法 → error 应答（fail-closed：引擎不悬挂）。
func (c *Controller) handleControlRequest(cr *engine.ControlRequest) engine.ControlResponse {
	errResp := func(msg string) engine.ControlResponse {
		raw, _ := json.Marshal(map[string]string{"error": msg})
		return engine.ControlResponse{SubType: "error", RequestID: cr.RequestID, Response: raw}
	}
	switch {
	case cr.Request.SubType != "mcp_message":
		return errResp("unsupported control subtype: " + cr.Request.SubType)
	case cr.Request.ServerName != permissionServerName:
		return errResp("unsupported mcp server: " + cr.Request.ServerName)
	}
	var call canUseToolCall
	if err := json.Unmarshal(cr.Request.Message, &call); err != nil {
		return errResp("malformed mcp message: " + err.Error())
	}
	if call.Method != "tools/call" || call.Params.Name != permissionToolName {
		return errResp("unsupported mcp request: " + call.Method + " " + call.Params.Name)
	}
	args := call.Params.Arguments
	if args.ToolName == "" || args.ToolUseID == "" {
		return errResp("can_use_tool requires tool_name and tool_use_id")
	}
	if len(args.Input) == 0 {
		args.Input = json.RawMessage(`{}`)
	}
	return c.evaluateCanUseTool(cr.RequestID, args.ToolUseID, args.ToolName, args.Input, call.ID)
}

// evaluateCanUseTool 是权限回路核心：镜像 approval_request（权限回路审计
// 起点）→ Gate 裁决（审批桥别名关联引擎 tool_use_id）→ 缓存裁决 → 镜像
// approval_resolved → MCP result。同 tool_use_id 的重发信封直接取缓存
// 裁决（人只裁决一次；裁决词汇映射：Gate deny/timeout → 镜像 timeout）。
func (c *Controller) evaluateCanUseTool(requestID, toolUseID, tool string, input, mcpID json.RawMessage) engine.ControlResponse {
	ruling := c.rulingFor(toolUseID, tool, input)
	evDecision, evSource := ruling.decision, ruling.source
	if evDecision == string(guard.DecisionDeny) && evSource == string(guard.SourceTimeout) {
		evDecision, evSource = "timeout", "timeout" // 词表：非人为 fail-closed
	}
	c.publish(event.ApprovalResolvedEvent{Seq: c.nextSeq(), ToolUseID: toolUseID,
		Decision: evDecision, Source: evSource, ResolvedBy: ruling.resolvedBy})
	return c.mcpBehaviorResponse(requestID, mcpID, ruling, input)
}

// rulingFor 是裁决的单飞入口（终评审 Important-3）：回调桥（HTTP handler
// goroutine）与泵（onControlRequest）并发进入——同 tool_use_id 的并发/重发
// 请求只评估一次（其余等待复用），人只裁决一次；resolved 缓存命中直接返回。
func (c *Controller) rulingFor(toolUseID, tool string, input json.RawMessage) gateRuling {
	c.mu.Lock()
	if r, cached := c.resolved[toolUseID]; cached {
		c.mu.Unlock()
		return r
	}
	if ch, inFlight := c.inflight[toolUseID]; inFlight {
		c.mu.Unlock()
		return <-ch // 首个评估进行中——等待并复用（发布面照旧：与重发信封同义）
	}
	ch := make(chan gateRuling, 1)
	c.inflight[toolUseID] = ch
	c.mu.Unlock()

	c.publish(event.ApprovalRequestEvent{Seq: c.nextSeq(), Turn: c.currentTurn(),
		ToolUseID: toolUseID, Tool: tool, Input: input})
	// 别名配对不变量：本函数是 Gate.Evaluate 的唯一生产调用点，且每次
	// Evaluate 前必须 SetAlias——别名配对 + 评估在 approveMu 内原子完成
	//（终评审 Important-3c：并发传输下 SetAlias/Evaluate 交错会把人的裁决
	// 交叉投递到错误工具调用；P1 审批语义 = 串行，与泵串行分发一致）。
	c.approveMu.Lock()
	if c.opts.Approvals != nil {
		c.opts.Approvals.SetAlias(toolUseID)
	}
	decision, err := c.opts.Gate.Evaluate(context.Background(), tool, input)
	c.approveMu.Unlock()
	if err != nil { // 防御：Gate 约定 err 恒空，仍 fail-closed 兜底
		decision = guard.GateDecision{Decision: guard.DecisionDeny,
			Source: guard.SourceTimeout, ResolvedBy: "gate: " + err.Error()}
	}
	ruling := gateRuling{decision: string(decision.Decision),
		source: string(decision.Source), resolvedBy: decision.ResolvedBy}
	c.mu.Lock()
	c.resolved[toolUseID] = ruling
	if c.inflight[toolUseID] == ch {
		ch <- ruling
		delete(c.inflight, toolUseID)
	}
	c.mu.Unlock()
	return ruling
}

// mcpBehaviorResponse 组 MCP tools/call 应答：content[0].text = behavior JSON
// （allow 回显 updatedInput；deny 附 message——§3 形状；P1 不改写输入）。
func (c *Controller) mcpBehaviorResponse(requestID string, mcpID json.RawMessage, r gateRuling, input json.RawMessage) engine.ControlResponse {
	var behavior map[string]any
	if r.decision == string(guard.DecisionAllow) {
		behavior = map[string]any{"behavior": "allow", "updatedInput": input}
	} else {
		msg := "denied by " + r.source
		if rb := strings.TrimPrefix(r.resolvedBy, "timeout: "); rb != "" && rb != r.source {
			msg += ": " + rb
		}
		behavior = map[string]any{"behavior": "deny", "message": msg}
	}
	behaviorJSON, _ := json.Marshal(behavior)
	envelope := map[string]any{
		"jsonrpc": "2.0",
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(behaviorJSON)}},
		},
	}
	if len(mcpID) > 0 {
		envelope["id"] = mcpID
	}
	raw, _ := json.Marshal(envelope)
	return engine.ControlResponse{SubType: "success", RequestID: requestID, Response: raw}
}

// ---- 钩子分发（Task 21 交接的映射钉） ----

// mapHookEvent 是 engine hook_event → guard.HookEvent 的显式四向映射
// （guard 不依赖 engine——映射只能钉在 control）。未知事件返回 false（与
// guard.Dispatch 的未知事件 no-op 语义一致）。
func mapHookEvent(s string) (guard.HookEvent, bool) {
	switch guard.HookEvent(s) {
	case guard.HookSessionStart, guard.HookPreToolUse, guard.HookPostToolUse, guard.HookStop:
		return guard.HookEvent(s), true
	}
	return "", false
}

// onHook 把引擎 hook_response 分发进 guard.Hooks。P1 仅在 hook_response
// 分发（complete record：Output/Outcome 齐备）；hook_started 无增量信息，
// 其独立分发是 P2 关注点。Stdout/Stderr/ExitCode 的合并规则（Task 21 交接
// 由 control 决定）：P1 直接透传 HookInfo.Output（引擎已合并摘要），
// 不做二次拼接——审计消费者需要原始 stdout 时再改（P2）。
func (c *Controller) onHook(ev engine.Event) {
	hi := ev.Hook
	if hi == nil || c.opts.Hooks == nil {
		return
	}
	he, ok := mapHookEvent(hi.HookEvent)
	if !ok {
		return
	}
	c.opts.Hooks.Dispatch(context.Background(), guard.HookPayload{
		Event: he, HookID: hi.HookID, HookName: hi.HookName,
		Output: hi.Output, Outcome: hi.Outcome,
	})
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

// ---- 指令文件快照与变更轮询（M15/FR-109 前半；design 002 §8） ----

// instructionWatchInterval 是指令文件轮询间隔（默认 2s）。包内变量便于测试
// 注入短间隔（wire 面无此参数——运行时恒走默认；同包测试串行执行，覆盖与
// 恢复由测试自管）。
var instructionWatchInterval = 2 * time.Second

// InstructionSnap 是单个指令文件的快照（内容 + sha256）。
type InstructionSnap struct {
	Content string // 文件全文（装载面读取）
	SHA256  string // 内容 sha256 hex——watcher 的比较依据（非 mtime）
}

// instructionSnap 是 brief Produces 接口的小写拼写（= InstructionSnap 的类型
// 别名；包内消费面沿用该拼写，两形态同型）。
type instructionSnap = InstructionSnap

// instructionFiles 是快照源文件清单（workspace 根相对；FR-109）。
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md"}

// loadInstructionSnapshots 读 workspace 的指令文件快照：键 = workspace 下的
// 文件路径（watcher 以键重读；提醒文案取 base 名），值 = 内容 + sha256。
// 文件不存在/不可读 → 键缺席（P1 快照语义：无文件即无快照；装载面静默
// 跳过——design 002 §8）。P1 从未读过指令文件（design 001 C3 的「启动快照」
// 落地为 AppendSystemPrompt 模板）——本函数补齐 FR-109 的前半。
func loadInstructionSnapshots(workspace string) map[string]instructionSnap {
	snaps := make(map[string]instructionSnap, len(instructionFiles))
	for _, name := range instructionFiles {
		path := filepath.Join(workspace, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue // 不存在/不可读——键缺席（不区分错误种类：装载面静默）
		}
		sum := sha256.Sum256(raw)
		snaps[path] = instructionSnap{Content: string(raw), SHA256: hex.EncodeToString(sum[:])}
	}
	return snaps
}

// LoadInstructionSnapshots 是 loadInstructionSnapshots 的导出形式（组合根
// 消费：`mulix instructions` 进程无运行中控制器句柄——启动快照不落镜像，
// 以同一装载函数对 workspace 现读现报）。
func LoadInstructionSnapshots(workspace string) map[string]InstructionSnap {
	return loadInstructionSnapshots(workspace)
}

// InstructionWatcher 是指令文件轮询 watcher 的句柄（watchInstructions 返回）。
type InstructionWatcher struct {
	quit      chan struct{} // Close 关闭——ticker 停止，goroutine 经此收口
	done      chan struct{} // goroutine 退出信号（泄漏检测/测试等待面）
	closeOnce sync.Once
}

// Close 停止轮询（幂等；channel 关闭即退出信号——goroutine 无泄漏退出）。
func (w *InstructionWatcher) Close() {
	if w == nil {
		return
	}
	w.closeOnce.Do(func() { close(w.quit) })
}

// Done 返回 goroutine 退出信号通道（Close 或 ctx 取消后关闭）。
func (w *InstructionWatcher) Done() <-chan struct{} {
	if w == nil {
		return nil
	}
	return w.done
}

// watchInstructions 启动指令文件变更轮询（导出便于测试）：interval 节拍重
// 读 files 的每个键，sha256 与基线比较（不是 mtime——touch 误报是 Review
// Focus 钉子）；变化/删除 → publish 一条 info 提醒（文案钉「快照保持，下
// 会话生效」），随后基线推进为当前观测——同文件多次变化在两次轮询间只报
// 一次，此后同内容不刷屏。files 缺席的键不监控（启动时无文件即无快照——
// P1 语义）。返回句柄：Close 停 ticker（goroutine 经 quit 通道收口，Done
// 可等待）；ctx 取消同样停止。interval <= 0 用默认 2s。
func watchInstructions(ctx context.Context, files map[string]instructionSnap, publish func(event.NoticeEvent), interval time.Duration) *InstructionWatcher {
	if interval <= 0 {
		interval = instructionWatchInterval
	}
	base := make(map[string]instructionSnap, len(files))
	for k, v := range files {
		base[k] = v
	}
	paths := make([]string, 0, len(base))
	for k := range base {
		paths = append(paths, k)
	}
	sort.Strings(paths) // 确定性轮询序（多文件同 tick 变化时提醒顺序稳定）
	w := &InstructionWatcher{quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		w.loop(ctx, base, paths, publish, interval)
	}()
	return w
}

// loop 是轮询核（单 goroutine 私有基线；每 tick 重读并比较 sha256）。
func (w *InstructionWatcher) loop(ctx context.Context, base map[string]instructionSnap, paths []string, publish func(event.NoticeEvent), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.quit:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					continue // 瞬态读取失败（如文件占用）——静默跳过不误报
				}
				// 删除 = 修改同语义：提醒一次，基线墓碑化（SHA256 空串
				// 哨兵——空内容的 sha256 是确定的非空 hex，不撞哨兵）。
				if base[path].SHA256 != "" {
					publish(instructionNotice(path))
					base[path] = instructionSnap{}
				}
				continue
			}
			sum := sha256.Sum256(raw)
			sha := hex.EncodeToString(sum[:])
			if sha == base[path].SHA256 {
				continue // 内容未变（含 mtime 变化的 touch）——不提醒
			}
			publish(instructionNotice(path))
			base[path] = instructionSnap{Content: string(raw), SHA256: sha}
		}
	}
}

// instructionNotice 组变更提醒事件（level=info + IsMeta——与 resume 链/
// 记忆召回等 info 级 harness 事件一致；文案由 brief 钉死）。
func instructionNotice(path string) event.NoticeEvent {
	return event.NoticeEvent{Level: "info", IsMeta: true,
		Content: fmt.Sprintf("指令文件变更: %s（快照保持，下会话生效）", filepath.Base(path))}
}

// startInstructionWatcher 是 start 序列尾的快照装载 + watcher 启动（M15/
// FR-109 前半）：仅在 start 成功路径调用——失败路径不起 watcher。快照存入
// 控制器（本任务只供 watcher 与 mulix instructions 消费——绝不改 Boot 面，
// SC-003）；无指令文件 → 不起 watcher（轮询空集无意义）。与 Close 的竞态：
// 存句柄前复查 closed，已收口则立即停掉刚起的 goroutine（无泄漏）。
func (c *Controller) startInstructionWatcher() {
	snaps := loadInstructionSnapshots(c.opts.Workspace)
	c.mu.Lock()
	c.instructionSnaps = snaps
	closed := c.closed
	c.mu.Unlock()
	if closed || len(snaps) == 0 {
		return
	}
	publish := func(ev event.NoticeEvent) {
		ev.Seq = c.nextSeq()
		c.publish(ev)
	}
	w := watchInstructions(context.Background(), snaps, publish, 0) // 0 → 默认 2s
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		w.Close() // Close 竞态抢先——立即收口刚起的 goroutine
		return
	}
	c.instrWatch = w
	c.mu.Unlock()
}
