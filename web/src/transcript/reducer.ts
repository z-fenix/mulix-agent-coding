// reducer.ts——transcript 归约（task-11）：WireFrame[] → TranscriptEntry[]。
// 纯函数（不修改入参、无副作用），对齐 TUI 呈现语义（internal/tui/model.go
// handleEvent 为参照）：
//   - assistant_text 连续分片合并为一条（流式段落）；任何非文本事件（含
//     turn_started）打断合并——对齐 TUI sealAnswer/closeThinking 块生命周期；
//   - tool_use_requested + tool_result 按 tool_use_id 配对成单卡（运行中 →
//     完成/失败原地收口，FR-005）；孤儿回执合成卡留痕不静默（TUI closeToolCard
//     同纪律）；engine_exited 扫尾运行中卡为失败（024 FR-002，model.go:532-552）；
//   - approval_request/resolved 两态条目（裁决回填原条目；孤儿裁决独立留痕）；
//   - thinking 连续分片合并为折叠块；notice 分级行；usage 回执行；
//   - turn_done 正常完成不渲染（025 FR-005 TUI 对齐），失败回合出错误行。
//
// 耗时口径 Ruling：事件流（types.ts）的 tool_use_requested/tool_result 均无
// 时间戳字段——配对卡不携带 duration，不虚构计时；TurnDone.duration_ms 是回合
// 级粒度，不归属单张工具卡。网页端 MVP 的卡片状态即事件时序本身。
//
// 本地取消意图（TUI cancelIntent → ⏹ 行）是终端会话本地状态，事件流中不存在
// ——reducer 无法复现，turn_done 只按 is_error 分流（TUI 语义的可达子集）。
import type { UsageEv, WireFrame } from '../types'

// ---- 条目形态 ----

export interface TextEntry {
  kind: 'text'
  seq: number
  turn: number
  text: string
}

// ThinkingEntry 思考折叠块；tokens 是 estimated_tokens（引擎聚合口径——取末次事件值）。
export interface ThinkingEntry {
  kind: 'thinking'
  seq: number
  turn: number
  text: string
  tokens: number
}

export type ToolState = 'running' | 'done' | 'failed'

// ToolEntry 工具卡（配对产物）。output 是结果全文（渲染侧再决定折叠/摘要）。
export interface ToolEntry {
  kind: 'tool'
  seq: number
  toolUseId: string
  tool: string
  input: unknown
  state: ToolState
  output: string
  isError: boolean
  failedDetail: string
}

// ApprovalEntry 审批两态条目：request 开卡（resolved=false），resolved 回填
// 裁决（decision/source）；孤儿裁决合成独立条目。
export interface ApprovalEntry {
  kind: 'approval'
  seq: number
  toolUseId: string
  tool: string
  input: unknown
  timeout: number
  resolved: boolean
  decision: string
  source: string
}

export interface NoticeEntry {
  kind: 'notice'
  seq: number
  level: string
  content: string
  isMeta: boolean
}

export interface UsageEntry {
  kind: 'usage'
  seq: number
  ev: UsageEv
}

// TurnErrorEntry 失败回合收口行（正常完成不产条目——025 FR-005）。
export interface TurnErrorEntry {
  kind: 'turn_error'
  seq: number
  turn: number
  subtype: string
}

export interface EngineExitedEntry {
  kind: 'engine_exited'
  seq: number
  code: number
  reason: string
}

export type DelegationState = 'running' | 'done' | 'cancelled' | 'failed'

export interface DelegationEntry {
  kind: 'delegation'
  seq: number
  subSessionId: string
  task: string
  state: DelegationState
  summary: string
  tokens: number
  costCents: number
  reason: string
}

export type TranscriptEntry =
  | TextEntry
  | ThinkingEntry
  | ToolEntry
  | ApprovalEntry
  | NoticeEntry
  | UsageEntry
  | TurnErrorEntry
  | EngineExitedEntry
  | DelegationEntry

// ---- 归约 ----

