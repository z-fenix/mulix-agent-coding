import type {
  MulixWorkbenchCounts as Counts,
  MulixWorkbenchIdentity as Identity,
  MulixWorkbenchNativeView as NativeView,
  MulixWorkbenchObservation as Observation,
  MulixWorkbenchState as State,
  MulixWorkbenchWorkflowFields as WorkflowFields,
  MulixWorkbenchWorkflowSnapshot as WorkflowSnapshot,
} from '../types'

export const TARGET_VERSION = '2.1.302'
export const MAX_SEEN_TURNS = 4096
const phases: readonly string[] = ['specify', 'clarify', 'design', 'tasks', 'build', 'verify', 'archive']
const verifyStates: readonly string[] = ['pending', 'pass', 'fail']

const record = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0
const time = (v: unknown): v is number => count(v) && v <= 8_640_000_000_000_000
const text = (v: unknown, max = 4096): v is string => typeof v === 'string' && v.length <= max
const nonempty = (v: unknown, max = 4096): v is string => text(v, max) && v.length > 0
const nullableText = (v: unknown): v is string | null => v === null || text(v)
const nullableTime = (v: unknown): v is number | null => v === null || time(v)
const nullableCount = (v: unknown): v is number | null => v === null || count(v)
const percent = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0 && v <= 100

function emptyObservation(epoch: number): Observation {
  return {
    epoch, since: null, until: null, pendingTurnId: null, seenTurnIds: [],
    validTurns: 0, missingTurns: 0, invalidTurns: 0,
    counts: { input: 0, write: 0, read: 0, output: 0 }, models: [], frozen: false,
  }
}

export function freshState(): State {
  return {
    schemaVersion: 1, identity: null, observation: emptyObservation(0),
    workflow: null, nativeUsage: null, warning: null,
    ui: { view: 'workflow', open: false, placed: false, busy: false, generation: 0, error: null },
  }
}

function identityValid(v: unknown): v is Identity {
  return record(v) && nonempty(v.sessionId, 512) && time(v.startedAt) && nonempty(v.root) && text(v.version, 128)
}

function countsValid(v: unknown): v is Counts {
  return record(v) && count(v.input) && count(v.write) && count(v.read) && count(v.output)
    && Number.isSafeInteger(v.input + v.write + v.read)
}

function observationValid(v: unknown): v is Observation {
  if (!record(v) || !count(v.epoch) || !nullableTime(v.since) || !nullableTime(v.until)
    || !(v.pendingTurnId === null || nonempty(v.pendingTurnId, 512))
    || !Array.isArray(v.seenTurnIds) || v.seenTurnIds.length > MAX_SEEN_TURNS
    || !v.seenTurnIds.every(id => nonempty(id, 512)) || new Set(v.seenTurnIds).size !== v.seenTurnIds.length
    || !count(v.validTurns) || !count(v.missingTurns) || !count(v.invalidTurns)
    || !countsValid(v.counts) || !Array.isArray(v.models) || !v.models.every(m => nonempty(m, 256))
    || new Set(v.models).size !== v.models.length || v.models.length > v.validTurns || typeof v.frozen !== 'boolean') return false
  if (v.validTurns + v.missingTurns + v.invalidTurns !== v.seenTurnIds.length) return false
  if (v.seenTurnIds.length > 0 && (v.since === null || v.until === null)) return false
  if (v.since !== null && v.until !== null && v.until < v.since) return false
  return v.pendingTurnId === null || !v.seenTurnIds.includes(v.pendingTurnId)
}

function workflowFieldsValid(v: unknown): v is WorkflowFields {
  return record(v) && nonempty(v.change, 256) && typeof v.phase === 'string' && phases.includes(v.phase)
    && typeof v.designApproved === 'boolean' && typeof v.verify === 'string' && verifyStates.includes(v.verify)
}

function workflowValid(v: unknown): v is WorkflowSnapshot {
  return record(v) && nonempty(v.root) && time(v.observedAt)
    && (v.fields === null || workflowFieldsValid(v.fields)) && nullableText(v.reason) && typeof v.stale === 'boolean'
}

