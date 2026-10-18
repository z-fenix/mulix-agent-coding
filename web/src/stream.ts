// stream.ts——会话事件流订阅与对账（task-10）。
//
// openSessionStream(id, handlers)：
//  1. 先 GET /api/sessions/{id}/events 对账：头帧 {"warnings":[...]} →
//     onWarnings；其后 wire record 帧按 (kind,seq) 幂等去重后 → onEvent。
//  2. 对账完成后 EventSource 订阅 /api/sessions/{id}/stream：同一去重集，
//     实时帧不重不漏（server 端心跳/握手注释行不产生 message 事件）。
//  3. onerror → 关闭当前订阅并按指数退避重连（500ms 起、×2、封顶 8s；连接
//     成功即归零）。重连先重新对账再重订阅：断连窗口内的事件由对账补齐，
//     重复帧被既有去重集吸收——无缺口、无重复。
//  4. close() 一次性终止：取消挂起重连定时器、断开 EventSource；后续重连/
//     对账全部短路。
//
// 去重键 = `${kind}:${ev.seq}`；ev 无数值 seq 的事件帧不做去重（直通一次）
// ——当前 15 Kind 均带 seq，此分支是向前兼容的兜底面。
import { fetchSessionEvents, openSession, streamURL } from './api'
import { isWarningsFrame, isWireFrame, type WireFrame } from './types'

export interface SessionStreamHandlers {
  onEvent: (frame: WireFrame) => void
  onWarnings: (warnings: string[]) => void
}

export interface SessionStream {
  close: () => void
}

const BACKOFF_BASE_MS = 500
const BACKOFF_MAX_MS = 8000

export function openSessionStream(id: string, handlers: SessionStreamHandlers): SessionStream {
  let closed = false
  let source: EventSource | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let attempt = 0
  const seen = new Set<string>()

  // deliver 是全链路唯一出口：对账帧与实时帧同门去重。
  const deliver = (frame: WireFrame) => {
    const seq = (frame.ev as { seq?: unknown }).seq
    if (typeof seq !== 'number') {
      handlers.onEvent(frame) // 无 seq：去重豁免（直通一次）
      return
    }
    const key = `${frame.kind}:${seq}`
    if (seen.has(key)) return
    seen.add(key)
    handlers.onEvent(frame)
  }

  const handleData = (raw: string) => {
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      // 实时流出现非 JSON 帧 = 流损坏：断流重连优于静默丢帧（fail-closed）。
      throw new Error('stream frame decode failed')
    }
    if (isWarningsFrame(parsed)) {
      handlers.onWarnings(parsed.warnings)
      return
    }
    if (isWireFrame(parsed)) {
      deliver(parsed)
      return
    }
    // 合法 JSON 但形态未知：与对账路径（fetchSessionEvents 抛 ApiError）同形态
    // ——断流重连优于静默丢帧（fail-closed，不静默）。
    throw new Error('stream frame shape unknown')
  }

  const backoffMs = () => Math.min(BACKOFF_BASE_MS * 2 ** attempt, BACKOFF_MAX_MS)

  const scheduleReconnect = () => {
    if (closed) return
    if (source) {
      source.onopen = source.onmessage = source.onerror = null
      source.close()
      source = null
    }
    if (timer !== null) clearTimeout(timer)
    const delay = backoffMs() // 先算后增：首次重连即 500ms（与「500ms 起」口径一致）
    attempt += 1
    timer = setTimeout(connect, delay)
  }

  const connect = () => {
    if (closed) return
    timer = null
    reconcile()
      .then(() => {
        if (closed) return
        // ?client= 承载每标签页身份（I1：EventSource 无法设自定义头）。
        const es = new EventSource(streamURL(id))
        source = es
        es.onopen = () => {
          attempt = 0 // 连接成功：退避计数归零
          // 重申交互权（评审 C1）：每次流建立（含重连）都重 claim——宽限期内
          // 重连时服务端 claim 同 client 幂等且取消离线计时（FR-009 裁定：
          // 重连即交互端就位；否则宽限到点 CancelAll 会拒绝在审请求）。
          // 失败不阻断流：下一轮重连再试；交互权丢失的可观测面是 mutating
          // 403（App 降级只读横幅）。
          openSession(id).catch(() => {})
        }
        es.onmessage = (m) => {
          try {
            handleData(m.data)
          } catch {
            scheduleReconnect()
          }
        }
        es.onerror = () => scheduleReconnect()
      })
      .catch(() => scheduleReconnect()) // 对账失败（含 404/409）同样走退避重试
  }

  const reconcile = async () => {
    const page = await fetchSessionEvents(id)
    handlers.onWarnings(page.warnings)
    for (const f of page.frames) {
      deliver(f)
    }
  }

  connect()

  return {
    close() {
      if (closed) return
      closed = true
      if (timer !== null) {
        clearTimeout(timer)
        timer = null
      }
      if (source) {
        source.onopen = source.onmessage = source.onerror = null
        source.close()
        source = null
      }
    },
  }
}
