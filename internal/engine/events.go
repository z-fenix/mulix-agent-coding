package engine

import (
	"encoding/json"
)

// EventKind 是归一化后的引擎事件类（decoder 按顶层 type[/subtype] 归一；
// 协议文档 §7 自检表）。
type EventKind string

const (
	KindInit             EventKind = "init"              // system/init（按回合重发，session_id 不变）
	KindHookStarted      EventKind = "hook_started"      // system/hook_started
	KindHookResponse     EventKind = "hook_response"     // system/hook_response
	KindPermissionDenied EventKind = "permission_denied" // system/permission_denied（fail-closed ①）
	KindAssistant        EventKind = "assistant"         // assistant（模型输出分片流）
	KindToolResult       EventKind = "tool_result"       // user（工具结果回流）
	KindResult           EventKind = "result"            // result（回合终止；同进程多回合多个）
	KindControlRequest   EventKind = "control_request"   // 控制信封（P1 观察）
	KindNoise            EventKind = "noise"             // informational/status/thinking_tokens（必须容忍）
	KindUnknown          EventKind = "unknown"           // 未知 type；decoder 当前策略：跳过不产出（保留供 P2 透传）
	KindTrace            EventKind = "trace"             // 宿主留痕（engine_drift 等；control 映射为镜像留痕事件）
	KindExit             EventKind = "exit"              // 宿主产出的进程退出事件（graceful|killed|crashed）
)

// Event 是引擎 stdout 事件的解码形态：归一化信封 + 按类载荷指针。
// decoder（Task 16）构造；Event 本体不走 JSON 往返（载荷类型各自对齐协议字段名）。
type Event struct {
	Kind      EventKind
	SessionID string // 几乎所有事件都带
	UUID      string // 事件 id

	Init       *InitInfo             // KindInit
	Assistant  *AssistantMsg         // KindAssistant
	ToolResult *ToolResultMsg        // KindToolResult
	Result     *ResultInfo           // KindResult
	Denied     *PermissionDeniedInfo // KindPermissionDenied
	Hook       *HookInfo             // KindHookStarted/KindHookResponse
	Control    *ControlRequest       // KindControlRequest
	Trace      *TraceInfo            // KindTrace（宿主留痕）
	Exit       *ExitInfo             // KindExit（宿主产出的进程退出）
	// KindNoise 不带结构化载荷（内容按值容忍）；需要时经 Raw 取原始行。

	Raw json.RawMessage `json:"-"` // 原始行（审计/调试；内存持有，不序列化）
}

// TraceInfo 是宿主留痕事件载荷（如 engine_drift 放行——FR-003/SC-005；
// control 映射为镜像留痕事件）。
type TraceInfo struct {
	Code    string          `json:"code"`    // engine_drift 等
	Message string          `json:"message"` // 人读说明
	Details json.RawMessage `json:"details,omitempty"`
}

// ExitInfo 是进程退出事件载荷（reason: graceful|killed|crashed|stdout_decode_error…）。
type ExitInfo struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// MCPServerStatus 是 init.mcp_servers 条目。
type MCPServerStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"` // connected|failed
	Source string `json:"source"` // user|project|local|dynamic|sdk
}

// InitInfo 是 system/init 载荷（按回合重发；Task 16 幂等处理，以首条为准）。
type InitInfo struct {
	CWD            string            `json:"cwd"`
	Tools          []string          `json:"tools"`
	MCPServers     []MCPServerStatus `json:"mcp_servers"`
	Model          string            `json:"model"`
	PermissionMode string            `json:"permissionMode"` // camelCase（协议实证）
	CLIVersion     string            `json:"claude_code_version"`
	// apiKeySource 不建模（§4：认证失败按失败形态判定，不按此单字段）。
	// 其余未列键（slash_commands/skills/…）由未知字段容忍红线处理。
}

