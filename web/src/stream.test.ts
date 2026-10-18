// stream.test.ts——openSessionStream 四行为（task-10-brief Step 1）：
// 1) (kind,seq) 幂等去重；2) 先对账后订阅，序 = 镜像序；3) 断连指数退避重连
// 且重连重新对账（去重保证无重复）；4) warnings 头帧 → onWarnings。
// 附加：close() 后不再重连不再对账（重连定时器泄漏防护）。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { openSessionStream } from './stream'
import type { WireFrame, WarningsFrame } from './types'

const id = 's1'

// ---- 假 EventSource（记录实例；emit 派发 onmessage；fail 派发 onerror）----
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

// fetch 桩：/events → 指定 NDJSON；其余 URL 一律拒绝（意外调用显形）。
function stubFetch(replay: unknown[]) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith(`/api/sessions/${id}/events`)) {
      return { ok: true, status: 200, text: async () => ndjson(replay) }
    }
    throw new Error(`unexpected fetch: ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('openSessionStream', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
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
    expect(events).toEqual(['turn_started:1', 'assistant_text:2'])
    // live 通道重放同帧：仍只收一次
    FakeEventSource.instances[0].emit(frame('assistant_text', 2))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2'])
  })

  it('replays then subscribes live：对账 3 事件后 live 1 事件，序 = 镜像序', async () => {
    stubFetch([
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
    await vi.waitFor(() => expect(FakeEventSource.instances.length).toBe(1))
    // 对账完成后才建立订阅，且订阅地址正确
    expect(FakeEventSource.instances[0].url).toBe(`/api/sessions/${id}/stream`)
    expect(events).toEqual(['turn_started:1', 'assistant_text:2', 'tool_result:3'])
    FakeEventSource.instances[0].emit(frame('assistant_text', 4, { turn: 1, text: 'b' }))
    expect(events).toEqual(['turn_started:1', 'assistant_text:2', 'tool_result:3', 'assistant_text:4'])
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
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    expect(events.length).toBe(3)
    // 断连：退避窗内不立即重连
    FakeEventSource.instances[0].fail()
    await vi.advanceTimersByTimeAsync(100)
    expect(FakeEventSource.instances.length).toBe(1)
    // 退避到期：重新对账 + 新订阅；重放帧全部被去重
    await vi.advanceTimersByTimeAsync(2000)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(2)
    expect(fetchMock).toHaveBeenCalledTimes(2)
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
    await vi.waitFor(() => expect(warnings.length).toBe(1))
    expect(warnings[0]).toEqual(['损坏段隔离: 1 行'])
  })

  it('close() 停止一切：断连后关闭 → 不再重连不再对账', async () => {
    vi.useFakeTimers()
    const fetchMock = stubFetch([warningsFrame, frame('turn_started', 1)])
    const stream = openSessionStream(id, {
      onEvent: () => {},
      onWarnings: () => {},
    })
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    stream.close()
    FakeEventSource.instances[0].fail()
    await vi.advanceTimersByTimeAsync(10_000)
    await flushAsync()
    expect(FakeEventSource.instances.length).toBe(1)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
