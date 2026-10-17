export type MulixWorkbenchIdentity = {
  sessionId: string
  startedAt: number
  root: string
  version: string
}

export type MulixWorkbenchCounts = {
  input: number
  write: number
  read: number
  output: number
}

export type MulixWorkbenchWorkflowFields = {
  change: string
  phase: 'specify' | 'clarify' | 'design' | 'tasks' | 'build' | 'verify' | 'archive'
  designApproved: boolean
  verify: 'pending' | 'pass' | 'fail'
}

export type MulixWorkbenchWorkflowSnapshot = {
  root: string
  observedAt: number
  fields: MulixWorkbenchWorkflowFields | null
  reason: string | null
  stale: boolean
}

export type MulixWorkbenchNativeView = {
  sessionId: string
  startedAt: number
  observedAt: number
  tokens: number | null
  window: number | null
  percent: number | null
  cost: {
    kind: 'unavailable'
    reason: 'pricing-coverage-unknown' | 'host-cost-unavailable'
  }
}

export type MulixWorkbenchObservation = {
  epoch: number
  since: number | null
  until: number | null
  pendingTurnId: string | null
  seenTurnIds: string[]
  validTurns: number
  missingTurns: number
  invalidTurns: number
  counts: MulixWorkbenchCounts
  models: string[]
  frozen: boolean
}

export type MulixWorkbenchState = {
  schemaVersion: 1
  identity: MulixWorkbenchIdentity | null
  observation: MulixWorkbenchObservation
  workflow: MulixWorkbenchWorkflowSnapshot | null
  nativeUsage: MulixWorkbenchNativeView | null
  warning: string | null
  ui: {
    view: 'workflow' | 'usage'
    open: boolean
    placed: boolean
    busy: boolean
    generation: number
    error: string | null
  }
}

declare module 'claude-code' {
  interface PluginState {
    'mulix-workbench': {
      workbench: MulixWorkbenchState
    }
  }
}
