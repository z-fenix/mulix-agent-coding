// App.tsx——布局装配（task-12 交互面接线）：会话事件流 → useTurnState/useApproval
// 推导回合在飞与待审请求；ApprovalBar 独占拦截（在场即冻结 composer——TUI
// 独占语义，动作即清卡）；StatusBar 500ms 轮询快照 + 损坏段警告 + 只读横幅/
// 接管；Composer 回合中禁提交（显示中断）、409 行内提示、未选工作区门禁
// （FR-006/007/011/013）。选中会话 → open（403 = 只读观流，横幅 + 接管）→
// openSessionStream 对账+实时（stream.ts 去重），会话切换即重置转录并断开旧流。
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  ApiError,
  cancelTurn,
  createSession,
  errMessage,
  getWorkspaces,
  listSessions,
  openSession,
  sendApproval,
  submitText,
} from './api'
import Header, { type Health } from './components/Header'
import Sidebar from './components/Sidebar'
import Composer from './components/Composer'
import TranscriptPane from './components/TranscriptPane'
import ApprovalBar from './components/ApprovalBar'
import StatusBar from './components/StatusBar'
import { useTurnState } from './hooks/useTurnState'
import { useApproval } from './hooks/useApproval'
import { openSessionStream } from './stream'
import type { ApprovalDecision, SessionEntry, WireFrame } from './types'

