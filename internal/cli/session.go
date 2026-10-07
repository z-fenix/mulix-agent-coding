package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/control"
	"mulix/internal/engine"
	"mulix/internal/guard"
	"mulix/internal/registry"
	"mulix/internal/sessionx"
)

// errOpenTurn 是恢复引导错误（外层可识别后转退出码）。
var errOpenTurn = errors.New("会话存在未收口回合")

// resolveResume 解析 --resume 目标（引擎 session id → 镜像会话目录）：
//   - 显式值：sessionx.FindByEngineID（空值不会命中——防静默错配）；
//   - 无值（"@"/空）：本 workspace 最新会话的镜像引擎 id。
//
// 返回 (引擎 id, 会话目录, error)。空引擎 id 一律拒绝：engine.Resume("")
// 会静默退化为新会话（claudehost 对空 id 跳过 --resume，实测陷阱）。
func resolveResume(requested string, o Options) (string, string, error) {
	root := o.SessionsRoot
	if requested != "" && requested != "@" {
		info, found, err := sessionx.FindByEngineID(root, requested)
		if err != nil {
			return "", "", err
		}
		if !found {
			return "", "", fmt.Errorf("没有镜像会话记录引擎会话 %q（新会话请直接 mulix）", requested)
		}
		if o.Workspace != "" && info.Workspace != o.Workspace {
			return "", "", fmt.Errorf("引擎会话 %q 属于工作区 %s（当前 %s）——续链需回到原工作区", requested, info.Workspace, o.Workspace)
		}
		return requested, sessionx.SessionDir(root, info.ID), nil
	}
	sessions, _, err := sessionx.ScanSessions(root)
	if err != nil {
		return "", "", err
	}
	sawWithoutID := ""
	for _, info := range sessions { // 新在前（ScanSessions 排序）
		if o.Workspace != "" && info.Workspace != o.Workspace {
			continue
		}
		if info.EngineID == "" {
			// 引擎 init 未到即退出的会话没有链可续——跳过继续找更老的
			//（与最新会话无关的旧链仍可续）。
			if sawWithoutID == "" {
				sawWithoutID = info.ID
			}
			continue
		}
		return info.EngineID, sessionx.SessionDir(root, info.ID), nil
	}
	if sawWithoutID != "" {
		return "", "", fmt.Errorf("会话 %s 没有可续链的引擎会话 id（引擎 init 未到达即退出？）；直接 mulix 新开会话", sawWithoutID)
	}
	return "", "", fmt.Errorf("当前工作区没有会话镜像（先 mulix 开新会话）")
}

// checkRecovery 恢复门（design §10：startup = OpenStore → RecoveryRequired →
// 指引 mulix --resume）：本 workspace **最新有效会话**存在未收口回合时拒绝
// 新会话（fail-closed）并指引续链。更老的镜像不检查（陈旧孤儿回合不阻塞
// 当前；恢复门是「上次运行中断」的判定，不是全库审计）。
func checkRecovery(o Options) error {
	sessions, _, err := sessionx.ScanSessions(o.SessionsRoot)
	if err != nil {
		return err
	}
	for _, info := range sessions { // 新在前——只看最新有 open turn 的
		if o.Workspace != "" && info.Workspace != o.Workspace {
			continue
		}
		store, err := sessionx.OpenStore(sessionx.SessionDir(o.SessionsRoot, info.ID))
		if err != nil {
			continue // 打不开的镜像不是恢复对象（诊断面不阻塞）
		}
		need, openTurn, err := control.RecoveryRequired(store)
		_ = store.Close()
		if err != nil {
			continue
		}
		if need {
			return fmt.Errorf("%w：会话 %s 回合 t%d 未收口（上次运行中断）。请 `mulix --resume` 续链，或删除该会话镜像", errOpenTurn, info.ID, openTurn)
		}
		return nil // 最新有效会话无 open turn 即放行
	}
	return nil
}

// effectiveConfig 返回生效配置（注入优先；否则分层加载——缺失文件用默认值）。
func effectiveConfig(o Options) (config.Config, error) {
	if o.Config != nil {
		return *o.Config, nil
	}
	paths := config.Paths{}
	if p, err := config.DefaultPaths(); err == nil {
		paths = p
	}
	cfg, _, err := config.NewLoader().Load(config.Flags{}, paths)
	if err != nil {
		return config.Config{}, err
	}
	return *cfg, nil
}

