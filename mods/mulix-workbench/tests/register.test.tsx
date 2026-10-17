import { test, expect } from 'claude-code/testing'
import type { MountTarget, Mounted } from 'claude-code/testing'
import { installHost } from './host-fixture'

const command = (name: string) => ({ command: name, args: '', origin: { kind: 'composer' as const }, presentation: { isFullscreen: false, columns: 100 } })
const pane = (width: number): MountTarget<'terminal', 'Pane'> => ({
  plugin: 'mulix-workbench', surface: 'terminal', component: 'Pane', requestId: 'mulix-workbench',
  props: { title: 'mulix', isFocused: true, bodyColumns: width, placement: 'inline', scroll: { offset: 0, bodyRows: 20 }, view: {} },
  viewport: { columns: width, rows: 30, isFullscreen: false },
})
const labels: Record<string, RegExp> = {
  'usage-provider': /^提供方：/,
  'usage-session': /^原生主会话：/,
  'usage-context': /^上下文：/,
  'usage-window': /^观察窗口：/,
  'usage-valid-turns': /^有效用量回合：/,
  'usage-input': /^未缓存输入：/,
  'usage-write': /^缓存写入：/,
  'usage-read': /^缓存读取：/,
  'usage-output': /^输出：/,
  'usage-hit': /^命中率：/,
  'usage-cost': /^费用不可得：/,
  'workflow-source': /^数据源：/,
  'workflow-root': /^仓库根目录：/,
  'workflow-change': /^变更：/,
  'workflow-phase': /^阶段：/,
  'workflow-design': /^设计批准：/,
  'workflow-verify': /^验证结果：/,
  'workflow-observed-at': /^观察时间：/,
  'workflow-stale': /^数据状态：/,
}
const row = async (ui: Mounted<'terminal', 'Pane'>, key: string) => {
  const label = labels[key]
  if (label === undefined) throw new Error(`unknown row ${key}`)
  const found = await ui.find({ type: 'Text', text: label })
  if (found === undefined) throw new Error(`missing visible row ${key}`)
  return found.text ?? ''
}

