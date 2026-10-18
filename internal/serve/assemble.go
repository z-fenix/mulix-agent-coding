// assemble.go——serve 侧 controller 真实装配(2026-10-18-web-ui-mvp Task 6)。
//
// 语义对照 internal/cli/session.go 的 buildComponents/assembleController
// (132-302)与 assembleKernelContext(304-356)独立实现:组合根不互依(serve 不
// import internal/cli),装配漂移由本包测试与 serve e2e(Task 13)钉住。逐条
// 对齐 cli:
//   - 显式规则集(deny → DecisionDeny / allow → DecisionAllow;解析失败 =
//     配置损坏 → 拒绝装配 fail-closed)→ policy → 审批桥 → Gate{Approver}
//     → Options{Gate, Approvals} 双槽同实例(cli:147-188 接线序);
//   - meter 记账续号:resume 经 cache.NextTurnNumber(cli openSession:714-726);
//   - memory/hook 按 config 条件装配(cli:197-214;Open 失败 fail-closed);
//   - kernel provide 列表(sink/store/meter/gate/config/approvals/memory/
//     profile)+ agentloop 挂载 + llm=claude 适配器 + Source/Kernel 回填
//     (cli:304-356 逐条);
//   - eventbus 门:cfg.Eventbus 单真源 → per-workspace 单例总线;门关删
//     残留 endpoint(cli:781-804 同语义独立实现);
//   - 生产 hostFactory = NewClaudeHost(LockPath: resolveEngineLockPath(ws))
//     (cli productionHostFactory:383-389;engine.lock 校验在 Host 层零改动)。
//
// web 会话恒 attended(评审 C1 裁定):桥恒建、Unattended 恒 false。web MVP
// 不接线子代理会话工厂(spec 子代理视图=第二期)——spawn/poll/cancel 三件套
// 不注册(语义同 cli selfCommand 失败分支),SubagentFactory=nil。
package serve

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"mulix/internal/agentloop"
	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/eventbus"
	"mulix/internal/guard"
	"mulix/internal/kernel"
	"mulix/internal/llm/claude"
	"mulix/internal/memory"
	"mulix/internal/registry"
	"mulix/internal/sessionx"
	"mulix/internal/tools/mulixmemory"
	"mulix/internal/tools/mulixsubagent"
	"mulix/internal/tools/mulixusage"
)

// serveVersion 是 mulix 版本(MCP implementation 元数据)。互指
// internal/cli.Version——组合根不互依故独立声明,漂移只影响 initialize 应答
// 元数据(无行为面)。
const serveVersion = "0.1.0-dev"

// assembleParams 是真实装配参数(Task 6):由 serve.Run 层(Task 7)经
// withAssembly 注入。零值语义:Cfg nil / SessionsRoot 空 = fail-closed 拒绝
// 装配;HostFactory nil = 生产 NewClaudeHost(测试换 FakeHost——引擎只是插件
// 之一,design 002 §2.1);UsageRoot 空 = 记账根取 SessionsRoot 父目录
// (标准布局 ~/.mulix/sessions → ~/.mulix,cli UsageRoot 同源)。
type assembleParams struct {
	Cfg          *config.Config
	SessionsRoot string
	UsageRoot    string
	AllowDrift   bool // --allow-engine-drift(serve.Options.AllowEngineDrift 直传)
	Stderr       io.Writer
	HostFactory  func(engine.BootConfig) (engine.Host, error)
}

// stderr 缺省 io.Discard(eventbus OpenBus 失败留痕面)。
func (p assembleParams) stderr() io.Writer {
	if p.Stderr == nil {
		return io.Discard
	}
	return p.Stderr
}

// hostOption 是 newSessionHost 的变参装配缝(定义于本文件——装配关注点;
// host.go 仅消费)。变参形态保持 Task 5 五参调用面不变。
type hostOption func(*sessionHost)

// withAssembly 注入真实装配参数。
func withAssembly(p assembleParams) hostOption {
	return func(h *sessionHost) { h.params = p }
}

