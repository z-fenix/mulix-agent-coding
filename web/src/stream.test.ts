// stream.test.ts——openSessionStream 行为（task-10-brief Step 1 + 终审修复轮）：
// 1) (kind,seq) 幂等去重；2) 订阅先行、open 后对账（评审 I2：先订阅后对账，
// 流开与对账之间的发布由实时通路送达，重叠帧被去重吸收——不重不漏）；3) 断连
// 指数退避重连且重连重新对账；4) warnings 头帧 → onWarnings；5) 每次流建立
// （含重连）重申交互权（评审 C1）；6) SSE 订阅地址携带每标签页身份（评审 I1）。
// 附加：close() 后不再重连不再对账（重连定时器泄漏防护）。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { openSessionStream } from './stream'
import type { WireFrame, WarningsFrame } from './types'

const id = 's1'

// ---- 假 EventSource（记录实例；open 派发 onopen；emit 派发 onmessage；fail 派发 onerror）----
class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  readyState = 0
  onmessage: ((ev: { data: string }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  onopen: ((ev: unknown) => void) | null = null
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  // open 模拟连接就绪（真实 EventSource 收到 200 + text/event-stream 头后触发
  // onopen；I2 修复后服务端 Subscribe 先于握手行，onopen 即「订阅已就绪」）。
  open() {
    this.readyState = 1
    this.onopen?.({ type: 'open' })
  }
  emit(frame: unknown) {
    this.onmessage?.({ data: JSON.stringify(frame) })
  }
  fail() {
    this.readyState = 2
    this.onerror?.({ type: 'error' })
  }
  close() {
    this.readyState = 2
  }
}

// ---- 帧与 NDJSON 构造 ----
const warningsFrame: WarningsFrame = { warnings: ['损坏段隔离: 1 行'] }

function frame<K extends keyof import('./types').EvMap>(
  kind: K,
  seq: number,
  extra: Partial<import('./types').EvMap[K]> = {},
): WireFrame {
  // 测试脚手架：以 unknown 中转的双跳断言（帧体按用例手工构造，完整性由
  // 被测模块的形态守卫兜底）。
  return { kind, ev: { seq, ...extra } } as unknown as WireFrame
}

function ndjson(lines: unknown[]): string {
  return lines.map((l) => JSON.stringify(l)).join('\n') + '\n'
}

// fetch 桩：/events → 指定 NDJSON（onFetch 回调在对账响应返回前触发——模拟
// 「流已订阅、快照在途」窗口内的实时发布）；/open（C1 重申交互权）→
// interactive 应答；其余 URL 一律拒绝（意外调用显形）。
function stubFetch(replay: unknown[], onFetch?: () => void) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith(`/api/sessions/${id}/events`)) {
      onFetch?.()
      return { ok: true, status: 200, text: async () => ndjson(replay) }
    }
    if (url.endsWith(`/api/sessions/${id}/open`)) {
      return { ok: true, status: 200, json: async () => ({ id, role: 'interactive' }) }
    }
    throw new Error(`unexpected fetch: ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

// openCalls 统计 fetch 桩收到的 POST /open 次数（C1 重申交互权的断言面）。
function openCalls(fetchMock: ReturnType<typeof stubFetch>) {
  return fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/open'))
}

// eventsCalls 统计 GET /events 对账次数。
function eventsCalls(fetchMock: ReturnType<typeof stubFetch>) {
  return fetchMock.mock.calls.filter(([u]) => String(u).endsWith('/events'))
}

describe('openSessionStream', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    sessionStorage.clear() // 每标签页身份令牌测试面隔离（I1）
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  // 伪定时器下冲刷微任务链（对账 fetch → 解析 → 交付/订阅均为微任务）。
  async function flushAsync() {
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(0)
    }
  }

  it('dedups (kind,seq)：同帧重放两次 → handler 只收一次', async () => {
    stubFetch([warningsFrame, frame('turn_started', 1), frame('assistant_text', 2), frame('turn_started', 1)])
    const events: string[] = []
    openSessionStream(id, {
      onEvent: (f) => events.push(`${f.kind}:${f.ev.seq}`),
      onWarnings: () => {},
    })
    await vi.waitFor(() => expect(FakeEventSource.instances.length).toBe(1))
    FakeEventSource.instances[0].open()
    await vi.waitFor(() => expect(events.length).toBe(2))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2'])
    // live 通道重放同帧：仍只收一次
    FakeEventSource.instances[0].emit(frame('assistant_text', 2))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2'])
  })

  it('subscribes first then reconciles：订阅先行，open 后对账（评审 I2）', async () => {
    const fetchMock = stubFetch([
      warningsFrame,
      frame('turn_started', 1),
      frame('assistant_text', 2, { turn: 1, text: 'a' }),
      frame('tool_result', 3, { turn: 1, tool_use_id: 't1', content: 'x', is_error: false }),
    ])
    const events: string[] = []
    openSessionStream(id, {
      onEvent: (f) => events.push(`${f.kind}:${f.ev.seq}`),
      onWarnings: () => {},
    })
    // 订阅同步建立（旧序「先对账后订阅」期间事件有丢失窗；新序先占住流）
    expect(FakeEventSource.instances.length).toBe(1)
    expect(eventsCalls(fetchMock).length).toBe(0) // open 前不对账
    FakeEventSource.instances[0].open()
    await vi.waitFor(() => expect(events.length).toBe(3))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2', 'tool_result:3'])
    // live 通道续帧照常交付
    FakeEventSource.instances[0].emit(frame('assistant_text', 4, { turn: 1, text: 'b' }))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2', 'tool_result:3', 'assistant_text:4'])
  })

  it('gap-free：流开与对账之间的帧经实时通路送达恰一次（评审 I2）', async () => {
    // 修复前（先对账后订阅）：对账在途窗口内的实时发布在订阅前无通道可走
    // ——丢失且 (kind,seq) 去重集无法察觉缺口。修复后（先订阅后对账）：
    // 流一开服务端即 Subscribe，窗口内发布进缓冲通道，恰达一次。
    const midFrame = frame('tool_result', 3, { turn: 1, tool_use_id: 't1', content: 'x', is_error: false })
    // 快照不含该帧（发布落在快照读取之后）
    stubFetch([warningsFrame, frame('turn_started', 1), frame('assistant_text', 2)], () => {
      FakeEventSource.instances[0].emit(midFrame)
    })
    const events: string[] = []
    openSessionStream(id, {
      onEvent: (f) => events.push(`${f.kind}:${f.ev.seq}`),
      onWarnings: () => {},
    })
    FakeEventSource.instances[0].open()
    await vi.waitFor(() => expect(events.length).toBe(3))
    expect(events.filter((e) => e === 'tool_result:3').length).toBe(1) // 恰一次
    expect(events.filter((e) => e === 'turn_started:1').length).toBe(1)
    expect(events.filter((e) => e === 'assistant_text:2').length).toBe(1)
  })

  it('gap-free：对账与实时的重叠帧被去重恰一次（评审 I2）', async () => {
    // 同帧既在快照又经实时通道（发布落在快照读取之前、送达期间）：去重集
    // 吸收重叠，只渲染一次。
    const midFrame = frame('tool_result', 3, { turn: 1, tool_use_id: 't1', content: 'x', is_error: false })
    stubFetch([warningsFrame, frame('turn_started', 1), midFrame], () => {
      FakeEventSource.instances[0].emit(midFrame)
    })
    const events: string[] = []
    openSessionStream(id, {
      onEvent: (f) => events.push(`${f.kind}:${f.ev.seq}`),
      onWarnings: () => {},
    })
    FakeEventSource.instances[0].open()
    await vi.waitFor(() => expect(events.length).toBe(2))
    expect(events.filter((e) => e === 'tool_result:3').length).toBe(1)
    expect(events.filter((e) => e === 'turn_started:1').length).toBe(1)
  })

  it('per-tab identity token：SSE 订阅跨重连复用同一令牌（评审 I1）', async () => {
    vi.useFakeTimers()
    stubFetch([warningsFrame, frame('turn_started', 1)])
    openSessionStream(id, {
      onEvent: () => {},
      onWarnings: () => {},
    })
    await flushAsync()
    const token = sessionStorage.getItem('mulix_client')
    expect(token).toBeTruthy()
    expect(FakeEventSource.instances[0].url).toBe(`/api/sessions/${id}/stream?client=${token}`)
    // 重连（同标签页）复用同一令牌——服务端身份稳定，claim/release 对位
    FakeEventSource.instances[0].fail()
    await vi.advanceTimersByTimeAsync(600)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(2)
    expect(FakeEventSource.instances[1].url).toBe(`/api/sessions/${id}/stream?client=${token}`)
  })

  it('reconnects with backoff and re-reconciles：error 后退避重连，去重避免重复渲染', async () => {
    vi.useFakeTimers()
    const replay = [
      warningsFrame,
      frame('turn_started', 1),
      frame('assistant_text', 2),
      frame('tool_result', 3),
    ]
    const fetchMock = stubFetch(replay)
    const events: string[] = []
    openSessionStream(id, {
      onEvent: (f) => events.push(`${f.kind}:${f.ev.seq}`),
      onWarnings: () => {},
    })
    FakeEventSource.instances[0].open()
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    expect(events.length).toBe(3)
    // 断连：退避窗内不立即重连（首次退避 = 500ms，与实现注释「500ms 起」一致）
    FakeEventSource.instances[0].fail()
    await vi.advanceTimersByTimeAsync(400)
    expect(FakeEventSource.instances.length).toBe(1)
    // 退避到期：新订阅（订阅先行）+ open 后重新对账；重放帧全部被去重
    await vi.advanceTimersByTimeAsync(200)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(2)
    expect(eventsCalls(fetchMock).length).toBe(1) // 新连接 open 前不对账
    FakeEventSource.instances[1].open()
    await flushAsync()
    expect(eventsCalls(fetchMock).length).toBe(2)
    expect(events.length).toBe(3)
    // 新连接上新帧照常交付
    FakeEventSource.instances[1].emit(frame('assistant_text', 4, { turn: 1, text: 'b' }))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2', 'tool_result:3', 'assistant_text:4'])
  })

  it('surfaces warnings frame：warnings 头帧 → onWarnings 回调', async () => {
    stubFetch([warningsFrame, frame('turn_started', 1)])
    const warnings: string[][] = []
    openSessionStream(id, {
      onEvent: () => {},
      onWarnings: (w) => warnings.push(w),
    })
    FakeEventSource.instances[0].open()
    await vi.waitFor(() => expect(warnings.length).toBe(1))
    expect(warnings[0]).toEqual(['损坏段隔离: 1 行'])
  })

  it('unknown-shape frame fail-closed：合法 JSON 但形态未知 → 断流重连而非静默丢弃', async () => {
    vi.useFakeTimers()
    stubFetch([warningsFrame, frame('turn_started', 1)])
    const events: string[] = []
    openSessionStream(id, {
      onEvent: (f) => events.push(f.kind),
      onWarnings: () => {},
    })
    FakeEventSource.instances[0].open()
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    expect(events).toEqual(['turn_started'])
    // 合法 JSON 但既非 wire frame 也非 warnings 帧：与对账路径同形态 fail-closed
    FakeEventSource.instances[0].emit({ foo: 1 })
    await vi.advanceTimersByTimeAsync(400)
    expect(FakeEventSource.instances.length).toBe(1) // 退避窗内
    await vi.advanceTimersByTimeAsync(200)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(2) // 已断流重连
    expect(events).toEqual(['turn_started']) // 未知帧未入转录
  })

  it('close() 停止一切：断连后关闭 → 不再重连不再对账', async () => {
    vi.useFakeTimers()
    const fetchMock = stubFetch([warningsFrame, frame('turn_started', 1)])
    const stream = openSessionStream(id, {
      onEvent: () => {},
      onWarnings: () => {},
    })
    FakeEventSource.instances[0].open()
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    stream.close()
    FakeEventSource.instances[0].fail()
    await vi.advanceTimersByTimeAsync(10_000)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    expect(eventsCalls(fetchMock).length).toBe(1)
    expect(openCalls(fetchMock).length).toBe(1)
  })

  it('re-claims interactive on every stream open（评审 C1：宽限内重连重申交互权）', async () => {
    // 失败链(修复前):审批挂起 → SSE 闪断 → 服务端 release 进入 10s 宽限 →
    // EventSource 重连只做对账+订阅,从不重 claim → 宽限到点 CancelAll(
    // interactive_offline) 拒绝在审请求,尽管客户端已回来。修复:每次流建立
    // (含重连)的 onopen 都重申交互权——服务端 claim 同 client 幂等,且取消
    // 宽限离线计时(host.go claim 语义)。
    vi.useFakeTimers()
    const fetchMock = stubFetch([warningsFrame, frame('turn_started', 1)])
    openSessionStream(id, {
      onEvent: () => {},
      onWarnings: () => {},
    })
    FakeEventSource.instances[0].open()
    await flushAsync()
    expect(openCalls(fetchMock).length).toBe(1) // 首次流建立即重申
    // 断连重连:新流 open 后再次重申(宽限计时由重 claim 取消)
    FakeEventSource.instances[0].fail()
    await vi.advanceTimersByTimeAsync(600)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(2)
    FakeEventSource.instances[1].open()
    await flushAsync()
    expect(openCalls(fetchMock).length).toBe(2)
  })
})
