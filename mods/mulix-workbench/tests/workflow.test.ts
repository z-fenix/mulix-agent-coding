import { test, expect } from 'claude-code/testing'
import { parseWorkflowOutput, queryWorkflow } from '../hooks/workflow'

const good = 'change: c1\nphase: tasks\ndesign: track=architectural approved=true path=x\nverify_result: pending (failures: 0)\n'

test('workflow protocol never guesses approval', () => {
  expect(parseWorkflowOutput(good)).toEqual({ change: 'c1', phase: 'tasks', designApproved: true, verify: 'pending' })
  for (const bad of [good + 'phase: build\n', good.replace('tasks', 'future'), good.replace('pending', 'ok'), good.replace('approved=true', 'approved='), good.replace('change: c1\n', ''), good + '\x1b[31m']) {
    expect(parseWorkflowOutput(bad)).toBe(null)
  }
  const projected = parseWorkflowOutput(good + 'credential: DO_NOT_RENDER\n')
  expect(JSON.stringify(projected).includes('DO_NOT_RENDER')).toBe(false)
})

test('fixed argv, root check and bounded I/O', async () => {
  const calls: unknown[] = []
  let root = 'C:/repo'
  const io = {
    root: async () => root,
    stat: async () => ({ kind: 'dir' as const }),
    now: async () => 7,
    run: async (argv: readonly string[], init: { cwd: string; timeoutMs: number }) => {
      calls.push({ argv, init })
      return { exitCode: 0, stdout: good, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
    },
  }
  expect((await queryWorkflow(io)).fields?.phase).toBe('tasks')
  expect(calls).toEqual([{ argv: ['mulix', 'state', 'show'], init: { cwd: 'C:/repo', timeoutMs: 2000 } }])
  expect((await queryWorkflow({ ...io, stat: async () => ({ kind: 'file' as const }) })).fields).toBe(null)
  expect(calls.length).toBe(1)
  expect((await queryWorkflow({ ...io, run: async () => { throw new Error('SECRET') } })).reason).not.toBe(null)
  const failed = await queryWorkflow({ ...io, run: async () => ({ exitCode: 1, stdout: '', stderr: 'SECRET', isStdoutTruncated: false, isStderrTruncated: false }) })
  expect(JSON.stringify(failed).includes('SECRET')).toBe(false)
  const cut = await queryWorkflow({ ...io, run: async () => ({ exitCode: 0, stdout: good, stderr: '', isStdoutTruncated: true, isStderrTruncated: false }) })
  expect(cut.fields).toBe(null)
  const moved = await queryWorkflow({ ...io, run: async () => {
    root = 'C:/other'
    return { exitCode: 0, stdout: good, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }
  } })
  expect(moved.fields).toBe(null)
})

test('CRLF and exact design grammar are bounded', () => {
  expect(parseWorkflowOutput(good.replace(/\n/g, '\r\n'))?.change).toBe('c1')
  for (const bad of ['', good + 'x'.repeat(8193), good.replace('architectural', 'future'), good.replace('approved=true', 'approved=true approved=false'), good.replace('verify_result: pending (failures: 0)\n', '')]) {
    expect(parseWorkflowOutput(bad)).toBe(null)
  }
})

test('stderr truncation is also unavailable', async () => {
  const r = await queryWorkflow({
    root: async () => 'C:/repo', stat: async () => ({ kind: 'dir' as const }), now: async () => 7,
    run: async () => ({ exitCode: 0, stdout: good, stderr: 'HIDDEN', isStdoutTruncated: false, isStderrTruncated: true }),
  })
  expect(r.fields).toBe(null)
  expect(JSON.stringify(r).includes('HIDDEN')).toBe(false)
})

test('unavailable identity or clock never fabricates a snapshot', async () => {
  const io = {
    root: async () => 'C:/repo', stat: async () => ({ kind: 'dir' as const }), now: async () => 7,
    run: async () => ({ exitCode: 0, stdout: good, stderr: '', isStdoutTruncated: false, isStderrTruncated: false }),
  }
  for (const [input, message] of [
    [{ ...io, root: async () => { throw new Error('SECRET') } }, '工作流根目录不可用'],
    [{ ...io, now: async () => { throw new Error('SECRET') } }, '工作流观察时钟不可用'],
  ] as const) {
    let error = ''
    try { await queryWorkflow(input) } catch (e) { error = e instanceof Error ? e.message : '' }
    expect(error).toBe(message)
    expect(error.includes('SECRET')).toBe(false)
  }
})