export function reduceTranscript(frames: WireFrame[]): TranscriptEntry[] {
  const entries: TranscriptEntry[] = []
  // mergeable：上一帧是否为同类可合并分片（text/thinking 各自独立判定）——
  // 任何其他事件到达即打断，turn_started 由此天然成为合并断点。
  let textMergeable = false
  let thinkingMergeable = false
  // 索引：tool_use_id / tool_use_id / sub_session_id → entries 下标（upsert 原地收口）。
  const toolIdx = new Map<string, number>()
  const approvalIdx = new Map<string, number>()
  const delegIdx = new Map<string, number>()

  for (const frame of frames) {
    switch (frame.kind) {
      case 'turn_started':
        // 头行本身不渲染条目（TUI turnHeaderPending 只冠正文块）；打断合并段。
        textMergeable = false
        thinkingMergeable = false
        break
      case 'assistant_text': {
        const last = entries[entries.length - 1]
        if (textMergeable && last?.kind === 'text' && last.turn === frame.ev.turn) {
          entries[entries.length - 1] = { ...last, text: last.text + frame.ev.text }
        } else {
          entries.push({
            kind: 'text',
            seq: frame.ev.seq,
            turn: frame.ev.turn,
            text: frame.ev.text,
          })
        }
        textMergeable = true
        thinkingMergeable = false
        break
      }
      case 'assistant_thinking': {
        const last = entries[entries.length - 1]
        if (thinkingMergeable && last?.kind === 'thinking' && last.turn === frame.ev.turn) {
          entries[entries.length - 1] = {
            ...last,
            text: last.text + frame.ev.thinking,
            tokens: frame.ev.estimated_tokens,
          }
        } else {
          entries.push({
            kind: 'thinking',
            seq: frame.ev.seq,
            turn: frame.ev.turn,
            text: frame.ev.thinking,
            tokens: frame.ev.estimated_tokens,
          })
        }
        thinkingMergeable = true
        textMergeable = false
        break
      }
      case 'tool_use_requested': {
        entries.push({
          kind: 'tool',
          seq: frame.ev.seq,
          toolUseId: frame.ev.tool_use_id,
          tool: frame.ev.tool,
          input: frame.ev.input,
          state: 'running',
          output: '',
          isError: false,
          failedDetail: '',
        })
        toolIdx.set(frame.ev.tool_use_id, entries.length - 1)
        break
      }
      case 'tool_result': {
        const idx = toolIdx.get(frame.ev.tool_use_id)
        const open = idx !== undefined ? entries[idx] : undefined
        if (!(open?.kind === 'tool') || idx === undefined) {
          // 孤儿回执（请求事件缺席——重放边界）：合成卡留痕，失败不静默。
          // ToolResultEvent 无工具名，请求缺席时名不可考（TUI 同口径「未知工具」）。
          entries.push({
            kind: 'tool',
            seq: frame.ev.seq,
            toolUseId: frame.ev.tool_use_id,
            tool: '未知工具',
            input: null,
            ...resultFields(frame.ev.content, frame.ev.is_error),
          })
          toolIdx.set(frame.ev.tool_use_id, entries.length - 1)
          break
        }
        entries[idx] = toolCardWithResult(open, frame.ev.content, frame.ev.is_error)
        textMergeable = false
        thinkingMergeable = false
        break
      }
      case 'approval_request': {
        entries.push({
          kind: 'approval',
          seq: frame.ev.seq,
          toolUseId: frame.ev.tool_use_id,
          tool: frame.ev.tool,
          input: frame.ev.input,
          timeout: frame.ev.timeout,
          resolved: false,
          decision: '',
          source: '',
        })
        approvalIdx.set(frame.ev.tool_use_id, entries.length - 1)
        break
      }
      case 'approval_resolved': {
        const idx = approvalIdx.get(frame.ev.tool_use_id)
        const open = idx !== undefined ? entries[idx] : undefined
        if (!(open?.kind === 'approval') || idx === undefined) {
          entries.push({
            kind: 'approval',
            seq: frame.ev.seq,
            toolUseId: frame.ev.tool_use_id,
            tool: '',
            input: null,
            timeout: 0,
            resolved: true,
            decision: frame.ev.decision,
            source: frame.ev.source,
          })
          approvalIdx.set(frame.ev.tool_use_id, entries.length - 1)
          break
        }
        entries[idx] = {
          ...open,
          resolved: true,
          decision: frame.ev.decision,
          source: frame.ev.source,
        }
        break
      }
      case 'notice':
        entries.push({
          kind: 'notice',
          seq: frame.ev.seq,
          level: frame.ev.level,
          content: frame.ev.content,
          isMeta: frame.ev.is_meta,
        })
        break
      case 'usage':
        entries.push({ kind: 'usage', seq: frame.ev.seq, ev: frame.ev })
        break
      case 'turn_done':
        // 正常完成不渲染收口行（025 FR-005：usage 回执即收口标记）；失败回合
        // 出错误行。⏹ 取消行依赖 TUI 本地 cancelIntent，事件流不可达（见头注）。
        if (frame.ev.is_error) {
          entries.push({
            kind: 'turn_error',
            seq: frame.ev.seq,
            turn: frame.ev.turn,
            subtype: frame.ev.subtype,
          })
        }
        break
      case 'engine_exited': {
        // 024 FR-002 扫尾：所有运行中工具卡 → 失败（引擎已死不得永久渲染
        // 「运行中」）。幂等——无运行中卡零改写；委派卡不在扫尾范围（TUI
        // model.go 同口径只扫工具卡）。
        for (let i = 0; i < entries.length; i++) {
          const e = entries[i]
          if (e.kind === 'tool' && e.state === 'running') {
            entries[i] = { ...e, state: 'failed', failedDetail: '引擎已退出，工具未返回' }
          }
        }
        entries.push({
          kind: 'engine_exited',
          seq: frame.ev.seq,
          code: frame.ev.code,
          reason: frame.ev.reason,
        })
        break
      }
      case 'delegation_started':
        entries.push({
          kind: 'delegation',
          seq: frame.ev.seq,
          subSessionId: frame.ev.sub_session_id,
          task: frame.ev.task,
          state: 'running',
          summary: '',
          tokens: 0,
          costCents: 0,
          reason: '',
        })
        delegIdx.set(frame.ev.sub_session_id, entries.length - 1)
        break
      case 'delegation_completed': {
        const idx = delegIdx.get(frame.ev.sub_session_id)
        const open = idx !== undefined ? entries[idx] : undefined
        const patch = { state: 'done' as const, summary: frame.ev.summary, tokens: frame.ev.total_tokens, costCents: frame.ev.cost_usd_cents }
        if (!(open?.kind === 'delegation') || idx === undefined) {
          // 孤儿回执合成新卡留痕（TUI closeDelegationCard 同纪律）。
          entries.push({
            kind: 'delegation',
            seq: frame.ev.seq,
            subSessionId: frame.ev.sub_session_id,
            task: '',
            ...patch,
            reason: '',
          })
          delegIdx.set(frame.ev.sub_session_id, entries.length - 1)
          break
        }
        entries[idx] = { ...open, ...patch }
        break
      }
      case 'delegation_cancelled': {
        const idx = delegIdx.get(frame.ev.sub_session_id)
        const open = idx !== undefined ? entries[idx] : undefined
        const patch = { state: 'cancelled' as const, reason: frame.ev.reason }
        if (!(open?.kind === 'delegation') || idx === undefined) {
          entries.push({
            kind: 'delegation',
            seq: frame.ev.seq,
            subSessionId: frame.ev.sub_session_id,
            task: '',
            summary: '',
            tokens: 0,
            costCents: 0,
            ...patch,
          })
          delegIdx.set(frame.ev.sub_session_id, entries.length - 1)
          break
        }
        entries[idx] = { ...open, ...patch }
        break
      }
      case 'delegation_failed': {
        const idx = delegIdx.get(frame.ev.sub_session_id)
        const open = idx !== undefined ? entries[idx] : undefined
        const patch = { state: 'failed' as const, reason: frame.ev.reason }
        if (!(open?.kind === 'delegation') || idx === undefined) {
          entries.push({
            kind: 'delegation',
            seq: frame.ev.seq,
            subSessionId: frame.ev.sub_session_id,
            task: '',
            summary: '',
            tokens: 0,
            costCents: 0,
            ...patch,
          })
          delegIdx.set(frame.ev.sub_session_id, entries.length - 1)
          break
        }
        entries[idx] = { ...open, ...patch }
        break
      }
      default: {
        // 未知 kind（向前兼容——编译期穷尽守卫）：留痕不静默（fail-closed 渲染面）。
        const unknown: never = frame
        void unknown
        break
      }
    }
  }
  return entries
}

