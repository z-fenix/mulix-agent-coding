// StatusBar.tsx——状态条（task-12）：Snapshot 500ms 轮询（对齐 TUI statusTick
// 口径），引擎模型/版本/回合/就绪态如实呈现（FR-013：FAILED 启动失败不静默，
// NOT_READY 启动中如实标注）；损坏段警告横幅（stream onWarnings 透传，逐条
// 列出）；只读横幅 + 「接管」按钮（403 / role=readonly → POST /open takeover，
// 交互权交还本端）。轮询失败（会话摘除/网络）同样显形不静默。
import { useEffect, useState } from 'react'
import { errMessage, getSessionState } from '../api'
import type { SessionState } from '../types'

const POLL_MS = 500

// ready → 呈现标签与色槽（未知名降级原样呈现 + warn 槽——向前兼容不静默）。
const READY_LABEL: Record<string, { text: string; tone: 'ok' | 'warn' | 'err' }> = {
  READY: { text: '就绪', tone: 'ok' },
  NOT_READY: { text: '启动中', tone: 'warn' },
  FAILED: { text: '启动失败', tone: 'err' },
}

export interface StatusBarProps {
  sessionId: string | null
  warnings: string[]
  readonly: boolean
  onTakeover: () => void
}

export default function StatusBar({ sessionId, warnings, readonly, onTakeover }: StatusBarProps) {
  const [snap, setSnap] = useState<SessionState | null>(null)
  const [pollError, setPollError] = useState<string | null>(null)

  // 会话切换即重置快照；500ms 轮询（TUI statusTick 同频）。alive 守卫竞态：
  // 迟到的旧会话应答不得写入新会话状态。
  useEffect(() => {
    setSnap(null)
    setPollError(null)
    if (!sessionId) return
    let alive = true
    const tick = () => {
      getSessionState(sessionId)
        .then((s) => {
          if (!alive) return
          setSnap(s)
          setPollError(null)
        })
        .catch((e) => {
          if (!alive) return
          setPollError(errMessage(e))
        })
    }
    tick()
    const timer = setInterval(tick, POLL_MS)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [sessionId])

  const ready = snap ? (READY_LABEL[snap.ready] ?? { text: snap.ready, tone: 'warn' as const }) : null

  return (
    <div className="statusbar">
      {readonly && (
        <div className="statusbar-readonly" role="alert">
          <span>只读模式：交互权由其他端持有，提交/中断/审批不可用</span>
          <button type="button" className="statusbar-takeover" onClick={onTakeover}>
            接管
          </button>
        </div>
      )}
      {pollError && (
        <div className="statusbar-error" role="alert">
          状态轮询失败：{pollError}
        </div>
      )}
      {warnings.length > 0 && (
        <div className="statusbar-warnings" role="alert">
          {warnings.map((w, i) => (
            <p key={i}>{w}</p>
          ))}
        </div>
      )}
      {snap && (
        <div className="statusbar-info">
          <span>模型 {snap.model || '—'}</span>
          <span>引擎 {snap.cli_version || '—'}</span>
          <span>回合 {snap.turn}</span>
          <span className={`statusbar-ready statusbar-ready--${ready?.tone}`}>{ready?.text}</span>
        </div>
      )}
    </div>
  )
}
