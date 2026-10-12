package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"mulix/internal/cache"
	"mulix/internal/config"
	"mulix/internal/event"
	"mulix/internal/registry"
	"mulix/internal/sessionx"
	"mulix/internal/tools/mulixmemory"
	"mulix/internal/tools/mulixsubagent"
)

// lineSink 是无人值守/骨架期的行式事件出口（TUI 是 attended 的正装出口——
// Task 28）。只打印对人类有意义的行：助手文本、通知、审批提示；并跟踪失败
// （IsError 收口/引擎退出）供 RunPrompt 决定退出码——无人值守/CI 消费者必须
// 能区分回合失败与成功（T27 评审 Important-2）。
type lineSink struct {
	w io.Writer

	mu     sync.Mutex
	failed bool
}

func (s *lineSink) Publish(ev any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e := ev.(type) {
	case event.AssistantTextEvent:
		_, _ = fmt.Fprintln(s.w, e.Text)
	case event.NoticeEvent:
		_, _ = fmt.Fprintf(s.w, "[notice] %s\n", e.Content)
	case event.ApprovalRequestEvent:
		_, _ = fmt.Fprintf(s.w, "[审批] 工具 %s 需要授权（无人值守将 fail-closed 拒绝）\n", e.Tool)
	case event.TurnDoneEvent:
		if e.IsError {
			s.failed = true
			_, _ = fmt.Fprintf(s.w, "[回合失败] %s\n", e.Subtype)
		}
	case event.EngineExitedEvent:
		s.failed = true
		_, _ = fmt.Fprintf(s.w, "[引擎已退出] %s\n", e.Reason)
	}
}

// failedTurn 报告是否观察到失败收口（IsError 回合/引擎退出）。
func (s *lineSink) failedTurn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// runSessions 实现 `mulix sessions`：镜像列表（新在前）。
func runSessions(o Options) error {
	sessions, warnings, err := sessionx.ScanSessions(o.SessionsRoot)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintln(o.stderr(), "警告:", w)
	}
	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(o.stdout(), "（没有会话镜像）")
		return nil
	}
	_, _ = fmt.Fprintf(o.stdout(), "%-34s  %-20s  %-8s  %s\n", "SESSION", "CREATED", "ENGINE", "WORKSPACE")
	for _, s := range sessions {
		engine := s.EngineID
		if len(engine) > 8 {
			engine = engine[:8] + "…"
		}
		if engine == "" {
			engine = "—"
		}
		_, _ = fmt.Fprintf(o.stdout(), "%-34s  %-20s  %-8s  %s\n", s.ID, s.CreatedAt, engine, s.Workspace)
	}
	return nil
}

// runCache 实现 `mulix cache`：记账汇总（跨进程权威口径——T26 对账面）。
func runCache(o Options, sessionID string) error {
	if sessionID != "" {
		rep, err := cache.ReadUsageReport(o.UsageRoot, sessionID)
		if err != nil {
			return err
		}
		printReport(o.stdout(), sessionID, rep)
		return nil
	}
	sessions, _, err := sessionx.ScanSessions(o.SessionsRoot)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(o.stdout(), "（没有会话记账）")
		return nil
	}
	for _, s := range sessions {
		rep, err := cache.ReadUsageReport(o.UsageRoot, s.ID)
		if err != nil {
			return err
		}
		printReport(o.stdout(), s.ID, rep)
	}
	return nil
}

func printReport(w io.Writer, sessionID string, rep cache.Report) {
	_, _ = fmt.Fprintf(w, "%s: 回合 %d | 命中率 %.1f%% | 输入(未命中/写入/读取) %d/%d/%d | 输出 %d | 成本 $%.4f | 节省 $%.4f | 冷回合 %d\n",
		sessionID, rep.Turns, rep.SessionHit*100, rep.Uncached, rep.Write, rep.Read, rep.Output, rep.Cost, rep.Saving, rep.ColdTurns)
}

// registerServeTools 是 serve 进程的组合根注册（评审修复轮 1 F1：注册面冻结
// 前调用——前台 assembleController 与 serve 进程各自为政，进程内恰好一次）：
//   - 子代理三件套：MULIX_SUBAGENT_CALLBACK 已注入 = control 已接线回调桥
//     （Boot Env 通道随引擎进程树到达 serve 子进程）→ 注册；缺席 = fail-closed，
//     工具不进引擎工具面（与工具自身 Execute 的 fail-closed 矩阵同向）；
//   - 记忆三件套：[tools] memory_search=true → 注册（workspace = 进程 cwd，
//     cwd 语义；同源分层配置与前台判定一致）。
func registerServeTools(o Options, cfg config.Config) {
	mulixsubagent.RegisterConditional(nil, os.Getenv("MULIX_SUBAGENT_CALLBACK") != "")
	mulixmemory.RegisterConditional(nil, o.Workspace, cfg.Tools.MemorySearch)
}

// ServeMCP 实现 `mulix mcp serve`：以 stdio 传输运行内置工具服务器
// （Boot 自 spawn 条目的对端——引擎经此直连 mulix 工具）。注册表在进程
// 启动时定型：无条件工具经 init()（main 的空白导入触发）；条件工具经
// registerServeTools 在此组合根注册后、NewServer 冻结（serve 进程不设
// SelfCommand——自引用嵌套）。
func ServeMCP(ctx context.Context, o Options) error {
	serveCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := effectiveConfig(o)
	if err != nil {
		return fmt.Errorf("mcp serve: load config: %w", err)
	}
	registerServeTools(o, cfg)
	// profile ToolFilter(014 FR-102):minimal 时 NewServer 过滤——
	// serve 子进程是引擎的工具可见面(Compose 的 mcp-config 自 spawn 指向此处)。
	srv := registry.NewServer(registry.Options{
		Name:       "mulix",
		Version:    Version,
		ToolFilter: profileToolFilter(cfg),
	})
	return srv.ServeStdio(serveCtx)
}
