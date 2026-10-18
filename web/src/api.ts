// api.ts——HTTP 客户端（task-10）：credentials:'same-origin' 统一带上会话
// Cookie；非 2xx 按状态映射为类型化 ApiError 并携带服务端 message（http.go
// writeError 的错误字段名是 `error`，形如 {"error":"..."}；open 的锁冲突在
// error 之外附 pid，此处只取 error）。GET /events 是 NDJSON（头帧 warnings，
// 其后 wire record 帧），非 JSON——单独按行解析分类。
import {
  isWarningsFrame,
  isWireFrame,
  type ApprovalDecision,
  type CreateSessionResult,
  type OpenSessionResult,
  type SessionEventsPage,
  type SessionListResult,
  type SessionState,
  type WireFrame,
  type WorkspaceListResult,
} from './types'

// ApiError 携带 HTTP 状态与（解析到的）服务端 message；status 0 = 网络层失败。
export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

// errMessage 把任意抛出物压成可呈现字符串（App 错误条消费）。
export function errMessage(e: unknown): string {
  if (e instanceof Error) return e.message
  return String(e)
}

async function fetchOrThrow(url: string, init?: RequestInit): Promise<Response> {
  let res: Response
  try {
    res = await fetch(url, { credentials: 'same-origin', ...init })
  } catch (e) {
    throw new ApiError(0, `网络错误: ${e instanceof Error ? e.message : String(e)}`)
  }
  if (!res.ok) {
    throw await apiErrorOf(res)
  }
  return res
}

// apiErrorOf 解析非 2xx 错误体：{"error": "..."} 取 message；非 JSON 体保留
// 状态行（不吞——错误信息如实透出）。
async function apiErrorOf(res: Response): Promise<ApiError> {
  let msg = `${res.status} ${res.statusText || ''}`.trim()
  try {
    const body: unknown = await res.json()
    if (body && typeof body === 'object' && typeof (body as { error?: unknown }).error === 'string') {
      msg = (body as { error: string }).error
    }
  } catch {
    /* 非 JSON 错误体：保留状态行 */
  }
  return new ApiError(res.status, msg)
}

function postJson(body: unknown): RequestInit {
  return { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body ?? {}) }
}

async function requestJson<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetchOrThrow(url, init)
  return (await res.json()) as T
}

// ---- 工作区与列表 ----

export function getWorkspaces(): Promise<string[]> {
  return requestJson<WorkspaceListResult>('/api/workspaces').then((r) => r.workspaces)
}

export function listSessions(): Promise<SessionListResult> {
  return requestJson<SessionListResult>('/api/sessions')
}

// ---- 创建/打开/状态 ----

export function createSession(workspace: string): Promise<CreateSessionResult> {
  return requestJson<CreateSessionResult>('/api/sessions', postJson({ workspace }))
}

export function openSession(id: string, takeover = false): Promise<OpenSessionResult> {
  const url = `/api/sessions/${encodeURIComponent(id)}/open`
  return requestJson<OpenSessionResult>(url, postJson({ takeover }))
}

export function getSessionState(id: string): Promise<SessionState> {
  return requestJson<SessionState>(`/api/sessions/${encodeURIComponent(id)}/state`)
}

// ---- 回放对账（NDJSON：头帧 {"warnings":[...]}，其后 {"kind","ev"}）----

export async function fetchSessionEvents(id: string): Promise<SessionEventsPage> {
  const res = await fetchOrThrow(`/api/sessions/${encodeURIComponent(id)}/events`)
  const text = await res.text()
  const warnings: string[] = []
  const frames: WireFrame[] = []
  for (const line of text.split('\n')) {
    const t = line.trim()
    if (t === '') continue
    let parsed: unknown
    try {
      parsed = JSON.parse(t)
    } catch {
      // 后端已把损坏段隔离为 warnings，此处好行再解码失败属流损坏——显形不静默。
      throw new ApiError(0, `回放帧解码失败: ${t.slice(0, 80)}`)
    }
    if (isWarningsFrame(parsed)) {
      warnings.push(...parsed.warnings)
    } else if (isWireFrame(parsed)) {
      frames.push(parsed)
    } else {
      throw new ApiError(0, `回放帧形态未知: ${t.slice(0, 80)}`)
    }
  }
  return { warnings, frames }
}

// ---- 交互（submit 202 {turn}；cancel/approval 200 {ok:true}）----

export function submitText(id: string, text: string): Promise<{ turn: number }> {
  const url = `/api/sessions/${encodeURIComponent(id)}/submit`
  return requestJson<{ turn: number }>(url, postJson({ text }))
}

export function cancelTurn(id: string): Promise<{ ok: boolean }> {
  const url = `/api/sessions/${encodeURIComponent(id)}/cancel`
  return requestJson<{ ok: boolean }>(url, postJson({}))
}

export function sendApproval(id: string, toolUseId: string, decision: ApprovalDecision): Promise<{ ok: boolean }> {
  const url = `/api/sessions/${encodeURIComponent(id)}/approval`
  return requestJson<{ ok: boolean }>(url, postJson({ tool_use_id: toolUseId, decision }))
}
