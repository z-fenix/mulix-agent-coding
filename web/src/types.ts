// types.ts——与 internal/event JSON 字段逐一对应的 TS 镜像（task-10）。
// 事件载荷逐字段对齐 internal/event/event.go 的 json tag；会话面对齐
// internal/serve/http.go 的 JSON 投影；记账对齐 internal/cache 的 JSON 形态。
// 未知字段容忍（向前兼容引擎升级——Task 11/12 的渲染与审批交互消费同一 union）。

// ---- 事件载荷（15 Kind，event.go 逐一镜像）----

// TurnStartedEvent 标记一个回合的开始。
export interface TurnStartedEv {
  seq: number
  session_id: string
  engine_session_id: string
  pinned_model: string
  unattended: boolean
}

// AssistantTextEvent 是引擎的一段文本输出分片。
export interface AssistantTextEv {
  seq: number
  turn: number
  text: string
}

// AssistantThinkingEvent 是引擎的思考分片（含聚合 token 数）。
export interface AssistantThinkingEv {
  seq: number
  turn: number
  thinking: string
  estimated_tokens: number
}

// ToolUseRequestedEvent 是引擎请求调用工具（审批回路的起点）；input 透传。
export interface ToolUseRequestedEv {
  seq: number
  turn: number
  tool_use_id: string
  tool: string
  input: unknown
}

// ToolResultEvent 是工具执行结果回流。
export interface ToolResultEv {
  seq: number
  turn: number
  tool_use_id: string
  content: string
  is_error: boolean
}

// ApprovalRequestEvent 是 guard 发起的审批请求（timeout 秒；0 = 未配置）。
export interface ApprovalRequestEv {
  seq: number
  turn: number
  tool_use_id: string
  tool: string
  input: unknown
  timeout: number
}

// ApprovalResolvedEvent 是一次审批的裁决结果。
export interface ApprovalResolvedEv {
  seq: number
  tool_use_id: string
  decision: string // allow | deny | timeout
  source: string // policy | session | user | timeout
  resolved_by: string
}

// UsageEvent 是 Meter 记账的对外发布（write_tokens = cache_creation）。
export interface UsageEv {
  seq: number
  turn: number
  uncached_input_tokens: number
  write_tokens: number
  read_tokens: number
  output_tokens: number
  thinking_tokens: number
  cost_usd: number
  hit_rate: number
}

// NoticeEvent 是需要呈现给用户的提示（警告/元信息）。
export interface NoticeEv {
  seq: number
  level: string
  content: string
  is_meta: boolean
}

// TurnDoneEvent 标记一个回合结束。
export interface TurnDoneEv {
  seq: number
  turn: number
  subtype: string
  is_error: boolean
  duration_ms: number
  result_text: string
}

// EngineExitedEvent 标记引擎进程退出。
export interface EngineExitedEv {
  seq: number
  code: number
  reason: string
}

// DelegationStartedEvent 标记一次对子代理的委派开始（父会话镜像记录）。
export interface DelegationStartedEv {
  seq: number
  sub_session_id: string
  task: string
  depth: number
}

// DelegationCompletedEvent 标记一次委派正常完成（含用量与成本）。
export interface DelegationCompletedEv {
  seq: number
  sub_session_id: string
  summary: string
  total_tokens: number
  cost_usd_cents: number
}

// DelegationCancelledEvent 标记一次委派被取消。
export interface DelegationCancelledEv {
  seq: number
  sub_session_id: string
  reason: string
}

// DelegationFailedEvent 标记一次委派失败。
export interface DelegationFailedEv {
  seq: number
  sub_session_id: string
  reason: string
}

// EvMap：kind → 载荷 反查表（WireFrame 判别 union 的基座）。
export interface EvMap {
  turn_started: TurnStartedEv
  assistant_text: AssistantTextEv
  assistant_thinking: AssistantThinkingEv
  tool_use_requested: ToolUseRequestedEv
  tool_result: ToolResultEv
  approval_request: ApprovalRequestEv
  approval_resolved: ApprovalResolvedEv
  usage: UsageEv
  notice: NoticeEv
  turn_done: TurnDoneEv
  engine_exited: EngineExitedEv
  delegation_started: DelegationStartedEv
  delegation_completed: DelegationCompletedEv
  delegation_cancelled: DelegationCancelledEv
  delegation_failed: DelegationFailedEv
}

export type EventKind = keyof EvMap

// WireFrame 是镜像 wire record / SSE 帧的统一形态 {"kind","ev"}。
export type WireFrame = { [K in EventKind]: { kind: K; ev: EvMap[K] } }[EventKind]

// WarningsFrame 是 GET /api/sessions/{id}/events 的 NDJSON 头帧。
export interface WarningsFrame {
  warnings: string[]
}

// SessionEventsPage 是对账结果（warnings + 帧序列）。
export interface SessionEventsPage {
  warnings: string[]
  frames: WireFrame[]
}

// ---- 会话面投影（http.go）----

// sessionEntry（handleList；open=true = 本进程在册真实状态）。
export interface SessionEntry {
  id: string
  workspace: string
  status: string
  last_activity?: string
  open: boolean
}

// handleList 应答（含 ScanSessions 损坏 meta 警告）。
export interface SessionListResult {
  sessions: SessionEntry[]
  warnings: string[]
}

// handleWorkspaces 应答。
export interface WorkspaceListResult {
  workspaces: string[]
}

// handleCreate 应答（201）。
export interface CreateSessionResult {
  id: string
  workspace: string
  status: string
}

// handleOpen 应答（role = interactive | readonly）。
export interface OpenSessionResult {
  id: string
  role: 'interactive' | 'readonly'
}

// handleState 应答的记账部分（cache.Report / cache.TurnUsage JSON 形态）。
export interface UsageReport {
  turns: number
  uncached: number
  write: number
  read: number
  output: number
  session_hit: number
  cost: number
  saving: number
  cold_turns: number
}

export interface TurnUsage {
  input_tokens: number
  cache_write_tokens: number
  cache_read_tokens: number
  output_tokens: number
  thinking_tokens: number
}

// handleState 应答（ready = NOT_READY/READY/FAILED）。
export interface SessionState {
  session_id: string
  engine_session_id: string
  model: string
  cli_version: string
  turn: number
  running: boolean
  ready: string
  dir: string
  meter: UsageReport
  turn_tokens: TurnUsage
}

// 审批裁决取值（handleApproval 的 decision 白名单）。
export type ApprovalDecision = 'allow' | 'allow_session' | 'deny'

// ---- 帧形态守卫（对账/实时流共用的判别器）----

export function isWarningsFrame(x: unknown): x is WarningsFrame {
  return (
    typeof x === 'object' &&
    x !== null &&
    Array.isArray((x as { warnings?: unknown }).warnings) &&
    !('kind' in x)
  )
}

export function isWireFrame(x: unknown): x is WireFrame {
  if (typeof x !== 'object' || x === null) return false
  const o = x as { kind?: unknown; ev?: unknown }
  return typeof o.kind === 'string' && typeof o.ev === 'object' && o.ev !== null
}