export default function App() {
  const [health, setHealth] = useState<Health>('checking')
  const [sessions, setSessions] = useState<SessionEntry[]>([])
  const [scanWarnings, setScanWarnings] = useState<string[]>([])
  const [workspaces, setWorkspaces] = useState<string[]>([])
  const [workspace, setWorkspace] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [frames, setFrames] = useState<WireFrame[]>([])
  const [streamWarnings, setStreamWarnings] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [readonly, setReadonly] = useState(false)
  const streamCloseRef = useRef<(() => void) | null>(null)

  const running = useTurnState(frames)
  const { pending: approvalPending, clear: clearApproval } = useApproval(frames)

  const refreshSessions = useCallback(() => {
    listSessions()
      .then((r) => {
        setSessions(r.sessions)
        setScanWarnings(r.warnings)
      })
      .catch((e) => setError(errMessage(e)))
  }, [])

  // 挂载期：健康探测 + 工作区清单 + 会话列表。
  useEffect(() => {
    const ctrl = new AbortController()
    fetch('/api/healthz', { signal: ctrl.signal })
      .then((res) => {
        setHealth(res.ok ? 'up' : 'down')
      })
      .catch(() => {
        // 网络失败/中止：中止由卸载触发，不得再改状态；其余按不可达处理。
        if (!ctrl.signal.aborted) setHealth('down')
      })
    getWorkspaces()
      .then((ws) => {
        setWorkspaces(ws)
        setWorkspace((cur) => cur || ws[0] || '')
      })
      .catch((e) => setError(errMessage(e)))
    refreshSessions()
    return () => ctrl.abort()
  }, [refreshSessions])

  // 选中会话：重置转录/交互态 → open（403 = 只读横幅，流仍可观看）→ SSE 对账流。
  useEffect(() => {
    const id = selectedId
    if (!id) return
    setFrames([])
    setStreamWarnings([])
    setError(null)
    setReadonly(false)
    let disposed = false
    ;(async () => {
      try {
        const r = await openSession(id)
        if (disposed) return
        setReadonly(r.role === 'readonly')
      } catch (e) {
        if (disposed) return
        if (e instanceof ApiError && e.status === 403) {
          setReadonly(true) // 只读观流横幅（接管按钮在 StatusBar），不打错误条
        } else {
          setError(errMessage(e))
        }
      }
      if (disposed) return
      const stream = openSessionStream(id, {
        onEvent: (f) => setFrames((prev) => [...prev, f]),
        onWarnings: (w) => setStreamWarnings(w),
      })
      streamCloseRef.current = stream.close
    })()
    return () => {
      disposed = true
      streamCloseRef.current?.()
      streamCloseRef.current = null
    }
  }, [selectedId])

  const handleCreate = useCallback(() => {
    if (!workspace) return
    createSession(workspace)
      .then((r) => {
        refreshSessions()
        setSelectedId(r.id)
      })
      .catch((e) => setError(errMessage(e)))
  }, [workspace, refreshSessions])

  // handleSubmit 返回 Promise<void>——Composer 据此 await 结果：成功清空、
  // 409 行内提示（FR-006）、其余经 onError 上抛错误条。
  const handleSubmit = useCallback(
    (text: string): Promise<void> => {
      if (!selectedId) return Promise.resolve()
      return submitText(selectedId, text).then(() => undefined)
    },
    [selectedId],
  )

  const handleCancel = useCallback(() => {
    if (!selectedId) return
    cancelTurn(selectedId).catch((e) => setError(errMessage(e)))
  }, [selectedId])

  // handleDecide 审批裁决：成功才清卡（.then clearApproval）；失败由拦截条行内
  // 呈现并保留可重试（评审修复轮 1——「动作即清卡」会让 POST 失败时服务端审批
  // 悬等 120s 且无重试通路；TUI 的「动作即清卡」是进程内同步调用、无失败窗口，
  // web 面以「成功清卡」对齐 fail-closed）。在途防重复提交由 ApprovalBar 的
  // busy 状态承担（按钮与键位同门）。
  const handleDecide = useCallback(
    (decision: ApprovalDecision): Promise<void> => {
      if (!selectedId || !approvalPending) return Promise.resolve()
      return sendApproval(selectedId, approvalPending.tool_use_id, decision)
        .then(() => {
          clearApproval()
        })
        .then(() => undefined)
    },
    [selectedId, approvalPending, clearApproval],
  )

  // handleTakeover 显式接管（POST /open takeover）：成功 → 角色回交互；
  // 失败（如未收口 409/跨进程锁）如实呈现。
  const handleTakeover = useCallback(() => {
    if (!selectedId) return
    openSession(selectedId, true)
      .then((r) => {
        setReadonly(r.role === 'readonly')
        setError(null)
      })
      .catch((e) => setError(errMessage(e)))
  }, [selectedId])

  return (
    <div className="app">
      <Header title="mulix" health={health} />
      <div className="app-body">
        <Sidebar
          sessions={sessions}
          activeId={selectedId}
          onSelect={(id) => {
            setError(null)
            setSelectedId(id)
          }}
          workspaces={workspaces}
          workspace={workspace}
          onWorkspaceChange={setWorkspace}
          onCreate={handleCreate}
          scanWarnings={scanWarnings}
        />
        <main className="app-main">
          <StatusBar sessionId={selectedId} warnings={streamWarnings} readonly={readonly} onTakeover={handleTakeover} />
          {error && (
            <div className="app-error" role="alert">
              {error}
            </div>
          )}
          <TranscriptPane frames={frames} activeApprovalId={approvalPending?.tool_use_id} />
          {/* 拦截条仅交互端持有者（readonly 观流者的 approval POST 会 403——
              留痕行仍在转录呈现，不夺焦点）。key=tool_use_id：新请求重置倒计时
              与焦点。 */}
          {approvalPending && selectedId && !readonly && (
            <ApprovalBar key={approvalPending.tool_use_id} request={approvalPending} onDecide={handleDecide} />
          )}
          {selectedId && (
            <Composer
              onSubmit={handleSubmit}
              onError={(e) => setError(errMessage(e))}
              onCancel={handleCancel}
              running={running}
              readonly={readonly}
              // 冻结语义（TUI 独占分流）：审批在场 or 未选工作区（FR-011）→ 发送禁用。
              disabled={!workspace || approvalPending !== null}
            />
          )}
        </main>
      </div>
    </div>
  )
}
