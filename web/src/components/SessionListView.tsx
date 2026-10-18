// SessionListView.tsx——会话列表（task-12 自 Sidebar 抽出）：条目 = 短 id +
// 状态 + 最近活动时间 + 工作区；点击 → onSelect（App 装配 openSession
// takeover=false——不夺他人交互权，只读打开由 403/role 呈现）。last_activity
// 来自列表投影（RFC3339），只取本地时间部分，解析失败不显示（不虚构）。
import type { SessionEntry } from '../types'

export interface SessionListViewProps {
  sessions: SessionEntry[]
  activeId: string | null
  onSelect: (id: string) => void
}

function timeOf(iso: string | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString()
}

export default function SessionListView({ sessions, activeId, onSelect }: SessionListViewProps) {
  return (
    <ul className="session-list" aria-label="会话列表">
      {sessions.map((s) => {
        const t = timeOf(s.last_activity)
        return (
          <li key={s.id}>
            <button
              type="button"
              className={`session-item${s.id === activeId ? ' session-item--active' : ''}`}
              aria-current={s.id === activeId || undefined}
              onClick={() => onSelect(s.id)}
            >
              <span className="session-id" title={s.id}>
                {s.id.slice(0, 8)}
              </span>
              <span className={`session-status session-status--${s.status}`}>{s.status}</span>
              {t && (
                <span className="session-time" title={s.last_activity}>
                  {t}
                </span>
              )}
              <span className="session-ws" title={s.workspace}>
                {s.workspace}
              </span>
            </button>
          </li>
        )
      })}
    </ul>
  )
}