function nativeValid(v: unknown): v is NativeView {
  return record(v) && nonempty(v.sessionId, 512) && time(v.startedAt) && time(v.observedAt)
    && nullableCount(v.tokens) && nullableCount(v.window) && (v.percent === null || percent(v.percent))
    && record(v.cost) && v.cost.kind === 'unavailable'
    && (v.cost.reason === 'pricing-coverage-unknown' || v.cost.reason === 'host-cost-unavailable')
}

function stateValid(v: unknown): v is State {
  if (!record(v) || v.schemaVersion !== 1 || !(v.identity === null || identityValid(v.identity))
    || !observationValid(v.observation) || !(v.workflow === null || workflowValid(v.workflow))
    || !(v.nativeUsage === null || nativeValid(v.nativeUsage)) || !nullableText(v.warning) || !record(v.ui)) return false
  const ui = v.ui
  if (!(ui.view === 'workflow' || ui.view === 'usage') || typeof ui.open !== 'boolean' || typeof ui.placed !== 'boolean'
    || typeof ui.busy !== 'boolean' || !count(ui.generation) || !nullableText(ui.error)) return false
  if (v.identity === null) return v.workflow === null && v.nativeUsage === null && v.observation.seenTurnIds.length === 0 && v.observation.pendingTurnId === null
  if (v.workflow !== null && v.workflow.root !== v.identity.root) return false
  return v.nativeUsage === null || (v.nativeUsage.sessionId === v.identity.sessionId && v.nativeUsage.startedAt === v.identity.startedAt)
}

export function restoreState(raw: unknown): State {
  if (!stateValid(raw)) {
    const reset = freshState()
    if (raw !== undefined && raw !== null) reset.warning = '旧工作台观察数据不兼容，已开始新的观察窗口'
    return reset
  }
  const s = raw
  return {
    schemaVersion: 1,
    identity: s.identity === null ? null : { sessionId: s.identity.sessionId, startedAt: s.identity.startedAt, root: s.identity.root, version: s.identity.version },
    observation: {
      epoch: s.observation.epoch, since: s.observation.since, until: s.observation.until,
      pendingTurnId: s.observation.pendingTurnId, seenTurnIds: [...s.observation.seenTurnIds],
      validTurns: s.observation.validTurns, missingTurns: s.observation.missingTurns, invalidTurns: s.observation.invalidTurns,
      counts: { input: s.observation.counts.input, write: s.observation.counts.write, read: s.observation.counts.read, output: s.observation.counts.output },
      models: [...s.observation.models], frozen: s.observation.frozen || s.observation.seenTurnIds.length >= MAX_SEEN_TURNS,
    },
    workflow: s.workflow === null ? null : {
      root: s.workflow.root, observedAt: s.workflow.observedAt, reason: s.workflow.reason, stale: s.workflow.stale,
      fields: s.workflow.fields === null ? null : { change: s.workflow.fields.change, phase: s.workflow.fields.phase, designApproved: s.workflow.fields.designApproved, verify: s.workflow.fields.verify },
    },
    nativeUsage: s.nativeUsage === null ? null : {
      sessionId: s.nativeUsage.sessionId, startedAt: s.nativeUsage.startedAt, observedAt: s.nativeUsage.observedAt,
      tokens: s.nativeUsage.tokens, window: s.nativeUsage.window, percent: s.nativeUsage.percent,
      cost: { kind: 'unavailable', reason: s.nativeUsage.cost.reason },
    },
    warning: s.warning,
    ui: { view: s.ui.view, open: s.ui.open, placed: s.ui.placed, busy: s.ui.busy, generation: s.ui.generation, error: s.ui.error },
  }
}

export function bindIdentity(s: State, id: Identity): State {
  if (!identityValid(id)) return { ...retireSession(s), warning: '原生会话身份不可用，未合并任何历史用量' }
  const identity = { sessionId: id.sessionId, startedAt: id.startedAt, root: id.root, version: id.version }
  if (s.identity === null || s.identity.sessionId !== id.sessionId || s.identity.startedAt !== id.startedAt) {
    return {
      ...freshState(), identity, observation: emptyObservation(s.observation.epoch + 1), warning: s.warning,
      ui: { ...s.ui, busy: false, generation: s.ui.generation + 1, error: null },
    }
  }
  if (s.identity.root === id.root && s.identity.version === id.version) return s
  return {
    ...s, identity, workflow: null,
    nativeUsage: s.identity.version === id.version ? s.nativeUsage : null,
    ui: { ...s.ui, busy: false, generation: s.ui.generation + 1, error: null },
  }
}