// buildComponents 组装会话组件（attended 与 unattended 共用；差异由参数表达）。
// 接线序（T22 评审定式）：bridge → Gate{Approver} → Options{Gate, Approvals}。
func buildComponents(o Options, store *sessionx.Store, meter *cache.Meter, sink control.Sink, attended bool) (*control.Controller, *control.UserApprovalBridge, error) {
	cfg, err := effectiveConfig(o)
	if err != nil {
		return nil, nil, err
	}
	// 显式规则集（终评冒烟发现：此前恒为 nil——config 的 allow/deny 清单
	// 从未生效，无人值守下写操作全被 fail-closed 拒绝，引擎空转 deny 循环）。
	// 口径：deny 清单 → DecisionDeny；allow 清单 → DecisionAllow；预设兜底
	// 未命中的工具（default=Ask → 无人值守 fail-closed）。解析失败 = 配置
	// 损坏 → 拒绝启动（fail-closed，绝不静默丢弃规则）。
	var rules []guard.Rule
	for _, raw := range cfg.Permissions.Deny {
		r, perr := guard.ParseRule(raw)
		if perr != nil {
			return nil, nil, fmt.Errorf("permissions.deny %q: %w", raw, perr)
		}
		r.Effect = guard.DecisionDeny
		rules = append(rules, r)
	}
	for _, raw := range cfg.Permissions.Allow {
		r, perr := guard.ParseRule(raw)
		if perr != nil {
			return nil, nil, fmt.Errorf("permissions.allow %q: %w", raw, perr)
		}
		r.Effect = guard.DecisionAllow
		rules = append(rules, r)
	}
	policy, err := guard.NewPolicy(guard.Preset(cfg.Permissions.Preset), rules)
	if err != nil {
		return nil, nil, err
	}
	gateOpts := guard.GateOptions{Policy: policy}
	var bridge *control.UserApprovalBridge
	if attended {
		bridge = control.NewUserApprovalBridge()
		gateOpts.Approver = bridge
	}
	gate, err := guard.NewGate(gateOpts)
	if err != nil {
		return nil, nil, err
	}
	hostFactory := o.HostFactory
	if hostFactory == nil {
		hostFactory = productionHostFactory(o)
	}
	c, err := control.NewController(control.Options{
		HostFactory:      hostFactory,
		Sink:             sink,
		Store:            store,
		Meter:            meter,
		Gate:             gate,
		Approvals:        bridge,
		Config:           cfg,
		Workspace:        o.Workspace,
		Unattended:       !attended,
		Registry:         newRegistryServer(),
		AllowEngineDrift: o.driftAllowed(),
	})
	if err != nil {
		return nil, nil, err
	}
	return c, bridge, nil
}

// productionHostFactory 生产引擎宿主（engine.lock 校验在 Host 层——Task 17）。
func productionHostFactory(o Options) func(engine.BootConfig) (engine.Host, error) {
	return func(boot engine.BootConfig) (engine.Host, error) {
		return engine.NewClaudeHost(engine.ClaudeHostOptions{
			LockPath: "engine.lock",
		}), nil
	}
}

// newRegistryServer 构造内置 MCP 注册面（mulix stdio 自 spawn 条目 → Boot
// mcp-config；工具经 init() 自注册，T26）。无可执行路径时退回无条目注册面
// ——此时 composeBoot 同样不会设置 prompt tool（两开关一致，不产生「指向
// 不存在服务器」的 Boot 面）。
func newRegistryServer() *registry.Server {
	selfCmd, err := selfCommand()
	if err != nil {
		return registry.NewServer(registry.Options{Name: "mulix", Version: Version})
	}
	return registry.NewServer(registry.Options{
		Name: "mulix", Version: Version, SelfCommand: selfCmd,
	})
}

// sessionHandle 是一次会话的组件集合（TUI /new /resume 换绑与 RunPrompt
// 共用装配路径——单一入口，防止接线分叉）。
type sessionHandle struct {
	ctrl   *control.Controller
	bridge *control.UserApprovalBridge // attended 审批桥（可 nil）
	store  *sessionx.Store
	meter  *cache.Meter
	id     string // mulix 会话 id
}

// close 有序收口（幂等安全：各组件自身幂等）。
func (h *sessionHandle) close() {
	if h == nil {
		return
	}
	if h.ctrl != nil {
		_ = h.ctrl.Close(context.Background())
	}
	if h.meter != nil {
		_ = h.meter.Close()
	}
	if h.store != nil {
		_ = h.store.Close()
	}
}

