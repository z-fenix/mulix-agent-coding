import { atom, read, update } from 'claude-code'
import type { EngineInterface, Register, SessionUsage } from 'claude-code'
import type { MulixWorkbenchIdentity, MulixWorkbenchState, MulixWorkbenchWorkflowSnapshot } from '../types'
import { TARGET_VERSION, freshState, restoreState, bindIdentity, beginTurn, finishTurn, retireSession, projectNativeUsage } from './model'
import { queryWorkflow } from './workflow'
import { renderWorkbench } from './view'

const PANE = 'mulix-workbench'
const state = atom({ plugin: 'mulix-workbench', key: 'workbench' } as const, freshState())
type Context = { identity: MulixWorkbenchIdentity; usage: SessionUsage; now: number }
type View = MulixWorkbenchState['ui']['view']

async function context($: EngineInterface): Promise<Context> {
  const first = await $.session.id()
  const [root, version, usage] = await Promise.all([$.session.root(), $.session.version(), $.session.usage()])
  const last = await $.session.id()
  const now = await $.clock.now()
  const identity = { sessionId: first, startedAt: usage.startedAt, root, version: version.version }
  if (first !== last || bindIdentity(freshState(), identity).identity === null
    || !Number.isSafeInteger(now) || now < 0 || now > 8_640_000_000_000_000) throw new Error('原生会话快照不可用')
  return { identity, usage, now }
}

function sameIdentity(a: MulixWorkbenchIdentity | null, b: MulixWorkbenchIdentity): boolean {
  return a !== null && a.sessionId === b.sessionId && a.startedAt === b.startedAt && a.root === b.root && a.version === b.version
}

async function notify($: EngineInterface, text: string): Promise<void> {
  await $.ui.log(text)
}

async function retirePane($: EngineInterface): Promise<void> {
  await update($, state, s => ({ ...s, ui: { ...s.ui, open: false, placed: false, busy: false, generation: s.ui.generation + 1 } }))
}

async function closePane($: EngineInterface): Promise<void> {
  await retirePane($)
  await $.ui.close({ id: PANE })
}