// ContentBlock 是 assistant.message.content 分片（text|thinking|tool_use）。
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`        // tool_use
	Name      string          `json:"name,omitempty"`      // tool_use
	Input     json.RawMessage `json:"input,omitempty"`     // tool_use（不透明；guard/工具层解释）
	Signature string          `json:"signature,omitempty"` // thinking
}

// AssistantMsg 是 assistant 事件载荷（同一逻辑消息拆多事件同 id；分片级
// usage 恒 0，忽略——协议 §2.3）。
type AssistantMsg struct {
	ID         string         `json:"id"`
	Model      string         `json:"model"`
	Blocks     []ContentBlock `json:"content"`
	StopReason *string        `json:"stop_reason"`
}

// ToolResultBlock 是 user 事件 content 里的工具结果（is_error 条件字段：
// 成功缺省=false，被拒/出错 true——decoder 不得假设必有，协议 §2.4）。
type ToolResultBlock struct {
	Type      string `json:"type,omitempty"` // tool_result
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolResultMsg 是 user 事件（工具结果回流）。
type ToolResultMsg struct {
	Blocks []ToolResultBlock `json:"content"`
	// tool_use_result 是结构化摘要（成功=对象、出错=纯字符串，按工具而异）；
	// P1 不解释，原样透传（容忍缺失）。
	RawToolUseResult json.RawMessage `json:"tool_use_result,omitempty"`
}

// OutputTokensDetails 是 result.usage 的 output 细目。
type OutputTokensDetails struct {
	ThinkingTokens int64 `json:"thinking_tokens"`
}

// Usage 是 result.usage（本回合口径——Meter（Task 13）数据源；modelUsage/
// total_cost_usd 才是会话累计，勿混淆，协议 §2.5）。
type Usage struct {
	InputTokens      int64               `json:"input_tokens"`
	OutputTokens     int64               `json:"output_tokens"`
	CacheWriteTokens int64               `json:"cache_creation_input_tokens"`
	CacheReadTokens  int64               `json:"cache_read_input_tokens"`
	Details          OutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// PermissionDenial 是 result.permission_denials 条目（fail-closed 路径②：
// 被拒工具全记录，含 tool_input）。
type PermissionDenial struct {
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
}

// ResultInfo 是 result 事件载荷（回合终止；同进程多回合 = 多个 result）。
type ResultInfo struct {
	SubType    string `json:"subtype"` // success|error…|rejected
	IsError    bool   `json:"is_error"`
	NumTurns   int64  `json:"num_turns"`
	ResultText string `json:"result"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Usage      Usage  `json:"usage"`
	// 会话累计口径：指针区分缺失与 0（Task 13 成本只取末条，不求和）
	TotalCostUSD *float64           `json:"total_cost_usd,omitempty"`
	Denials      []PermissionDenial `json:"permission_denials,omitempty"`
	ResultIndex  *int64             `json:"result_index,omitempty"` // 同进程第几个回合（0 起）
}

// PermissionDeniedInfo 是 system/permission_denied 载荷（fail-closed 路径①，
// 即时；与 result.permission_denials[] 汇总双路径并存）。
type PermissionDeniedInfo struct {
	ToolName  string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
	Message   string `json:"message"`
}

// HookInfo 是 system/hook_started|hook_response 载荷（协议 §2.2）。
type HookInfo struct {
	HookID    string `json:"hook_id"`
	HookName  string `json:"hook_name"`
	HookEvent string `json:"hook_event"` // SessionStart|PreToolUse|PostToolUse|Stop
	// hook_response 附加字段（output/stdout 是 JSON 字符串，注意二次解码属 P2）
	Output   string `json:"output,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
}

// ControlRequest 是引擎→宿主的控制信封（协议 §5 观察事实；P1 不依赖——
// 权限回路走 --permission-prompt-tool 的 MCP tools/call，不经此信封）。
// 未应答时同一逻辑请求重发且携带新 request_id：宿主必须逐信封应答其 id。
type ControlRequest struct {
	RequestID string             `json:"request_id"`
	Request   ControlRequestBody `json:"request"`
}

// ControlRequestBody 是信封内 request 对象（subtype 嵌套在信封内，非顶层）。
type ControlRequestBody struct {
	SubType    string          `json:"subtype"` // mcp_message 等
	ServerName string          `json:"server_name,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"` // MCP 消息对象（P1 透传不解释）
}

// ControlResponse 是宿主→引擎的控制应答载荷（stdin 信封内嵌；由 Host 实现
// 组包 {"type":"control_response","response":{…}}，Task 17/18）。
type ControlResponse struct {
	SubType   string          `json:"subtype"` // success|error
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response,omitempty"` // MCP result 等（按信封原样）
}