// assembleReal 是 web 会话的真实装配(buildComponents 对齐;Task 5 assembleFn
// 槽的实现):镜像 store(resume = 恢复门+OpenStore / 新建 = Create)→ meter
// (resume 续号)→ 规则集/policy/桥/Gate → memory/hooks 条件装配 → kernel
// provide + agentloop 挂载 → eventbus 门 → NewController → NewSession/Resume
// (resume 语义经 h.resume 传递)。失败路径统一收口已开资源(store/meter/
// ctrl——cli openSession fail 闭包同纪律),无句柄泄漏。
func assembleReal(ctx context.Context, h *sessionHost) (*control.Controller, *control.UserApprovalBridge, error) {
	p := h.params
	if p.Cfg == nil {
		return nil, nil, errors.New("serve: assemble: config required (fail-closed)")
	}
	if p.SessionsRoot == "" || h.ID == "" {
		return nil, nil, errors.New("serve: assemble: sessions root and session id required")
	}
	cfg := *p.Cfg
	usageRoot := p.UsageRoot
	if usageRoot == "" {
		usageRoot = filepath.Dir(p.SessionsRoot)
	}

	var store *sessionx.Store
	var engineID string
	var err error
	if h.resume {
		if store, engineID, err = openForResume(h, p.SessionsRoot); err != nil {
			return nil, nil, err
		}
	} else {
		if store, err = sessionx.Create(p.SessionsRoot, sessionx.Options{
			ID: h.ID, Workspace: h.Workspace,
			Unattended: false, // web 会话恒 attended(评审 C1)
		}); err != nil {
			return nil, nil, fmt.Errorf("serve: create mirror: %w", err)
		}
	}
	var meter *cache.Meter
	var ctrl *control.Controller
	// 失败收口(cli openSession fail 闭包同纪律):ctrl 构造后任何失败路径
	// 释放 meter usage 句柄、镜像 store、control 宿主——Windows 句柄占用
	// 会卡 TempDir 清理。
	fail := func(err error) (*control.Controller, *control.UserApprovalBridge, error) {
		if ctrl != nil {
			_ = ctrl.Close(ctx)
		}
		if meter != nil {
			_ = meter.Close()
		}
		_ = store.Close()
		return nil, nil, err
	}
	// 记账:resume 续号(cli openSession 同序——同会话记账文件不撞回合号)。
	offset := int64(0)
	if h.resume {
		if offset, err = cache.NextTurnNumber(usageRoot, h.ID); err != nil {
			return fail(fmt.Errorf("serve: 记账续号失败: %w", err))
		}
	}
	if meter, err = cache.NewMeter(cache.MeterOptions{
		Root: usageRoot, SessionID: h.ID,
		Pricing:    cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}, // cli openSession 同定价表
		TurnOffset: offset,
	}); err != nil {
		return fail(fmt.Errorf("serve: meter: %w", err))
	}
	// 显式规则集(cli:148-172 同序同口径):deny 清单 → DecisionDeny;allow
	// 清单 → DecisionAllow;预设兜底未命中的工具。解析失败 = 配置损坏 →
	// 拒绝装配(fail-closed,绝不静默丢弃规则)。
	var rules []guard.Rule
	for _, raw := range cfg.Permissions.Deny {
		r, perr := guard.ParseRule(raw)
		if perr != nil {
			return fail(fmt.Errorf("permissions.deny %q: %w", raw, perr))
		}
		r.Effect = guard.DecisionDeny
		rules = append(rules, r)
	}
	for _, raw := range cfg.Permissions.Allow {
		r, perr := guard.ParseRule(raw)
		if perr != nil {
			return fail(fmt.Errorf("permissions.allow %q: %w", raw, perr))
		}
		r.Effect = guard.DecisionAllow
		rules = append(rules, r)
	}
	policy, err := guard.NewPolicy(guard.Preset(cfg.Permissions.Preset), rules)
	if err != nil {
		return fail(err)
	}
	bridge := control.NewUserApprovalBridge() // attended 审批桥(web 会话恒 attended)
	gate, err := guard.NewGate(guard.GateOptions{Policy: policy, Approver: bridge})
	if err != nil {
		return fail(err)
	}
	// 记忆库(cli resolveMemoryStore 同语义;serve 无子会话树——单实例即本
	// 会话):开关关 → 不开库(nil = control 零注入零检索);开 → 打开
	// <workspace>/.mulix/memory。Open 失败 = 记忆已启用而库不可用 → 装配
	// 失败(fail-closed,绝不静默降级)。
	memStore, err := resolveMemoryStore(h.Workspace, cfg)
	if err != nil {
		return fail(err)
	}
	// Options.Memory 只接「真 nil 或真实例」:typed-nil 陷阱(cli:228-234 同款)。
	var memRecall control.MemoryRecall
	if memStore != nil {
		memRecall = memStore
	}
	// 自定义钩子消费者(cli:207-214 同款):声明校验失败 = 配置错误 → 拒绝
	// 装配(fail-closed);空 → 零接线(不构造消费者)。
	var hookConsumers []guard.HookConsumer
	if len(cfg.Hooks.Custom.Rules) > 0 {
		consumer, herr := guard.NewScriptConsumer(cfg.Hooks.Custom.Rules)
		if herr != nil {
			return fail(fmt.Errorf("hooks.custom.rules: %w", herr))
		}
		hookConsumers = append(hookConsumers, consumer)
	}
	// 工具面条件注册(组合根职责,registry.NewServer 冻结前调用;Once 进程内
	// 首次定型):子代理三件套不注册(web MVP 不接线会话工厂,spec 第二期——
	// 语义同 cli selfCommand 失败分支:引擎工具面无 spawn/poll/cancel,委派
	// 于缺席处 fail-closed);记忆三件套按 [tools] memory_search。Once 进程级
	// 定型:多工作区会话共享同一注册面(与 cli 单工作区进程的语义差异,见
	// Task 6 报告)。
	mulixsubagent.RegisterConditional(nil, false)
	mulixmemory.RegisterConditional(nil, h.Workspace, cfg.Tools.MemorySearch)
	// mulix_usage 跨进程权威口径(T26 接线,cli openSession setUsageSource
	// 同语义):ReadUsageReport 含 resume 续号的完整历史。
	mulixusage.SetSource(func() (cache.Report, error) {
		return cache.ReadUsageReport(usageRoot, h.ID)
	})
	// 生产引擎宿主(cli productionHostFactory:383-389 同形态;engine.lock
	// 校验在 Host 层零改动)。测试缝:p.HostFactory 非 nil 时替换(FakeHost)。
	hostFactory := p.HostFactory
	if hostFactory == nil {
		ws := h.Workspace
		hostFactory = func(engine.BootConfig) (engine.Host, error) {
			return engine.NewClaudeHost(engine.ClaudeHostOptions{
				LockPath: resolveEngineLockPath(ws),
			}), nil
		}
	}
	root, opts, kerr := assembleKernelContext(control.Options{
		HostFactory: hostFactory,
		Sink:        h.sink,
		Store:       store,
		Meter:       meter,
		Gate:        gate,
		Approvals:   bridge,
		Config:      cfg,
		Workspace:   h.Workspace,
		Unattended:  false,
		Registry:    newRegistryServer(profileToolFilter(cfg)),
		// 镜像→总线桥经下方 eventbus 门接线(018 FR-102 单真源=cfg.Eventbus)。
		AllowEngineDrift: p.AllowDrift,
		SubagentLimits: control.SubagentLimits{
			MaxDepth:      cfg.Subagent.MaxDepth,
			MaxConcurrent: cfg.Subagent.MaxConcurrent,
		},
		Memory:        memRecall,
		HookConsumers: hookConsumers,
	})
	if kerr != nil {
		return fail(kerr)
	}
	// 镜像→总线桥(cli:261-291 同语义独立实现):门开 → per-workspace 单例
	// 总线(provide "events" + 声明 subject + EventPublisher);桥不可用不阻断
	// 会话(stderr 一行留痕)。门关 → 死残留 endpoint 删除清痕(020 FR-102;
	// 错误忽略——卫生面不阻断)。
	eventbusOn, eberr := cfg.Eventbus.Enabled()
	if eberr != nil {
		return fail(fmt.Errorf("eventbus: gate: %w", eberr))
	}
	if eventbusOn {
		if bus := sharedEventBus(h.Workspace, p.stderr()); bus != nil {
			if err := root.Provide("events", bus); err != nil {
				return fail(err)
			}
			if err := bus.DeclareEvents(mirrorEventKinds()...); err != nil {
				return fail(fmt.Errorf("eventbus: declare: %w", err))
			}
			opts.EventPublisher = bus
		}
	} else {
		_ = os.Remove(filepath.Join(h.Workspace, ".mulix", "bus", "endpoint"))
	}
	// KernelKeys 快照(016 FR-101;cli:292-295 同款——快照取自装配末尾,总线
	// 键注册之后,否则 kernel_inspect.services 确定性缺键)。
	opts.KernelKeys = root.Keys()
	c, err := control.NewController(opts)
	if err != nil {
		return fail(err)
	}
	ctrl = c // 此后 fail 闭包可收口 ctrl(NewSession/Resume 失败路径)
	if h.resume {
		err = ctrl.Resume(ctx, engineID)
	} else {
		err = ctrl.NewSession(ctx)
	}
	if err != nil {
		return fail(fmt.Errorf("serve: %s: %w", map[bool]string{true: "resume", false: "new session"}[h.resume], err))
	}
	// 装配资源移交 host 生命周期:close 在 ctrl.Close(等泵退出)之后级联
	// 收口 meter/store——账面写入者(泵)已停,句柄释放即安全(镜像 close
	// 纪律:仅成功装配移交;失败路径已由 fail 闭包收口)。
	h.assembleRelease = func() {
		_ = meter.Close()
		_ = store.Close()
	}
	return ctrl, bridge, nil
}

