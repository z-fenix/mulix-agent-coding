// App.tsx——布局壳装配（task-10）：Header/Sidebar/TranscriptPane/Composer +
// 会话事件流。选中会话 → open（争取交互端持有者，失败如实呈现错误——只读
// 观流仍可用）→ openSessionStream 对账+实时（stream.ts 去重），实时帧入
// 转录状态；会话切换即重置转录并断开旧流。
import { useCallback, useEffect, useRef, useState } from 'react'
import { cancelTurn, createSession, errMessage, getWorkspaces, listSessions, openSession, submitText } from './api'
import Header, { type Health } from './components/Header'
import Sidebar from './components/Sidebar'
import Composer from './components/Composer'
import TranscriptPane from './components/TranscriptPane'
import { openSessionStream } from './stream'
import type { SessionEntry, WireFrame } from './types'

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
  const streamCloseRef = useRef<(() => void) | null>(null)

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

  // 选中会话：重置转录 → open（尽力而为）→ SSE 对账流。
  useEffect(() => {
    const id = selectedId
    if (!id) return
    setFrames([])
    setStreamWarnings([])
    let disposed = false
    ;(async () => {
      try {
        await openSession(id)
      } catch (e) {
        // 409 未收口/锁冲突等如实呈现；流仍可只读观看（GET /stream 不设门槛）。
        if (!disposed) setError(errMessage(e))
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

  const handleSubmit = useCallback(
    (text: string) => {
      if (!selectedId) return
      submitText(selectedId, text).catch((e) => setError(errMessage(e)))
    },
    [selectedId],
  )

  const handleCancel = useCallback(() => {
    if (!selectedId) return
    cancelTurn(selectedId).catch((e) => setError(errMessage(e)))
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
          {error && (
            <div className="app-error" role="alert">
              {error}
            </div>
          )}
          {streamWarnings.length > 0 && (
            <div className="app-warnings" role="status">
              {streamWarnings.map((w, i) => (
                <p key={i}>{w}</p>
              ))}
            </div>
          )}
          <TranscriptPane frames={frames} />
          {selectedId && <Composer onSubmit={handleSubmit} onCancel={handleCancel} />}
        </main>
      </div>
    </div>
  )
}