test('user commands show only the native main-session view', async ($, on) => {
  const h = installHost(on)
  h.setUsage({ startedAt: 1, context: { tokens: 512, window: 4096, percent: 12 }, rateLimits: [], cost: { usd: 12.5 } })
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  expect(h.registered).toEqual(['mulix-status', 'mulix-cache'])
  expect(h.opens.length).toBe(0)
  expect(h.processCalls.length).toBe(0)
  const result = await $.command.run(command('mulix-cache'))
  expect(result.text).toBe(undefined)
  expect(result.context).toBe(undefined)
  for (const width of [60, 120]) {
    const ui = await $.ui.mount(pane(width))
    expect(await row(ui, 'usage-cost')).toMatch(/费用不可得/)
    expect(await row(ui, 'usage-provider')).toMatch(/Claude Code.*原生宿主/)
    expect(await row(ui, 'usage-session')).toMatch(/原生主会话.*native-1/)
    expect(await row(ui, 'usage-context')).toMatch(/上下文.*512.*4,?096.*12\s*%/)
    expect(await row(ui, 'usage-window')).toMatch(/观察窗口/)
    for (const key of ['usage-input', 'usage-write', 'usage-read', 'usage-output']) {
      expect(await row(ui, key)).toMatch(/尚未观察|未提供|不可得/)
    }
    expect(await row(ui, 'usage-hit')).toMatch(/不可计算|尚未观察/)
    await ui.unmount()
  }
  await h.advance(1)
  await $.turn.start({ text: 'fixture', turnId: 't1' })
  await h.advance(1)
  await $.turn.complete({ turnId: 't1', answer: '', durationMs: 1, isAborted: false, reason: 'answer', usage: { model: 'fixture-model', input_tokens: 90, cache_creation_input_tokens: 0, cache_read_input_tokens: 10, output_tokens: 5 } })
  await h.advance(1)
  await $.turn.start({ text: 'fixture', turnId: 't2' })
  await h.advance(1)
  await $.turn.complete({ turnId: 't2', answer: '', durationMs: 1, isAborted: false, reason: 'answer', usage: { model: 'fixture-model', input_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 900, output_tokens: 20 } })
  const usage = await $.ui.mount(pane(100))
  expect(await row(usage, 'usage-input')).toMatch(/未缓存输入.*\b90\b/)
  expect(await row(usage, 'usage-write')).toMatch(/缓存写入.*\b0\b/)
  expect(await row(usage, 'usage-read')).toMatch(/缓存读取.*\b910\b/)
  expect(await row(usage, 'usage-output')).toMatch(/输出.*\b25\b/)
  expect(await row(usage, 'usage-hit')).toMatch(/命中率.*91(?:\.0+)?\s*%/)
  expect(await row(usage, 'usage-valid-turns')).toMatch(/有效用量回合.*\b2\b/)
  const observed = (await h.state()).observation
  expect(await row(usage, 'usage-window')).toContain(new Date(observed.since!).toISOString())
  expect(await row(usage, 'usage-window')).toContain(new Date(observed.until!).toISOString())
  h.setUsage({ startedAt: 1, context: { tokens: 768, window: 4096, percent: 18 }, rateLimits: [], cost: { usd: 12.5 } })
  await usage.press({ key: 'refresh' })
  expect(await row(usage, 'usage-context')).toMatch(/上下文.*768.*4,?096.*18\s*%/)
  expect(await row(usage, 'usage-hit')).toMatch(/91(?:\.0+)?\s*%/)
  expect(await row(usage, 'usage-read')).toMatch(/\b910\b/)
  expect(await row(usage, 'usage-cost')).not.toMatch(/12\.5|\$|USD/)
  expect(h.processCalls.length).toBe(0)
  await usage.unmount()
  const before = await h.state()
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  expect((await h.state()).observation.counts).toEqual(before.observation.counts)
  await $.session.end({ reason: 'clear', sessionId: 'native-1', resume: { id: 'native-1' } })
  h.setIdentity({ sessionId: 'native-2', startedAt: 2 })
  await $.turn.complete({ turnId: 't1', answer: '', durationMs: 1, isAborted: false, reason: 'answer' })
  expect((await h.state()).observation.validTurns).toBe(0)
  expect((await h.state()).identity).toBe(null)
  await $.turn.start({ text: 'new main', turnId: 'new-main' })
  await $.turn.start({ text: 'child', turnId: 'child-turn', agentId: 'child-1' })
  await $.turn.complete({ turnId: 'child-turn', agentId: 'child-1', answer: '', durationMs: 1, isAborted: false, reason: 'answer', usage: { model: 'fixture-model', input_tokens: 999, cache_creation_input_tokens: 0, cache_read_input_tokens: 0, output_tokens: 1 } })
  expect((await h.state()).observation.validTurns).toBe(0)
  expect((await h.state()).identity?.sessionId).toBe('native-2')
  expect((await h.state()).observation.pendingTurnId).toBe('new-main')
})

test('close fences late query and repeated refresh', async ($, on) => {
  const h = installHost(on)
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  const held = h.deferProcess()
  const opening = $.command.run(command('mulix-status'))
  await held.entered
  const ui = await $.ui.mount(pane(100))
  await ui.press({ key: 'refresh' })
  expect(h.processCalls.length).toBe(1)
  await ui.press({ key: 'close' })
  held.finish({ exitCode: 0, stdout: 'change: late\nphase: build\ndesign: track=architectural approved=true path=x\nverify_result: pass (failures: 0)\n', stderr: '', isStdoutTruncated: false, isStderrTruncated: false })
  await opening
  const closed = await h.state()
  if (closed.ui.open) throw new Error(`close did not retire query: ${JSON.stringify({ calls: h.closes, ui: closed.ui })}`)
  expect(closed.ui.open).toBe(false)
  expect(closed.workflow).toBe(null)
  expect(h.opens.length).toBe(1)
  await ui.unmount()
})