// openForResume 打开续链镜像并过 serve 侧恢复门(brief:open turn → 拒绝,
// 先于 ctrl.Resume——裸 Resume 会合成 resume_reconciled 静默改写镜像,web 端
// 必须把中断如实呈现给用户而非代为收口;错误面 = FR-013)。引擎 id 取镜像
// meta:空 = 引擎 init 未到达即退出,无链可续(cli resolveResume 同拒——
// engine.Resume("") 会静默退化为新会话)。工作区不一致拒绝(cli resolveResume
// 同款防线:错工作区续链一律拒绝)。
func openForResume(h *sessionHost, sessionsRoot string) (*sessionx.Store, string, error) {
	store, err := sessionx.OpenStore(sessionx.SessionDir(sessionsRoot, h.ID))
	if err != nil {
		return nil, "", fmt.Errorf("serve: resume: %w", err)
	}
	need, openTurn, err := control.RecoveryRequired(store)
	if err != nil {
		_ = store.Close()
		return nil, "", fmt.Errorf("serve: resume: 恢复检查失败: %w", err)
	}
	if need {
		_ = store.Close()
		return nil, "", fmt.Errorf("serve: resume: 会话 %s 回合 t%d 未收口（上次运行中断），拒绝续链；请先收口该回合或删除该会话镜像", h.ID, openTurn)
	}
	meta, err := store.ReadMeta()
	if err != nil {
		_ = store.Close()
		return nil, "", fmt.Errorf("serve: resume: meta: %w", err)
	}
	id, _ := meta["engine_session_id"].(string)
	if id == "" {
		_ = store.Close()
		return nil, "", fmt.Errorf("serve: resume: 会话 %s 没有可续链的引擎会话 id（引擎 init 未到达即退出？）", h.ID)
	}
	if ws, _ := meta["workspace"].(string); ws != "" && h.Workspace != "" && ws != h.Workspace {
		_ = store.Close()
		return nil, "", fmt.Errorf("serve: resume: 会话 %s 属于工作区 %s（当前 %s）——续链需回到原工作区", h.ID, ws, h.Workspace)
	}
	return store, id, nil
}

