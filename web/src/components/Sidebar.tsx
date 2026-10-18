// Sidebar.tsx——侧栏（task-10 布局壳）：工作区选择 + 新建会话 + 会话列表。
// 新建/选择经回调上抛（App 装配）；列表来自 GET /api/sessions（open=true =
// 本进程在册，open=false = 磁盘镜像投影）。本任务为最小态，审批/续链等
// 交互随后续任务接管。
import type { SessionEntry } from '../types'

export interface SidebarProps {
  sessions: SessionEntry[]
  activeId: string | null
  onSelect: (id: string) => void
  workspaces: string[]
  workspace: string
  onWorkspaceChange: (ws: string) => void
  onCreate: () => void
  // scanWarnings 是列表接口透出的镜像损坏 meta 警告（如实呈现，不静默）。
  scanWarnings?: string[]
}

export default function Sidebar({
  sessions,
  activeId,
  onSelect,
  workspaces,
  workspace,
  onWorkspaceChange,
  onCreate,
  scanWarnings = [],
}: SidebarProps) {
  return (
    <aside className="sidebar">
      <div className="sidebar-section">
        <label className="sidebar-label" htmlFor="workspace-picker">
          工作区
        </label>
        <select
          id="workspace-picker"
          className="workspace-picker"
          value={workspace}
          onChange={(e) => onWorkspaceChange(e.target.value)}
        >
          <option value="" disabled>
            选择工作区…
          </option>
          {workspaces.map((ws) => (
            <option key={ws} value={ws}>
              {ws}
            </option>
          ))}
        </select>
        <button
          type="button"
          className="new-session"
          onClick={onCreate}
          disabled={workspace === ''}
          title={workspace === '' ? '先选择工作区' : '在所选工作区新建会话'}
        >
          新建会话
        </button>
      </div>
      <ul className="session-list" aria-label="会话列表">
        {sessions.map((s) => (
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
              <span className="session-ws" title={s.workspace}>
                {s.workspace}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {sessions.length === 0 && <p className="sidebar-empty">暂无会话</p>}
      {scanWarnings.length > 0 && (
        <div className="sidebar-warnings" role="status">
          {scanWarnings.map((w, i) => (
            <p key={i}>{w}</p>
          ))}
        </div>
      )}
    </aside>
  )
}
