import type {
  MulixWorkbenchWorkflowFields,
  MulixWorkbenchWorkflowSnapshot,
} from '../types'

export type MulixWorkbenchWorkflowIO = {
  root(): Promise<string>
  stat(path: string): Promise<{ kind: 'file' | 'dir' | 'other' }>
  run(argv: readonly string[], init: { cwd: string; timeoutMs: number }): Promise<{
    exitCode: number
    stdout: string
    stderr: string
    isStdoutTruncated: boolean
    isStderrTruncated: boolean
  }>
  now(): Promise<number>
}

const forbidden = /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f\u{61c}\u{200e}-\u{200f}\u{2028}-\u{202e}\u{2066}-\u{2069}]/u
const phases: readonly string[] = ['specify', 'clarify', 'design', 'tasks', 'build', 'verify', 'archive']

export function parseWorkflowOutput(text: string): MulixWorkbenchWorkflowFields | null {
  if (text.length === 0 || text.length > 8192 || forbidden.test(text)) return null
  const fields = new Map<string, string>()
  for (const line of text.split(/\r?\n/)) {
    const match = /^\s*(change|phase|design|verify_result):\s*(.*)$/.exec(line)
    if (match === null) continue
    const key = match[1]
    const value = match[2]
    if (key === undefined || value === undefined || fields.has(key)) return null
    fields.set(key, value.trim())
  }
  const change = fields.get('change')
  const phase = fields.get('phase')
  const design = fields.get('design')
  const verify = fields.get('verify_result')
  if (!change || change.length > 256 || !phase || !phases.includes(phase) || design === undefined || verify === undefined) return null
  const approval = /^track=(bounded|architectural|)\s+approved=(true|false)\s+path=.*$/.exec(design)
  const result = /^(pending|pass|fail)(?:\s+\(failures:\s*\d+\))?$/.exec(verify)
  if (approval === null || result === null || result[1] === undefined) return null
  return {
    change,
    phase: phase as MulixWorkbenchWorkflowFields['phase'],
    designApproved: approval[2] === 'true',
    verify: result[1] as MulixWorkbenchWorkflowFields['verify'],
  }
}

async function observationTime(io: MulixWorkbenchWorkflowIO): Promise<number> {
  try {
    const value = await io.now()
    if (!Number.isSafeInteger(value) || value < 0 || value > 8_640_000_000_000_000) throw new Error()
    return value
  } catch {
    throw new Error('工作流观察时钟不可用')
  }
}

async function projectRoot(io: MulixWorkbenchWorkflowIO): Promise<string> {
  try {
    const root = await io.root()
    if (typeof root !== 'string' || root.length === 0 || root.length > 4096 || forbidden.test(root)
      || !/^(?:[A-Za-z]:[\\/]|\/)/.test(root)) throw new Error()
    return root
  } catch {
    throw new Error('工作流根目录不可用')
  }
}

export async function queryWorkflow(io: MulixWorkbenchWorkflowIO): Promise<MulixWorkbenchWorkflowSnapshot> {
  const observedAt = await observationTime(io)
  const root = await projectRoot(io)
  const unavailable = (reason: string, sourceRoot = root): MulixWorkbenchWorkflowSnapshot => ({
    root: sourceRoot, observedAt, fields: null, reason, stale: false,
  })
  try {
    const dir = root.replace(/[\\/]+$/, '') + '/.mulix'
    if ((await io.stat(dir)).kind !== 'dir') return unavailable('当前根目录未提供 .mulix 工作流目录')
    const result = await io.run(['mulix', 'state', 'show'], { cwd: root, timeoutMs: 2000 })
    const currentRoot = await projectRoot(io)
    if (currentRoot !== root) return unavailable('根目录已变化，旧查询结果已丢弃', currentRoot)
    if (result.isStdoutTruncated || result.isStderrTruncated) return unavailable('工作流查询输出被截断，未采信状态')
    if (result.exitCode !== 0) {
      if (/\bno active change\b/i.test(result.stderr)) return unavailable('当前没有 active change')
      const code = Number.isSafeInteger(result.exitCode) ? String(result.exitCode) : '未知'
      return unavailable(`工作流查询失败（退出码 ${code}）`)
    }
    const fields = parseWorkflowOutput(result.stdout)
    return fields === null ? unavailable('工作流状态协议未知或字段不完整，未采信状态') : {
      root, observedAt, fields, reason: null, stale: false,
    }
  } catch (error) {
    if (error instanceof Error && error.message === '工作流根目录不可用') throw error
    return unavailable('工作流目录或工具不可用，或查询超时')
  }
}