export function beginTurn(s: State, turnId: string, now: number): State {
  const o = s.observation
  if (s.identity === null || s.identity.version !== TARGET_VERSION || !nonempty(turnId, 512) || !time(now)
    || o.frozen || o.seenTurnIds.includes(turnId) || o.pendingTurnId === turnId) return s
  if (o.seenTurnIds.length >= MAX_SEEN_TURNS) return { ...s, observation: { ...o, frozen: true } }
  return {
    ...s, warning: o.pendingTurnId === null ? s.warning : '前一主回合未完整收口，观察窗口可能不完整',
    observation: { ...o, since: o.since ?? now, pendingTurnId: turnId },
  }
}

function decodeUsage(v: unknown): { counts: Counts; model: string } | null {
  if (!record(v) || !count(v.input_tokens) || !count(v.cache_creation_input_tokens)
    || !count(v.cache_read_input_tokens) || !count(v.output_tokens) || !nonempty(v.model, 256)) return null
  const counts = { input: v.input_tokens, write: v.cache_creation_input_tokens, read: v.cache_read_input_tokens, output: v.output_tokens }
  return countsValid(counts) ? { counts, model: v.model } : null
}

export function finishTurn(s: State, turnId: string, usage: unknown, now: number): State {
  const o = s.observation
  if (s.identity === null || s.identity.version !== TARGET_VERSION || o.frozen || o.pendingTurnId !== turnId
    || o.seenTurnIds.includes(turnId) || !time(now)) return s
  const seenTurnIds = [...o.seenTurnIds, turnId]
  const observation: Observation = {
    ...o, pendingTurnId: null, seenTurnIds,
    until: Math.max(o.until ?? o.since ?? now, now), frozen: seenTurnIds.length >= MAX_SEEN_TURNS,
  }
  if (usage === undefined || usage === null) {
    return { ...s, warning: '部分主回合未提供用量，以下仅统计有效样本', observation: { ...observation, missingTurns: o.missingTurns + 1 } }
  }
  const sample = decodeUsage(usage)
  const counts: Counts | null = sample === null ? null : {
    input: o.counts.input + sample.counts.input, write: o.counts.write + sample.counts.write,
    read: o.counts.read + sample.counts.read, output: o.counts.output + sample.counts.output,
  }
  if (sample === null || counts === null || !countsValid(counts)) {
    return { ...s, warning: '部分主回合用量非法或超出安全计数范围，以下仅统计有效样本', observation: { ...observation, invalidTurns: o.invalidTurns + 1 } }
  }
  return {
    ...s, observation: {
      ...observation, counts, validTurns: o.validTurns + 1,
      models: o.models.includes(sample.model) ? o.models : [...o.models, sample.model],
    },
  }
}

export function retireSession(s: State): State {
  return {
    ...s, identity: null, observation: emptyObservation(s.observation.epoch + 1),
    workflow: null, nativeUsage: null, warning: null,
    ui: { ...s.ui, busy: false, generation: s.ui.generation + 1, error: null },
  }
}

export function cacheHit(s: State): number | null {
  const c = s.observation.counts
  const total = c.input + c.write + c.read
  return s.observation.validTurns === 0 || !count(total) || total === 0 ? null : c.read / total
}

export function projectNativeUsage(id: Identity, raw: unknown, now: number): NativeView {
  const value = record(raw) ? raw : {}
  const context = record(value.context) ? value.context : {}
  return {
    sessionId: id.sessionId, startedAt: id.startedAt, observedAt: now,
    tokens: count(context.tokens) ? context.tokens : null,
    window: count(context.window) && context.window > 0 ? context.window : null,
    percent: percent(context.percent) ? context.percent : null,
    cost: { kind: 'unavailable', reason: value.cost === undefined || value.cost === null ? 'host-cost-unavailable' : 'pricing-coverage-unknown' },
  }
}