let interactive = false
const owned = new Set<string>()
let flight: object | null = null

  async function belongs($: EngineInterface, name: string): Promise<boolean> {
    if (!owned.has(name)) return false
    const command = (await $.command.list()).find(c => c.name === name)
    return command?.source === 'plugin' && command.plugin === $.plugin.name
  }

  async function fail($: EngineInterface, generation: number, message: string): Promise<void> {
    await update($, state, s => s.ui.generation === generation ? {
      ...s,
      workflow: s.workflow?.fields ? { ...s.workflow, stale: true, reason: message } : s.workflow,
      ui: { ...s.ui, busy: false, error: message },
    } : s)
    if ((await read($, state)).ui.generation === generation) await notify($, message)
  }

  async function refresh($: EngineInterface): Promise<void> {
    let initial = await read($, state)
    if (!initial.ui.open) return
    if (!initial.ui.placed) {
      const reconcileGeneration = initial.ui.generation
      const pane = (await $.ui.panes()).find(p => p.id === PANE)
      if (!pane?.isPlaced) return
      await update($, state, s => s.ui.open && s.ui.generation === reconcileGeneration
        ? { ...s, ui: { ...s.ui, placed: true, error: null } } : s)
      initial = await read($, state)
      if (!initial.ui.open || !initial.ui.placed || initial.ui.generation !== reconcileGeneration) return
    }
    const view = initial.ui.view
    if (view === 'workflow' && flight !== null) return
    const ticket = {}
    if (view === 'workflow') flight = ticket
    const generation = initial.ui.generation
    try {
      const before = await context($)
      if (before.identity.version !== TARGET_VERSION) {
        await fail($, generation, 'CLI 版本未验证或不可用；工作台仅支持 2.1.301')
        return
      }
      await update($, state, s => {
        if (s.ui.generation !== generation || !s.ui.open) return s
        const bound = bindIdentity(s, before.identity)
        return {
          ...bound, nativeUsage: projectNativeUsage(before.identity, before.usage, before.now),
          ui: { ...bound.ui, busy: view === 'workflow' && bound.ui.generation === generation, error: null },
        }
      })
      const ready = await read($, state)
      if (ready.ui.generation !== generation || !ready.ui.open || !sameIdentity(ready.identity, before.identity)) return
      if (view === 'usage') return

      let snapshot: MulixWorkbenchWorkflowSnapshot | null = null
      try {
        snapshot = await queryWorkflow({
          root: async () => {
            const root = await $.session.root()
            if (root !== before.identity.root) throw new Error('查询目标已变化')
            return root
          },
          stat: path => $.fs.stat(path),
          run: (argv, init) => $.process.run(argv, init),
          now: () => $.clock.now(),
        })
      } catch {
        // No dependency error text is copied into UI/state.
      }
      const after = await context($)
      await update($, state, s => {
        if (s.ui.generation !== generation || !s.ui.open) return s
        const bound = bindIdentity(s, after.identity)
        const nativeUsage = projectNativeUsage(after.identity, after.usage, after.now)
        if (bound.ui.generation !== generation || !sameIdentity(bound.identity, before.identity)) return { ...bound, nativeUsage }
        const next = snapshot?.root === before.identity.root ? snapshot : null
        const error = next?.reason ?? (next?.fields ? null : '工作流查询不可用，未采信结果')
        const previous = bound.workflow
        const workflow = next?.fields ? next : previous?.root === before.identity.root && previous.fields ? {
          ...previous, stale: true, reason: error,
        } : next
        return { ...bound, nativeUsage, workflow, ui: { ...bound.ui, busy: false, error } }
      })
    } catch {
      await fail($, generation, '原生工作台查询不可用，请稍后刷新')
    } finally {
      if (flight === ticket) flight = null
      await update($, state, s => s.ui.generation === generation && s.ui.busy ? { ...s, ui: { ...s.ui, busy: false } } : s)
    }
  }

  async function open($: EngineInterface, view: View, args: string): Promise<void> {
    if (args.trim() !== '') {
      await notify($, '只读工作台不支持命令参数；未执行查询')
      return
    }
    if (!interactive) {
      await notify($, '原生终端工作台不可用；没有可绘制的交互终端')
      return
    }
    const original = await read($, state)
    const current = await context($)
    if (current.identity.version !== TARGET_VERSION) {
      await notify($, 'CLI 版本未验证或不可用；工作台仅支持 2.1.301')
      return
    }
    await update($, state, s => {
      if (s.observation.epoch !== original.observation.epoch || s.ui.generation !== original.ui.generation) return s
      const bound = bindIdentity(s, current.identity)
      return {
        ...bound, nativeUsage: projectNativeUsage(current.identity, current.usage, current.now),
        ui: { ...bound.ui, view, open: true, placed: false, busy: false, generation: bound.ui.generation + 1, error: null },
      }
    })
    const pending = await read($, state)
    if (!sameIdentity(pending.identity, current.identity) || !pending.ui.open || pending.ui.view !== view) return
    const generation = pending.ui.generation
    const placed = await $.ui.open({ id: PANE, title: 'mulix 只读工作台', focus: true, closeOnEscape: true, rows: 22 })
    await update($, state, s => s.ui.generation === generation && s.ui.open ? {
      ...s, ui: { ...s.ui, placed: placed.isPlaced, error: placed.isPlaced ? null : '原生面板不可用或未放置，未执行查询' },
    } : s)
    const showing = await read($, state)
    if (showing.ui.generation !== generation || !showing.ui.open) return
    if (!placed.isPlaced) {
      await notify($, '原生面板不可用或未放置，未执行查询')
      return
    }
    await refresh($)
  }

  async function select($: EngineInterface, view: View): Promise<void> {
    await update($, state, s => ({ ...s, ui: { ...s.ui, view, busy: false, generation: s.ui.generation + 1, error: null } }))
    await refresh($)
  }

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    interactive = e.isInteractive && e.surface === 'terminal'
    owned.clear()
    const result = await next(e)
    const commands = await $.command.list()
    const free = (name: string) => {
      const existing = commands.find(c => c.name === name)
      return existing === undefined || existing.source === 'plugin' && existing.plugin === $.plugin.name
    }
    if (free('mulix-status')) {
      await $.command.register({ name: 'mulix-status', description: '只读查看当前仓库的 mulix 工作流状态' })
      owned.add('mulix-status')
    }
    if (free('mulix-cache')) {
      await $.command.register({ name: 'mulix-cache', description: '只读查看当前原生主会话用量与观察窗口' })
      owned.add('mulix-cache')
    }
    const original = await read($, state)
    const current = await context($)
    const panes = await $.ui.panes()
    const pane = panes.find(p => p.id === PANE)
    await update($, state, s => {
      if (s.observation.epoch !== original.observation.epoch) return s
      const bound = bindIdentity(restoreState(s), current.identity)
      return {
        ...bound, nativeUsage: projectNativeUsage(current.identity, current.usage, current.now),
        ui: { ...bound.ui, open: pane !== undefined, placed: pane?.isPlaced ?? false, busy: false, generation: bound.ui.generation + 1 },
      }
    })
    return result
  }).catch(async ($, e, next) => {
    await notify($, '原生工作台初始化不可用；主会话执行不受此影响')
    return next(e)
  })

  on('command.run', { command: 'mulix-status' }, async ($, e, next) => {
    if (!await belongs($, e.command)) return next(e)
    if (e.origin.kind !== 'composer') return {}
    await open($, 'workflow', e.args)
    return {}
  }).catch(async ($, e, next) => {
    if (next.called || !owned.has(e.command)) return next(e)
    await notify($, '工作流只读面板不可用')
    return {}
  })

  on('command.run', { command: 'mulix-cache' }, async ($, e, next) => {
    if (!await belongs($, e.command)) return next(e)
    if (e.origin.kind !== 'composer') return {}
    await open($, 'usage', e.args)
    return {}
  }).catch(async ($, e, next) => {
    if (next.called || !owned.has(e.command)) return next(e)
    await notify($, '原生用量只读面板不可用')
    return {}
  })

  on('turn.start', async ($, e, next) => {
    if (e.agentId !== undefined) return next(e)
    const original = await read($, state)
    const result = await next(e)
    const current = await context($)
    await update($, state, s => s.observation.epoch === original.observation.epoch
      ? beginTurn(bindIdentity(s, current.identity), e.turnId, current.now) : s)
    return result
  })

  on('turn.complete', async ($, e, next) => {
    if (e.agentId !== undefined) return next(e)
    const original = await read($, state)
    const result = await next(e)
    if (original.observation.pendingTurnId !== e.turnId) return result
    const current = await context($)
    await update($, state, s => {
      if (s.observation.epoch !== original.observation.epoch || s.observation.pendingTurnId !== e.turnId) return s
      const bound = bindIdentity(s, current.identity)
      if (bound.observation.epoch !== original.observation.epoch) return bound
      return finishTurn(bound, e.turnId, e.usage, current.now)
    })
    return result
  })

  on('session.end', async ($, e, next) => {
    await update($, state, retireSession)
    return next(e)
  })

  on('ui.close', { id: PANE }, async ($, e, next) => {
    await retirePane($)
    return next(e)
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e, next) => {
    if (e.surface !== 'terminal') return next(e)
    const snapshot = await read($, state)
    return renderWorkbench($, e, snapshot, {
      refresh: () => refresh($),
      select: view => select($, view),
      close: () => closePane($),
    })
  })
}
