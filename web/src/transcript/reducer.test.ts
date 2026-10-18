// reducer.test.ts——transcript reducer 契约测试（task-11 Step 1）。
// 覆盖：tool use/result 按 tool_use_id 配对、assistant_text 连续合并（turn_started
// 断点）、孤立 use（引擎中断态）与 engine_exited 扫尾、approval 两态、
// notice 分级、usage 回执、turn_done 正常不渲染/失败渲染、delegation 生命周期。
// 耗时口径 Ruling：事件流（types.ts）无时间戳字段，配对卡不携带 duration——
// 不虚构计时（task 指令明令）；耗时只能来自 TurnDone.duration_ms 的回合级粒度，
// 不归属单张工具卡。
import { describe, expect, it } from 'vitest'
import { reduceTranscript, receiptLine } from './reducer'
import type { EventKind, EvMap, WireFrame } from '../types'

function fr<K extends EventKind>(kind: K, ev: EvMap[K]): WireFrame {
  return { kind, ev } as WireFrame
}

const TURN_START = fr('turn_started', {
  seq: 1,
  session_id: 's1',
  engine_session_id: 'e1',
  pinned_model: 'm',
  unattended: false,
})

describe('reduceTranscript — assistant_text 合并', () => {
  it('merges consecutive assistant_text and splits at turn_started', () => {
    const frames = [
      TURN_START,
      fr('assistant_text', { seq: 2, turn: 1, text: 'hello ' }),
      fr('assistant_text', { seq: 3, turn: 1, text: 'world' }),
      TURN_START,
      fr('assistant_text', { seq: 5, turn: 2, text: 'next turn' }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(2)
    const [first, second] = entries
    expect(first.kind).toBe('text')
    expect(first).toMatchObject({ kind: 'text', turn: 1, text: 'hello world', seq: 2 })
    expect(second).toMatchObject({ kind: 'text', turn: 2, text: 'next turn', seq: 5 })
  })

  it('splits the merge at any intervening non-text event (usage)', () => {
    const frames = [
      fr('assistant_text', { seq: 1, turn: 1, text: 'a' }),
      fr('usage', {
        seq: 2,
        turn: 1,
        uncached_input_tokens: 0,
        write_tokens: 0,
        read_tokens: 0,
        output_tokens: 0,
        thinking_tokens: 0,
        cost_usd: 0,
        hit_rate: 0,
      }),
      fr('assistant_text', { seq: 3, turn: 1, text: 'b' }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(3)
    expect(entries[0]).toMatchObject({ kind: 'text', text: 'a' })
    expect(entries[1].kind).toBe('usage')
    expect(entries[2]).toMatchObject({ kind: 'text', text: 'b' })
  })
})

describe('reduceTranscript — assistant_thinking 合并', () => {
  it('merges consecutive thinking shards keeping the latest aggregate tokens', () => {
    const frames = [
      fr('assistant_thinking', { seq: 1, turn: 1, thinking: 'th', estimated_tokens: 10 }),
      fr('assistant_thinking', { seq: 2, turn: 1, thinking: 'ink', estimated_tokens: 25 }),
      fr('assistant_text', { seq: 3, turn: 1, text: 'answer' }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(2)
    expect(entries[0]).toMatchObject({ kind: 'thinking', text: 'think', tokens: 25 })
  })
})

describe('reduceTranscript — 工具卡配对', () => {
  it('pairs tool_use_requested + tool_result by tool_use_id into a done card', () => {
    const frames = [
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 'tu1', tool: 'Bash', input: { command: 'ls' } }),
      fr('tool_result', { seq: 2, turn: 1, tool_use_id: 'tu1', content: 'file.txt', is_error: false }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(1)
    const card = entries[0]
    expect(card).toMatchObject({
      kind: 'tool',
      toolUseId: 'tu1',
      tool: 'Bash',
      state: 'done',
      output: 'file.txt',
      isError: false,
    })
    // 事件无时间戳——配对卡不虚构 duration（见文件头 Ruling）。
    expect('durationMs' in card).toBe(false)
    expect('duration_ms' in card).toBe(false)
  })

  it('marks an errored result as failed with first non-empty line as detail', () => {
    const frames = [
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 'tu1', tool: 'Bash', input: {} }),
      fr('tool_result', { seq: 2, turn: 1, tool_use_id: 'tu1', content: '\nboom: exit 1\nrest', is_error: true }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({
      kind: 'tool',
      state: 'failed',
      isError: true,
      failedDetail: 'boom: exit 1',
      output: '\nboom: exit 1\nrest',
    })
  })

  it('keeps an orphan tool use running, then engine_exited sweeps it to failed', () => {
    const frames = [
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 'tu2', tool: 'Read', input: { file_path: 'x' } }),
      fr('engine_exited', { seq: 2, code: 1, reason: 'engine died' }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(2)
    expect(entries[0]).toMatchObject({
      kind: 'tool',
      toolUseId: 'tu2',
      state: 'failed',
      failedDetail: '引擎已退出，工具未返回',
    })
    expect(entries[1]).toMatchObject({ kind: 'engine_exited', reason: 'engine died' })
  })

  it('synthesizes a card for an orphan result (request absent — replay boundary)', () => {
    const frames = [
      fr('tool_result', { seq: 1, turn: 1, tool_use_id: 'tu3', content: 'late', is_error: false }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({
      kind: 'tool',
      toolUseId: 'tu3',
      tool: '未知工具',
      state: 'done',
      output: 'late',
    })
  })
})

describe('reduceTranscript — 审批两态', () => {
  it('pairs approval_request + approval_resolved into one two-state entry', () => {
    const frames = [
      fr('approval_request', {
        seq: 1,
        turn: 1,
        tool_use_id: 'tu9',
        tool: 'Bash',
        input: { command: 'rm -rf /' },
        timeout: 120,
      }),
      fr('approval_resolved', { seq: 2, tool_use_id: 'tu9', decision: 'deny', source: 'user', resolved_by: 'u' }),
    ]
    const entries = reduceTranscript(frames)
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({
      kind: 'approval',
      toolUseId: 'tu9',
      tool: 'Bash',
      resolved: true,
      decision: 'deny',
      source: 'user',
    })
  })

  it('renders a pending approval entry before resolution', () => {
    const entries = reduceTranscript([
      fr('approval_request', {
        seq: 1,
        turn: 1,
        tool_use_id: 'tu9',
        tool: 'Bash',
        input: {},
        timeout: 0,
      }),
    ])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({ kind: 'approval', resolved: false, decision: '', timeout: 0 })
  })

  it('keeps an orphan resolution as a standalone resolved entry', () => {
    const entries = reduceTranscript([
      fr('approval_resolved', { seq: 1, tool_use_id: 'tuX', decision: 'timeout', source: 'timeout', resolved_by: 'guard' }),
    ])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({ kind: 'approval', toolUseId: 'tuX', resolved: true, decision: 'timeout' })
  })
})

describe('reduceTranscript — 系统行', () => {
  it('emits notice entries level-aware (warning vs info/meta)', () => {
    const entries = reduceTranscript([
      fr('notice', { seq: 1, level: 'warning', content: 'disk low', is_meta: false }),
      fr('notice', { seq: 2, level: 'info', content: 'meta hint', is_meta: true }),
    ])
    expect(entries).toEqual([
      { kind: 'notice', seq: 1, level: 'warning', content: 'disk low', isMeta: false },
      { kind: 'notice', seq: 2, level: 'info', content: 'meta hint', isMeta: true },
    ])
  })

  it('emits a usage receipt entry with the TUI-aligned line format', () => {
    const ev = {
      seq: 1,
      turn: 3,
      uncached_input_tokens: 500,
      write_tokens: 100,
      read_tokens: 2000,
      output_tokens: 800,
      thinking_tokens: 0,
      cost_usd: 0.0123,
      hit_rate: 0.5,
    }
    const entries = reduceTranscript([fr('usage', ev)])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({ kind: 'usage', seq: 1 })
    expect(receiptLine(ev)).toBe(
      '本轮  3.4K tok · in 500 · cached 2.0K · new 100 · out 800 · reasoning 0 · ≈$0.0123',
    )
  })

  it('renders nothing for normal turn_done, an error line for failed turns', () => {
    const normal = reduceTranscript([fr('turn_done', { seq: 1, turn: 1, subtype: 'success', is_error: false, duration_ms: 12, result_text: '' })])
    expect(normal).toHaveLength(0)
    const failed = reduceTranscript([fr('turn_done', { seq: 2, turn: 4, subtype: 'api_error', is_error: true, duration_ms: 12, result_text: '' })])
    expect(failed).toEqual([{ kind: 'turn_error', seq: 2, turn: 4, subtype: 'api_error' }])
  })
})

describe('reduceTranscript — 委派卡生命周期', () => {
  it('upserts one card across started → completed with summary/tokens/cost', () => {
    const entries = reduceTranscript([
      fr('delegation_started', { seq: 1, sub_session_id: 'sub1234567890', task: 'do thing', depth: 1 }),
      fr('delegation_completed', { seq: 2, sub_session_id: 'sub1234567890', summary: 'done well', total_tokens: 1500, cost_usd_cents: 7 }),
    ])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({
      kind: 'delegation',
      subSessionId: 'sub1234567890',
      task: 'do thing',
      state: 'done',
      summary: 'done well',
      tokens: 1500,
      costCents: 7,
    })
  })

  it('maps cancelled and failed receipts onto the running card', () => {
    const cancelled = reduceTranscript([
      fr('delegation_started', { seq: 1, sub_session_id: 's1', task: 't', depth: 0 }),
      fr('delegation_cancelled', { seq: 2, sub_session_id: 's1', reason: 'user cancel' }),
    ])
    expect(cancelled).toHaveLength(1)
    expect(cancelled[0]).toMatchObject({ kind: 'delegation', state: 'cancelled', reason: 'user cancel' })
    const failed = reduceTranscript([
      fr('delegation_started', { seq: 1, sub_session_id: 's2', task: 't', depth: 0 }),
      fr('delegation_failed', { seq: 2, sub_session_id: 's2', reason: 'crash' }),
    ])
    expect(failed).toHaveLength(1)
    expect(failed[0]).toMatchObject({ kind: 'delegation', state: 'failed', reason: 'crash' })
  })

  it('synthesizes a card for an orphan delegation receipt', () => {
    const entries = reduceTranscript([
      fr('delegation_completed', { seq: 1, sub_session_id: 'orphan', summary: 's', total_tokens: 1, cost_usd_cents: 0 }),
    ])
    expect(entries).toHaveLength(1)
    expect(entries[0]).toMatchObject({ kind: 'delegation', state: 'done', summary: 's' })
  })
})

describe('reduceTranscript — 纯函数性', () => {
  it('does not mutate the input frames array', () => {
    const frames = [
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 't', tool: 'Bash', input: { a: 1 } }),
      fr('tool_result', { seq: 2, turn: 1, tool_use_id: 't', content: 'x', is_error: false }),
    ]
    const snapshot = JSON.parse(JSON.stringify(frames))
    reduceTranscript(frames)
    expect(frames).toEqual(snapshot)
  })
})
