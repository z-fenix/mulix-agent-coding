// Package event 定义 mulix 的类型化事件：Kind 常量与可 JSON 往返的结构体，
// 供 sessionx（持久化）、control（编排）与 cli（呈现）共用。
// 红线：所有字段必须 JSON 往返稳定；未知字段解码必须容忍（向前兼容引擎升级）。
package event

import "encoding/json"

// Kind 是事件的稳定类型标识（JSON 中的 kind 字段值）。
type Kind string

const (
	TurnStarted       Kind = "turn_started"
	AssistantText     Kind = "assistant_text"
	AssistantThinking Kind = "assistant_thinking"
	ToolUseRequested  Kind = "tool_use_requested"
	ToolResult        Kind = "tool_result"
	ApprovalRequest   Kind = "approval_request"
	ApprovalResolved  Kind = "approval_resolved"
	Usage             Kind = "usage"
	Notice            Kind = "notice"
	TurnDone          Kind = "turn_done"
	EngineExited      Kind = "engine_exited"
)

// TurnStartedEvent 标记一个回合的开始。
type TurnStartedEvent struct {
	Seq             int64  `json:"seq"`
	SessionID       string `json:"session_id"`
	EngineSessionID string `json:"engine_session_id"`
	PinnedModel     string `json:"pinned_model"`
	Unattended      bool   `json:"unattended"`
}

// AssistantTextEvent 是引擎的一段文本输出分片。
type AssistantTextEvent struct {
	Seq  int64  `json:"seq"`
	Turn int64  `json:"turn"`
	Text string `json:"text"`
}

// AssistantThinkingEvent 是引擎的思考分片（含聚合 token 数）。
type AssistantThinkingEvent struct {
	Seq             int64  `json:"seq"`
	Turn            int64  `json:"turn"`
	Thinking        string `json:"thinking"`
	EstimatedTokens int64  `json:"estimated_tokens"`
}

// ToolUseRequestedEvent 是引擎请求调用工具（审批回路的起点）。
type ToolUseRequestedEvent struct {
	Seq       int64           `json:"seq"`
	Turn      int64           `json:"turn"`
	ToolUseID string          `json:"tool_use_id"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input"`
}

// ToolResultEvent 是工具执行结果回流。
type ToolResultEvent struct {
	Seq       int64  `json:"seq"`
	Turn      int64  `json:"turn"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

// ApprovalRequestEvent 是 guard 发起的审批请求（120s 超时 fail-closed）。
type ApprovalRequestEvent struct {
	Seq       int64           `json:"seq"`
	Turn      int64           `json:"turn"`
	ToolUseID string          `json:"tool_use_id"`
	Tool      string          `json:"tool"`
	Input     json.RawMessage `json:"input"`
	Timeout   int             `json:"timeout"` // 秒；0 = 未配置（消费方套用默认 120s）
}

// ApprovalResolvedEvent 是一次审批的裁决结果。
type ApprovalResolvedEvent struct {
	Seq        int64  `json:"seq"`
	ToolUseID  string `json:"tool_use_id"`
	Decision   string `json:"decision"` // allow | deny | timeout
	Source     string `json:"source"`   // policy | session | user | timeout
	ResolvedBy string `json:"resolved_by"`
}

// UsageEvent 是 Meter 记账的对外发布（缓存三层可观测性）。
// 口径（Task 13 钉死实现）：WriteTokens = cache_creation（缓存写入），与 OutputTokens
// 相互独立；HitRate = ReadTokens/(ReadTokens+UncachedInputTokens)。
type UsageEvent struct {
	Seq                 int64   `json:"seq"`
	Turn                int64   `json:"turn"`
	UncachedInputTokens int64   `json:"uncached_input_tokens"`
	WriteTokens         int64   `json:"write_tokens"` // cache_creation（缓存写入），非 output 别名
	ReadTokens          int64   `json:"read_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	ThinkingTokens      int64   `json:"thinking_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	HitRate             float64 `json:"hit_rate"`
}

// NoticeEvent 是需要呈现给用户的提示（警告/元信息）。
type NoticeEvent struct {
	Seq     int64  `json:"seq"`
	Level   string `json:"level"`
	Content string `json:"content"`
	IsMeta  bool   `json:"is_meta"`
}

// TurnDoneEvent 标记一个回合结束。
type TurnDoneEvent struct {
	Seq        int64  `json:"seq"`
	Turn       int64  `json:"turn"`
	Subtype    string `json:"subtype"`
	IsError    bool   `json:"is_error"`
	DurationMS int64  `json:"duration_ms"`
	ResultText string `json:"result_text"`
}

// EngineExitedEvent 标记引擎进程退出。
type EngineExitedEvent struct {
	Seq    int64  `json:"seq"`
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}