// resolveMemoryStore 解析本会话记忆库实例(cli resolveMemoryStore 同语义;serve
// 无子会话树,单实例即本会话):[tools] memory_search=false → (nil, nil)——
// 记忆关闭不开库;Open 失败 = 记忆已启用而库不可用 → 装配失败(fail-closed)。
func resolveMemoryStore(workspace string, cfg config.Config) (*memory.Store, error) {
	if !cfg.Tools.MemorySearch {
		return nil, nil
	}
	st, err := memory.Open(mulixmemory.Dir(workspace))
	if err != nil {
		return nil, fmt.Errorf("memory store: %w", err)
	}
	return st, nil
}

// assembleKernelContext 把装配产出的依赖以 kernel 服务键登记(011 FR-101;
// 互指 cli/session.go:304-356——provide 列表与 agentloop 挂载逐条对齐,独立
// 实现)。可空依赖 nil 跳过——键不就位与「nil=功能关闭」语义一致。Options
// 同源回填(键值=字段):Source = claude 适配器(012 FR-101a 生产路径——适配器
// 即 agentloop.EventSource,泵经 ctx.llm 消费宿主事件流);Kernel = 根 ctx
// (021 FR-102 loop 权威真源——NewController 缺键 fail-closed)。
func assembleKernelContext(o control.Options) (*kernel.Context, control.Options, error) {
	root := kernel.NewContext(nil)
	for _, kv := range []struct {
		key string
		v   any
	}{
		{"sink", o.Sink}, {"store", o.Store}, {"meter", o.Meter},
		{"gate", o.Gate}, {"config", o.Config},
	} {
		if err := root.Provide(kv.key, kv.v); err != nil {
			return nil, control.Options{}, err
		}
	}
	if o.Approvals != nil {
		if err := root.Provide("approvals", o.Approvals); err != nil {
			return nil, control.Options{}, err
		}
	}
	if o.Memory != nil {
		if err := root.Provide("memory", o.Memory); err != nil {
			return nil, control.Options{}, err
		}
	}
	if err := root.Provide("profile", o.Config.Profile.Normalized()); err != nil {
		return nil, control.Options{}, err
	}
	if err := root.Mount(agentloop.NewPlugin()); err != nil {
		return nil, control.Options{}, fmt.Errorf("assemble: %w", err)
	}
	adapter := claude.NewClaudeAdapter()
	if err := root.Provide("llm", adapter); err != nil {
		return nil, control.Options{}, err
	}
	o.Source = adapter
	o.Kernel = root
	return root, o, nil
}