test('foreign names and late takeover pass through', async ($, on) => {
  const h = installHost(on)
  h.setCommands([{ name: 'mulix-cache', description: 'other', source: 'plugin', plugin: 'other' }])
  on('command.run', { command: 'mulix-cache' }, () => ({ text: 'foreign-cache' }))
  on('command.run', { command: 'mulix-status' }, () => ({ text: 'foreign-status' }))
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  expect(h.registered).toEqual(['mulix-status'])
  expect((await $.command.run(command('mulix-cache'))).text).toBe('foreign-cache')
  h.setCommands([{ name: 'mulix-status', description: 'user-owned', source: 'user' }])
  expect((await $.command.run(command('mulix-status'))).text).toBe('foreign-status')
  expect(h.opens.length).toBe(0)
})

test('programmatic commands, args and wrong versions do not open', async ($, on) => {
  const h = installHost(on)
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  await $.command.run({ ...command('mulix-cache'), origin: { kind: 'plugin', name: 'other' } })
  await $.command.run({ ...command('mulix-status'), args: '--force; DO_NOT_EXECUTE' })
  for (const version of ['2.1.300', '2.1.302', '']) {
    h.setIdentity({ version })
    await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
    const beforeMessages = h.messages.length
    await $.command.run(command('mulix-cache'))
    expect(h.messages.length).toBeGreaterThan(beforeMessages)
    expect(h.messages.slice(beforeMessages).join('\n')).toMatch(/版本|version|CLI|不可用/)
  }
  expect(h.opens.length).toBe(0)
  expect(h.processCalls.length).toBe(0)
})

test('unplaced commands report unavailable and never query workflow', async ($, on) => {
  const h = installHost(on)
  h.setPlaced({ isPlaced: false, reason: 'no-surface' })
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  const beforeMessages = h.messages.length
  for (const name of ['mulix-status', 'mulix-cache']) {
    const result = await $.command.run(command(name))
    expect(result.text).toBe(undefined)
    expect(result.context).toBe(undefined)
  }
  expect((await h.state()).ui.placed).toBe(false)
  expect(h.messages.length).toBeGreaterThan(beforeMessages)
  expect(h.messages.slice(beforeMessages).join('\n')).toMatch(/不可用|未放置|无法显示|no-surface/)
  expect(h.processCalls.length).toBe(0)
})

test('a pane placed after opening can refresh without reopening the command', async ($, on) => {
  const fixture = installHost(on)
  fixture.setPlaced({ isPlaced: false, reason: 'no-surface' })
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  await $.command.run(command('mulix-status'))
  expect((await fixture.state()).ui.placed).toBe(false)
  expect(fixture.processCalls.length).toBe(0)
  fixture.setPanes([{ id: 'mulix-workbench', title: 'mulix', isShown: true, isFocused: true, isPlaced: true }])
  const ui = await $.ui.mount(pane(100))
  await ui.press({ key: 'refresh' })
  expect(fixture.processCalls.length).toBe(1)
  expect((await fixture.state()).ui.placed).toBe(true)
  expect(await row(ui, 'workflow-change')).toContain('c1')
  expect(fixture.opens.length).toBe(1)
  await ui.unmount()
})

test('missing usage is not zero; agent view is not source scope', async ($, on) => {
  const fixture = installHost(on)
  on('ui.render', { component: 'Pane' }, (host, e) => {
    const { Text } = host.ui.resolve(e)
    return <Text>native fallback</Text>
  })
  fixture.setUsage({ startedAt: 1, context: { window: 1000 }, rateLimits: [] })
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  await $.command.run(command('mulix-cache'))
  const s = await fixture.state()
  expect(s.nativeUsage?.tokens).toBe(null)
  expect(s.nativeUsage?.cost.reason).toBe('host-cost-unavailable')
  const agentPane = pane(100)
  agentPane.props.view = { agentId: 'child-1' }
  const ui = await $.ui.mount(agentPane)
  expect(await row(ui, 'usage-session')).toMatch(/原生主会话.*native-1/)
  expect(await row(ui, 'usage-context')).toMatch(/未提供|不可得/)
  expect(await row(ui, 'usage-cost')).toMatch(/费用不可得.*宿主未提供费用/)
  await ui.unmount()
  const desktop = await $.ui.mount({ ...pane(100), surface: 'desktop' as const })
  expect(await desktop.find({ type: 'Text', text: 'native fallback' })).toBeDefined()
  await desktop.unmount()
})

