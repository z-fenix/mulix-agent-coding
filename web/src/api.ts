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

// ---- 每标签页客户端身份（评审 I1：后开者只读跟随）----
//
// 同浏览器的多个标签页共享会话 Cookie，Cookie 值做身份会让后开标签页复用
// 交互权——身份改用 sessionStorage 持久的随机令牌（标签页存续期内稳定）。
// 载体：mutating fetch 一律带 X-Mulix-Client 头；SSE 订阅（EventSource 无法
// 设自定义头）以 ?client= 查询参数携带。同源防护层不变：Cookie + Origin 仍是
// 变更请求的前置门（服务端 mutating 中间件），令牌只决定 claim/release 消费
// 的身份串。

const CLIENT_TOKEN_KEY = 'mulix_client'

function randomTabToken(): string {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') return c.randomUUID()
  // 兜底（非安全上下文/老环境）：可预测令牌只影响并列概率，不影响正确性
  // （服务端 randomToken 同款折中）。
  return `t${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

// sessionClientToken 返回本标签页的身份令牌（首次生成后写 sessionStorage 复用）。
export function sessionClientToken(): string {
  try {
    const cur = sessionStorage.getItem(CLIENT_TOKEN_KEY)
    if (cur) return cur
    const t = randomTabToken()
    sessionStorage.setItem(CLIENT_TOKEN_KEY, t)
    return t
  } catch {
    // sessionStorage 不可用（隐私模式等）：退化为页内随机令牌——身份稳定性
    // 降级（重连可能换身份），不阻塞（服务端按 Cookie 回退仍可用）。
    return randomTabToken()
  }
}

// clientHeaders 变更请求统一携带的身份头。
export function clientHeaders(): Record<string, string> {
  return { 'X-Mulix-Client': sessionClientToken() }
}

// streamURL 是 SSE 订阅地址（?client= 承载每标签页身份）。
export function streamURL(id: string): string {
  return `/api/sessions/${encodeURIComponent(id)}/stream?client=${encodeURIComponent(sessionClientToken())}`
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
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...clientHeaders() },
    body: JSON.stringify(body ?? {}),
  }
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