// selfCommand 返回 mcp-config 条目的 mulix 自 spawn 命令(互指
// cli/support.go:26 同语义独立实现;测试内嵌 go test 二进制也可执行——统一走
// os.Executable)。
func selfCommand() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("serve: resolve executable: %w", err)
	}
	if _, err := exec.LookPath(exe); err != nil {
		return nil, fmt.Errorf("serve: executable not runnable: %w", err)
	}
	return []string{exe, "mcp", "serve"}, nil
}

// newRegistryServer 构造内置 MCP 注册面(cli:422-430 同语义独立实现):mulix
// stdio 自 spawn 条目 → Boot mcp-config(权限回路 can_use_tool 的传输载体);
// selfCommand 失败退回无条目注册面——composeBoot 同样不会钉
// --permission-prompt-tool,不产生「指向不存在服务器」的 Boot 面。
func newRegistryServer(toolFilter map[string]bool) *registry.Server {
	selfCmd, err := selfCommand()
	if err != nil {
		return registry.NewServer(registry.Options{Name: "mulix", Version: serveVersion, ToolFilter: toolFilter})
	}
	return registry.NewServer(registry.Options{
		Name: "mulix", Version: serveVersion, SelfCommand: selfCmd, ToolFilter: toolFilter,
	})
}

// profileToolFilter 返回 profile 对应的工具过滤(nil=全部——standard;minimal=
// 仅权限回路必需工具)。014 FR-102;cli 同名函数同语义。
func profileToolFilter(cfg config.Config) map[string]bool {
	if cfg.Profile.Normalized() == config.ProfileMinimal {
		return map[string]bool{"permission": true}
	}
	return nil
}

// resolveEngineLockPath 引擎锁解析链(互指 cli/session.go:391-416:工作区根
// → 可执行文件同目录 → 现行相对路径;命中即止,全缺回退相对路径由 Host 层
// 报缺失保持既有错误形态;校验语义——漂移拒绝/放行/损坏拒绝——零改动)。
// 独立实现——组合根不互依。
func resolveEngineLockPath(workspace string) string {
	candidates := []string{filepath.Join(workspace, "engine.lock")}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "engine.lock"))
	}
	candidates = append(candidates, "engine.lock")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[len(candidates)-1]
}

// 总线单例按工作区键隔离(互指 cli/session.go:779-804 同语义独立实现;
// Windows FileStorage 句柄锁目录——键隔离避免;跨进程同工作区由 OpenBus
// 自身治理)。
var (
	busMu   sync.Mutex
	busByWS = map[string]*eventbus.Bus{}
)

// sharedEventBus 取/开本工作区总线;OpenBus 失败不阻断会话(stderr 一行留痕,
// 返回 nil = 桥关——cli:786-804 同语义)。
func sharedEventBus(workspace string, stderr io.Writer) *eventbus.Bus {
	busMu.Lock()
	defer busMu.Unlock()
	if b, ok := busByWS[workspace]; ok {
		return b
	}
	b, err := eventbus.OpenBus(eventbus.BusOptions{
		StoreDir: filepath.Join(workspace, ".mulix", "bus"),
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "eventbus: 桥关闭(OpenBus 失败): %v\n", err)
		return nil
	}
	busByWS[workspace] = b
	return b
}

// CloseSharedBus 关闭全部工作区总线(进程退出调用;对齐 cli.CloseSharedBus——
// FileStorage 同步发布已持久,无 Close 亦安全,显式收口为卫生面)。组合根
// 不互依故独立实现,serve 进程退出由 cmd 层调用。
func CloseSharedBus() {
	busMu.Lock()
	defer busMu.Unlock()
	for _, b := range busByWS {
		_ = b.Close()
	}
	busByWS = map[string]*eventbus.Bus{}
}

// mirrorEventKinds 返回全部镜像 kind 的总线 subject 清单(cli:273-280 逐字节
// 同表——声明面漂移即消费面缺口)。
func mirrorEventKinds() []string {
	return []string{
		"mirror.turn_started", "mirror.assistant_thinking", "mirror.assistant_text",
		"mirror.tool_use_requested", "mirror.approval_request", "mirror.approval_resolved",
		"mirror.tool_result", "mirror.delegation_started", "mirror.delegation_completed",
		"mirror.delegation_cancelled", "mirror.delegation_failed", "mirror.usage",
		"mirror.turn_done", "mirror.notice", "mirror.engine_exited",
	}
}
