// stream.ts——会话事件流订阅与对账（task-10；终审修复轮 I2/C1/I1）。
//
// openSessionStream(id, handlers)：
//  1. 先 EventSource 订阅 /api/sessions/{id}/stream?client=<每标签页令牌>
//     （评审 I2 订阅先行：服务端在握手行之前 Subscribe——onopen 即「订阅已
//     就绪」，此刻起任何发布都进订阅者缓冲通道，不存在「对账↔订阅」间的
//     丢失窗；两路重叠帧由同一去重集吸收）。onopen 同时重申交互权（评审
//     C1：宽限期内重连重 claim 取消服务端离线计时）。
//  2. 流开后再 GET /api/sessions/{id}/events 对账：头帧 {"warnings":[...]} →
//     onWarnings；其后 wire record 帧按 (kind,seq) 幂等去重后 → onEvent——
//     补齐历史；对流开之后已实时送达的帧去重，不重不漏。
//  3. onerror → 关闭当前订阅并按指数退避重连（500ms 起、×2、封顶 8s；连接
//     成功即归零）。重连同主序：先订阅、open 后重对账——断连窗口内的事件
//     由对账补齐，重复帧被既有去重集吸收——无缺口、无重复。
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
    // 订阅先行（评审 I2）：先占住流再对账——旧序（先对账后订阅）在客户端
    // 侧有「快照读取 ↔ 订阅建立」间的发布丢失窗，去重集无法察觉缺 seq。
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
      // 流开后再对账（评审 I2）：此刻起服务端已在订阅者集合内，对账在途
      // 期间的发布由实时通路送达——重叠帧去重吸收，无缺口无重复。失败
      // （含 404/409）走退避重试。
      reconcile().catch(() => scheduleReconnect())
    }
    es.onmessage = (m) => {
      try {
        handleData(m.data)
      } catch {
        scheduleReconnect()
      }
    }
    es.onerror = () => scheduleReconnect()
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