// toolCardWithResult 回执落卡（原地收口的新对象）：is_error → 失败态 + 详情行。
function toolCardWithResult(card: ToolEntry, content: string, isError: boolean): ToolEntry {
  return { ...card, ...resultFields(content, isError) }
}

function resultFields(content: string, isError: boolean): Pick<ToolEntry, 'state' | 'output' | 'isError' | 'failedDetail'> {
  return {
    state: isError ? 'failed' : 'done',
    output: content,
    isError,
    failedDetail: isError ? failureDetail(content) : '',
  }
}

// failureDetail 失败详情：Content 首个非空行（截断 200——TUI failureDetail 同口径）。
function failureDetail(content: string): string {
  for (const line of content.split('\n')) {
    const t = line.trim()
    if (t !== '') {
      return t.length > 200 ? t.slice(0, 200) + '…' : t
    }
  }
  return ''
}

// ---- 回执行格式（TUI receipt.line 同源——status.go 字段序/分隔符对齐） ----

export function receiptLine(u: UsageEv): string {
  const total =
    u.uncached_input_tokens + u.write_tokens + u.read_tokens + u.output_tokens + u.thinking_tokens
  return (
    `本轮  ${humanizeTok(total)} tok · in ${humanizeTok(u.uncached_input_tokens)}` +
    ` · cached ${humanizeTok(u.read_tokens)} · new ${humanizeTok(u.write_tokens)}` +
    ` · out ${humanizeTok(u.output_tokens)} · reasoning ${humanizeTok(u.thinking_tokens)}` +
    ` · ≈$${u.cost_usd.toFixed(4)}`
  )
}

function humanizeTok(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}K` : String(n)
}

// shortID 长标识符短显（TUI shortID 同口径：8 字符 + 省略号）。
export function shortID(id: string): string {
  return id.length > 8 ? id.slice(0, 8) + '…' : id
}