// openSession 装配一次会话（attended 与 unattended 共用）：
// resumeID 非空 → 解析镜像引擎 id → 开既有镜像（记账续号）+ Resume；
// 否则 → 新镜像 + 新会话。返回 (handle, 退出码, 错误)——code != 0 时 handle
// 为 nil（错误已由调用方决定如何呈现）。
func openSession(o Options, resumeID string, attended bool, sink control.Sink) (*sessionHandle, int, error) {
	o = withDefaults(o)
	if resumeID == "" {
		if err := checkRecovery(o); err != nil {
			return nil, 3, err
		}
	}
	var store *sessionx.Store
	var engineID string
	var err error
	if resumeID != "" {
		var dir string
		engineID, dir, err = resolveResume(resumeID, o)
		if err != nil {
			return nil, 2, err
		}
		if store, err = sessionx.OpenStore(dir); err != nil {
			return nil, 2, err
		}
	} else {
		if store, err = sessionx.Create(o.SessionsRoot, sessionx.Options{
			ID: newSessionID(), Workspace: o.Workspace, Unattended: !attended,
		}); err != nil {
			return nil, 2, err
		}
	}
	h := &sessionHandle{store: store, id: storeID(store)}
	// fail 收口半成品组件（T28 评审 Important-2：h 构造后任何失败路径都必须
	// 释放已开资源——meter 的 usage 文件句柄、镜像 store、control 宿主（含
	// mcp-config 临时文件清理），否则泄漏且 Windows 上句柄占用会卡 TempDir 清理。
	fail := func(code int, err error) (*sessionHandle, int, error) {
		if h.ctrl != nil {
			_ = h.ctrl.Close(context.Background())
		}
		if h.meter != nil {
			_ = h.meter.Close()
		}
		_ = h.store.Close()
		return nil, code, err
	}
	// 记账：resume 续号（T26 评审修复——同会话记账文件不撞回合号）
	offset := int64(0)
	if engineID != "" {
		if offset, err = cache.NextTurnNumber(o.UsageRoot, h.id); err != nil {
			return fail(2, fmt.Errorf("记账续号失败: %w", err))
		}
	}
	if h.meter, err = cache.NewMeter(cache.MeterOptions{
		Root: o.UsageRoot, SessionID: h.id,
		Pricing:    cache.Pricing{InputPerMTok: 3, OutputPerMTok: 15}, // TODO(P2): 配置化定价表
		TurnOffset: offset,
	}); err != nil {
		return fail(2, err)
	}
	ctrl, bridge, err := buildComponents(o, store, h.meter, sink, attended)
	if err != nil {
		return fail(2, err)
	}
	h.ctrl, h.bridge = ctrl, bridge
	// T26 接线：mulix_usage 的跨进程权威口径
	setUsageSource(o.UsageRoot, h.id)
	if engineID != "" {
		if err := ctrl.Resume(context.Background(), engineID); err != nil {
			return fail(2, fmt.Errorf("resume: %w", err))
		}
	} else {
		if err := ctrl.NewSession(context.Background()); err != nil {
			return fail(2, fmt.Errorf("new session: %w", err))
		}
	}
	return h, 0, nil
}

// RunPrompt 无人值守单回合（`mulix run`）：策略 deny 全自动，ask fail-closed；
// 助手文本经 lineSink 到 stdout；回合收口即退出。resumeID 非空时续链。
// 返回进程退出码（0 = 成功）。
func RunPrompt(ctx context.Context, o Options, text, resumeID string) int {
	o = withDefaults(o)
	sink := &lineSink{w: o.stdout()}
	h, code, err := openSession(o, resumeID, false, sink)
	if err != nil {
		_, _ = fmt.Fprintln(o.stderr(), "mulix:", err)
		return code
	}
	defer h.close()
	if err := h.ctrl.Submit(ctx, text); err != nil {
		_, _ = fmt.Fprintln(o.stderr(), "mulix: submit:", err)
		return 1
	}
	// 回合收口由 Submit 内部等待（pump 收口 turn_done）；Submit 返回即完成。
	// 失败判定（T27 评审 Important-2）：IsError 收口/引擎退出 → 退出码 1——
	// 无人值守/CI 消费者必须能区分回合失败与成功。
	if sink.failedTurn() {
		return 1
	}
	return 0
}

func storeID(s *sessionx.Store) string {
	dir := s.Dir()
	if i := strings.LastIndexAny(dir, `/\`); i >= 0 {
		return dir[i+1:]
	}
	return dir
}
