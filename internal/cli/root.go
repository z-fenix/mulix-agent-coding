// Package cli 是 mulix 的命令路由（design §5.2：TUI 与子命令经 control 消费
// 内核；frontends 是唯一可 import control 的层）。
//
// 命令面（P1）：
//   - mulix            默认进入 TUI 会话（Task 28 接线；本任务先到骨架桩）
//   - mulix --resume   续链最近/指定引擎会话
//   - mulix run <词>   无人值守单回合（无审批者，fail-closed）
//   - mulix sessions   会话列表
//   - mulix cache      记账汇总（跨进程权威口径 cache.ReadUsageReport）
//   - mulix doctor     自检（Task 29 实装；本任务为骨架声明）
//   - mulix mcp serve  以 stdio MCP 服务器运行内置工具（Boot 自 spawn 条目）
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"mulix/internal/config"
	"mulix/internal/engine"
)

// Version 是 mulix 版本（--version 输出；MCP implementation 元数据共用）。
const Version = "0.1.0-dev"

// Options 是命令装配参数（测试注入 IO/路径/钩子；生产零值走默认）。
type Options struct {
	Stdin        io.Reader // 测试注入；nil = os.Stdin
	Stdout       io.Writer // 测试注入；nil = os.Stdout
	Stderr       io.Writer // 测试注入；nil = os.Stderr
	Workspace    string    // 工作区（默认 cwd）
	SessionsRoot string    // 会话镜像根（默认 ~/.mulix/sessions）
	UsageRoot    string    // 记账根（默认 ~/.mulix）

	// HostFactory 注入引擎宿主（测试=FakeHost；nil = 生产 ClaudeHost）。
	HostFactory func(boot engine.BootConfig) (engine.Host, error)
	// RunTUI 注入交互前端（Task 28；nil = 骨架桩提示）。
	RunTUI func(ctx context.Context, o Options, resumeID string) error
	// Config 注入生效配置（测试；nil = 分层加载默认）。
	Config *config.Config

	// allowEngineDrift 指向根命令解析出的 flag 值（run 会话构造读取）。
	allowEngineDrift *bool
}

func (o Options) driftAllowed() bool {
	return o.allowEngineDrift != nil && *o.allowEngineDrift
}

func (o Options) stdout() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return io.Discard
}

func (o Options) stderr() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return io.Discard
}

// withDefaults 解析零值字段为生产默认（调用方可逐项覆盖——测试注入点）。
func withDefaults(o Options) Options {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Workspace == "" {
		o.Workspace, _ = os.Getwd()
	}
	home, _ := os.UserHomeDir()
	if o.SessionsRoot == "" {
		o.SessionsRoot = filepath.Join(home, ".mulix", "sessions")
	}
	if o.UsageRoot == "" {
		o.UsageRoot = filepath.Join(home, ".mulix")
	}
	return o
}

// NewRootCommand 装配根命令（幂等；每次调用独立实例）。
func NewRootCommand(opts Options) *cobra.Command {
	opts = withDefaults(opts)
	var resumeID string // --resume [engine-session-id]
	var allowDrift bool

	root := &cobra.Command{
		Use:           "mulix",
		Short:         "mulix — 缓存感知的编码代理 harness",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		RunE: func(cmd *cobra.Command, args []string) error {
			runTUI := opts.RunTUI
			if runTUI == nil {
				runTUI = RunTUI // 生产 TUI（T9.2）；测试注入替身
			}
			return runTUI(context.Background(), opts, resumeID)
		},
	}
	flags := root.PersistentFlags()
	// --resume 是可选值 flag：`mulix --resume`（跟最新镜像会话）与
	// `mulix --resume <engine-id>`（显式续链）两种形态。
	flags.StringVarP(&resumeID, "resume", "r", "", "续链引擎会话（无值=最新镜像会话；值=引擎 session id）")
	flags.Lookup("resume").NoOptDefVal = "@"
	// --allow-engine-drift：engine.lock 版本漂移显式放行（FR-016；放行时引擎
	// 留痕 engine_drift trace——Task 17 行为）。
	flags.BoolVar(&allowDrift, "allow-engine-drift", false, "放行 engine.lock 版本漂移（留痕入会话镜像）")
	opts.allowEngineDrift = &allowDrift

	root.AddCommand(newRunCommand(&opts))
	root.AddCommand(newSessionsCommand(&opts))
	root.AddCommand(newCacheCommand(&opts))
	root.AddCommand(newDoctorCommand(&opts))
	root.AddCommand(newMCPCommand(&opts))
	return root
}

func newRunCommand(opts *Options) *cobra.Command {
	var resumeID string
	cmd := &cobra.Command{
		Use:   "run <文本>",
		Short: "无人值守单回合（无审批者；ask 一律 fail-closed 拒绝）",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := RunPrompt(context.Background(), *opts, strings.Join(args, " "), resumeID)
			if code != 0 {
				return fmt.Errorf("run failed (exit %d)", code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&resumeID, "resume", "", "续链引擎会话 id（镜像里的引擎 session id）")
	return cmd
}

func newSessionsCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "列出会话镜像（新在前）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessions(*opts)
		},
	}
}

func newCacheCommand(opts *Options) *cobra.Command {
	var sessionID string
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "缓存记账汇总（跨进程权威口径）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCache(*opts, sessionID)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "只看指定 mulix 会话（默认汇总全部）")
	return cmd
}

func newDoctorCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "环境自检（Task 29 实装）",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = fmt.Fprintln(opts.stdout(), "doctor: 骨架就位，检查项在 Task 29 落地")
			return nil
		},
	}
}

func newMCPCommand(opts *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP 服务器管理",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "以 stdio MCP 服务器运行 mulix 内置工具（引擎经 mcp-config 自 spawn 直连）",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ServeMCP(context.Background(), *opts)
		},
	})
	return cmd
}
