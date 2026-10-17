import { mock } from 'claude-code/testing'
import type {
  CommandInfo, On, PaneOpenArgs,
  ProcessRunResult, SessionUsage, UiOpenResult, UiPane,
} from 'claude-code'
import type { MulixWorkbenchIdentity } from '../types'
import { freshState } from '../hooks/model'

const good = 'change: c1\nphase: tasks\ndesign: track=architectural approved=true path=x\nverify_result: pending (failures: 0)\n'
const result = (): ProcessRunResult => ({ exitCode: 0, stdout: good, stderr: '', isStdoutTruncated: false, isStderrTruncated: false })

type Deferred = {
  promise: Promise<ProcessRunResult>
  entered: () => void
}

export function installHost(on: On) {
  const registered: string[] = []
  const opens: PaneOpenArgs[] = []
  const closes: unknown[] = []
  const processCalls: unknown[] = []
  const messages: string[] = []
  let latest = freshState()
  let latestVersion = 0
  let identity: MulixWorkbenchIdentity = { sessionId: 'native-1', startedAt: 1, root: 'C:/repo', version: '2.1.302' }
  let usage: SessionUsage = { startedAt: 1, context: { window: 1000 }, rateLimits: [], cost: { usd: 0 } }
  let commands: CommandInfo[] = []
  let placed: UiOpenResult = { isPlaced: true }
  let panes: UiPane[] = []
  let pending: Deferred | undefined
  const clock = mock.clock(on, { now: 1000 })
  mock.session(on)

  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('state.get', { plugin: 'mulix-workbench', key: 'workbench' }, async (_, e, next) => {
    const result = await next(e)
    if ('value' in result && result.value !== undefined && result.value.value !== undefined && result.value.version >= latestVersion) {
      latestVersion = result.value.version
      latest = JSON.parse(JSON.stringify(result.value.value)) as typeof latest
    }
    return result
  })
  on('state.set', { plugin: 'mulix-workbench', key: 'workbench' }, async (_, e, next) => {
    const written = await next(e)
    if ('value' in written && written.value !== undefined && written.value.isSet && written.value.version >= latestVersion) {
      latestVersion = written.value.version
      latest = JSON.parse(JSON.stringify(e.value)) as typeof latest
    }
    return written
  })
  on('turn.start', (_, e) => ({ turnId: e.turnId }))
  on('turn.complete', (_, e) => ({ text: e.answer, ...(e.usage === undefined ? {} : { usage: e.usage }) }))
  on('session.end', (_, e) => ({ sessionId: e.sessionId }))
  on('session.id', () => ({ value: identity.sessionId }))
  on('session.root', () => ({ value: identity.root }))
  on('session.version', () => ({ value: { version: identity.version } }))
  on('session.usage', (_, e) => {
    if (e.breakdown !== undefined) throw new Error('forbidden token-count/breakdown request')
    return { value: usage }
  })
  on('command.list', () => ({ value: commands }))
  on('command.register', (_, e) => {
    registered.push(e.name)
    commands = commands.filter(c => c.name !== e.name)
    commands.push({ name: e.name, description: e.description, source: 'plugin', plugin: 'mulix-workbench' })
    return { value: { command: e.name } }
  })
  on('fs.stat', () => ({ value: { kind: 'dir', size: 0, mtimeMs: 0, isLink: false } }))
  on('ui.open', (_, e) => {
    opens.push({ ...e })
    panes = panes.filter(p => p.id !== e.id)
    panes.push({ id: e.id, title: e.title ?? e.id, isShown: placed.isPlaced, isFocused: false, isPlaced: placed.isPlaced })
    return { value: placed }
  })
  on('ui.close', (_, e) => {
    closes.push({ id: e.id, origin: e.origin })
    panes = panes.filter(p => p.id !== e.id)
    return { value: undefined }
  })
  on('ui.log', (_, e) => {
    if (e.to !== 'debug') messages.push(e.text)
    return { value: undefined }
  })
  on('ui.panes', () => ({ value: panes }))
  on('process.run', async (_, e) => {
    processCalls.push({ argv: [...e.argv], init: e.init })
    if (pending === undefined) return { value: result() }
    const held = pending
    pending = undefined
    held.entered()
    return { value: await held.promise }
  })

  const forbidden = () => { throw new Error('forbidden workbench effect') }
  on('fs.write', forbidden)
  on('store.*', forbidden)
  on('http.*', forbidden)
  on('model.*', forbidden)
  on('tool.*', forbidden)
  on('prompt.*', forbidden)
  on('config.*', forbidden)
  on('agent.*', forbidden)
  on('session.append', { door: 'note' }, forbidden)
  on('session.send', forbidden)
  on('session.compact', forbidden)
  on('ui.notice', forbidden)
  on('ui.toast', forbidden)
  on('ui.status', forbidden)

  return {
    registered, opens, closes, processCalls, messages,
    advance: (ms: number) => clock.advance(ms),
    async state() { return latest },
    setIdentity(patch: Partial<MulixWorkbenchIdentity>) {
      identity = { ...identity, ...patch }
      usage = { ...usage, startedAt: identity.startedAt }
    },
    setCommands(value: CommandInfo[]) { commands = value.map(c => ({ ...c })) },
    setPlaced(value: UiOpenResult) { placed = value },
    setPanes(value: UiPane[]) { panes = value.map(p => ({ ...p })) },
    setUsage(value: SessionUsage) { usage = value },
    deferProcess() {
      let enter: () => void = () => {}
      let finish: (value: ProcessRunResult) => void = () => {}
      const entered = new Promise<void>(resolve => { enter = resolve })
      const promise = new Promise<ProcessRunResult>(resolve => { finish = resolve })
      pending = { promise, entered: enter }
      return { entered, finish }
    },
  }
}
