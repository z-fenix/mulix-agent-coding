import { test, expect } from 'claude-code/testing'
import {
  freshState, restoreState, bindIdentity, beginTurn, finishTurn,
  retireSession, cacheHit, projectNativeUsage, MAX_SEEN_TURNS,
} from '../hooks/model'

const id = { sessionId: 'native-1', startedAt: 1, root: 'C:/repo', version: '2.1.301' }
const u = (input: number, read: number, output: number) => ({
  input_tokens: input, cache_creation_input_tokens: 0,
  cache_read_input_tokens: read, output_tokens: output, model: 'fixture-model',
})

test('weighted input and exact turn ownership', () => {
  let s = bindIdentity(freshState(), id)
  s = finishTurn(s, 'unseen', u(1, 9, 1), 1)
  expect(s.observation.validTurns).toBe(0)
  s = finishTurn(beginTurn(s, 't1', 10), 't1', u(90, 10, 5), 11)
  s = finishTurn(beginTurn(s, 't2', 20), 't2', { ...u(0, 900, 20), model: 'second-model' }, 21)
  expect(s.observation.models).toEqual(['fixture-model', 'second-model'])
  expect(s.observation.counts).toEqual({ input: 90, write: 0, read: 910, output: 25 })
  expect(cacheHit(s)).toBe(0.91)
  expect(finishTurn(s, 't2', u(0, 900, 20), 22)).toEqual(s)
  expect(bindIdentity(s, { ...id, root: 'C:/other' }).observation.counts).toEqual(s.observation.counts)
  expect(bindIdentity(s, { ...id, sessionId: 'native-2' }).observation.validTurns).toBe(0)
  expect(retireSession(s).observation.pendingTurnId).toBe(null)
  expect(restoreState(JSON.parse(JSON.stringify(s))).observation.counts).toEqual(s.observation.counts)
  expect(restoreState({ schemaVersion: 0 }).warning).not.toBe(null)
})

test('unknown is not zero and cost has no amount', () => {
  let s = bindIdentity(freshState(), id)
  expect(cacheHit(s)).toBe(null)
  s = finishTurn(beginTurn(s, 'zero', 1), 'zero', u(0, 0, 0), 2)
  expect(cacheHit(s)).toBe(null)
  s = finishTurn(beginTurn(s, 'missing', 3), 'missing', undefined, 4)
  expect(s.observation.missingTurns).toBe(1)
  for (const bad of [-1, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) {
    s = finishTurn(beginTurn(s, `bad-${String(bad)}`, 5), `bad-${String(bad)}`, u(bad, 0, 0), 6)
  }
  expect(s.observation.invalidTurns).toBe(4)
  for (const cost of [undefined, { usd: 0 }, { usd: 12.5 }]) {
    const v = projectNativeUsage(id, { startedAt: 1, context: { tokens: 0, window: 1000, percent: 0 }, rateLimits: [], cost }, 9)
    expect(v.tokens).toBe(0)
    expect(v.cost.kind).toBe('unavailable')
    expect(Object.hasOwn(v.cost, 'usd')).toBe(false)
    expect(JSON.stringify(v).includes('"usd"')).toBe(false)
    expect(JSON.stringify(v).includes('12.5')).toBe(false)
    expect(v.cost.reason).toBe(cost === undefined ? 'host-cost-unavailable' : 'pricing-coverage-unknown')
  }
})

test('bounded window stops rather than evicts dedupe', { timeoutMs: 30000 }, () => {
  let s = bindIdentity(freshState(), id)
  for (let n = 0; n < MAX_SEEN_TURNS; n++) {
    s = finishTurn(beginTurn(s, `t-${n}`, n), `t-${n}`, u(1, 0, 0), n)
  }
  const counts = s.observation.counts
  s = finishTurn(beginTurn(s, 'beyond', 5000), 'beyond', u(10, 0, 0), 5001)
  expect(s.observation.frozen).toBe(true)
  expect(s.observation.counts).toEqual(counts)
  expect(finishTurn(s, 't-0', u(100, 0, 0), 5002).observation.counts).toEqual(counts)
})

test('overflow and retired pending turn never alter valid totals', () => {
  let s = bindIdentity(freshState(), id)
  s = finishTurn(beginTurn(s, 'max', 1), 'max', u(Number.MAX_SAFE_INTEGER, 0, 0), 2)
  s = finishTurn(beginTurn(s, 'overflow', 3), 'overflow', u(1, 0, 0), 4)
  expect(s.observation.counts.input).toBe(Number.MAX_SAFE_INTEGER)
  expect(s.observation.invalidTurns).toBe(1)
  const waiting = beginTurn(s, 'old', 5)
  const retired = bindIdentity(retireSession(waiting), { ...id, sessionId: 'native-2', startedAt: 2 })
  expect(finishTurn(retired, 'old', u(1, 0, 0), 6)).toEqual(retired)
  const moved = bindIdentity(s, { ...id, root: 'C:/other' })
  expect(moved.workflow).toBe(null)
  expect(moved.ui.generation).toBeGreaterThan(s.ui.generation)
})