test('root change drops old data; status rendering follows fresh and stale data', async ($, on) => {
  const h = installHost(on)
  await $.session.start({ cwd: 'C:/repo', surface: 'terminal', isInteractive: true })
  await $.command.run(command('mulix-status'))
  const ui = await $.ui.mount(pane(100))
  expect(await row(ui, 'workflow-source')).toMatch(/mulix.*工作流.*CLI/)
  expect(await row(ui, 'workflow-root')).toContain('C:/repo')
  expect(await row(ui, 'workflow-change')).toContain('c1')
  expect(await row(ui, 'workflow-phase')).toContain('tasks')
  expect(await row(ui, 'workflow-design')).toMatch(/设计.*true/)
  expect(await row(ui, 'workflow-verify')).toMatch(/验证.*pending/)
  expect(await row(ui, 'workflow-stale')).toMatch(/当前/)
  const before = (await h.state()).workflow
  expect(await row(ui, 'workflow-observed-at')).toContain(new Date(before!.observedAt).toISOString())
  await h.advance(1)
  const failed = h.deferProcess()
  const second = $.command.run(command('mulix-status'))
  await failed.entered
  failed.finish({ exitCode: 1, stdout: '', stderr: 'SECRET', isStdoutTruncated: false, isStderrTruncated: false })
  await second
  const stale = (await h.state()).workflow
  expect(stale?.stale).toBe(true)
  expect(stale?.fields).toEqual(before?.fields)
  expect(stale?.observedAt).toBe(before?.observedAt)
  expect(await row(ui, 'workflow-stale')).toMatch(/过期/)
  expect(await row(ui, 'workflow-observed-at')).toContain(new Date(before!.observedAt).toISOString())
  for (const next of [{ change: 'c2', phase: 'verify', approved: true, verify: 'fail' }, { change: 'c3', phase: 'design', approved: false, verify: 'pending' }]) {
    await h.advance(1)
    const update = h.deferProcess()
    const reading = $.command.run(command('mulix-status'))
    await update.entered
    update.finish({ exitCode: 0, stdout: `change: ${next.change}\nphase: ${next.phase}\ndesign: track=architectural approved=${next.approved} path=x\nverify_result: ${next.verify} (failures: ${next.verify === 'fail' ? 1 : 0})\n`, stderr: '', isStdoutTruncated: false, isStderrTruncated: false })
    await reading
    expect(await row(ui, 'workflow-change')).toContain(next.change)
    expect(await row(ui, 'workflow-phase')).toContain(next.phase)
    expect(await row(ui, 'workflow-design')).toContain(String(next.approved))
    expect(await row(ui, 'workflow-verify')).toContain(next.verify)
    expect(await row(ui, 'workflow-stale')).toMatch(/当前/)
  }
  const moved = h.deferProcess()
  const third = $.command.run(command('mulix-status'))
  await moved.entered
  h.setIdentity({ root: 'C:/other' })
  moved.finish({ exitCode: 0, stdout: 'change: old\nphase: build\ndesign: track=architectural approved=true path=x\nverify_result: pass (failures: 0)\n', stderr: '', isStdoutTruncated: false, isStderrTruncated: false })
  await third
  const s = await h.state()
  expect(s.identity?.root).toBe('C:/other')
  expect(s.workflow?.fields ?? null).toBe(null)
  expect(s.ui.busy).toBe(false)
  expect(await row(ui, 'workflow-root')).toContain('C:/other')
  expect(await row(ui, 'workflow-change')).toMatch(/不可得|未提供|未知/)
  expect(await row(ui, 'workflow-change')).not.toMatch(/c1|c2|c3|old/)
  expect(JSON.stringify(s).includes('SECRET')).toBe(false)
  expect(h.messages.join('\n').includes('SECRET')).toBe(false)
  await ui.unmount()
})
