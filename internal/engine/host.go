package engine

import (
	"context"
)

// Host 是 Go 侧对引擎的唯一依赖面（design §7.1）。ClaudeHost 是真实实现；
// FakeHost 是脚本化实现（按 fixture 序列回放事件）——e2e 用「假引擎 × 真实
// control/cache/sessionx 栈」断言系统行为。
type Host interface {
	// Start 拉起引擎子进程并完成初始化握手（system/init 到达）。
	Start(ctx context.Context, cfg BootConfig) error
	// Send 提交一回合输入（stream-json stdin 一行）。
	Send(ctx context.Context, msg UserMessage) error
	// Events 返回引擎事件流（含 usage/tool_use/result；先持久化后发布的
	// 双路分发由 control 负责，本通道只投递）。
	Events() <-chan Event
	// RespondControl 应答引擎的控制请求（审批/钩子/SDK-MCP 信封；逐信封
	// 应答其 request_id——重发携带新 id，见协议文档 §5）。
	RespondControl(ctx context.Context, id string, payload ControlResponse) error
	// Interrupt 取消当前回合（控制协议 interrupt；P1 由 FakeHost 演练、
	// 真实引擎 Task 24 联调）。
	Interrupt(ctx context.Context) error
	// Close 有界关闭（优雅退出 → 超时杀进程树，design §7.4）。
	Close(ctx context.Context) error
	// Info 返回握手后的引擎身份（Start 成功前字段为零值）。
	Info() EngineInfo
}

// EngineInfo 是引擎身份信息（TUI 状态栏/doctor 用）。
type EngineInfo struct {
	CLIVersion  string // init.claude_code_version（engine.lock 校验，协议 §6）
	SessionID   string // 引擎 session id（同进程多回合不变，实测）
	Model       string // init 侧模型名（不透明字符串；与 assistant.message.model 可能不同）
	Fingerprint string // 协议指纹（实现自述：ClaudeHost=lock probe 标识，FakeHost="fake"）
}

// BootConfig 是引擎拉起参数（design §7.2 P1 固定集 + spawn 机制字段）。
// 仅 Start 消费；会话内不变（Boot 冻结面，C1/C2）。
type BootConfig struct {
	// spawn 目标与环境
	EnginePath string   // claude CLI 可执行路径（绝对路径优先）
	Env        []string // 追加环境变量（凭据经环境变量注入，不落日志；§7.4）
	Workspace  string   // 启动目录（cwd）
	// 协议面
	PermissionMode       string   // --permission-mode（必须显式；auto 会绕过审批——实测）
	PermissionPromptTool string   // --permission-prompt-tool（mcp__<server>__<tool>；P1 权限回路）
	SettingSources       []string // --setting-sources（必须含 user 作用域——实测）
	// Boot 产物（cache.BootComposer 唯一生产者，§8.1 C1/C2）
	AppendSystemPrompt string   // --append-system-prompt（只追加不替换）
	MCPConfigJSON      string   // --mcp-config 文件内容（顺序稳定序列化）
	AllowedTools       []string // --allowedTools（显式顺序）
	DisallowedTools    []string // --disallowedTools（显式顺序）
	// 模型与会话
	Model                 string // --model（空 = 引擎默认）
	ResumeEngineSessionID string // --resume <engine-session-id>（续链，实测不换 session id）
	ForkSession           bool   // --fork-session（分叉，实测生成新 id）
	// 治理
	AllowEngineDrift bool // engine.lock 版本不一致时显式放行（FR-016；默认拒绝启动）
}

// UserMessage 是一回合的用户输入（C5：动态内容只走 turn tail，永不进 system
// prefix；注入点唯一化由 control 保证）。
type UserMessage struct {
	Text     string   // 用户输入正文
	TurnTail []string // 追加在尾部的 text 块（记忆召回/运行时状态；每项一块）
}
