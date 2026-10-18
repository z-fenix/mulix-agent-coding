// Sidebar.tsx——侧栏（task-12 交互面改造）：工作区选择（WorkspacePicker）+
// 新建会话 + 会话列表（SessionListView）。新建/选择经回调上抛（App 装配
// createSession / openSession takeover=false）；列表来自 GET /api/sessions。
// 未选工作区时新建禁用（提交门禁在 App 的 composer disabled，FR-011）。
import SessionListView from './SessionListView'
import WorkspacePicker from './WorkspacePicker'
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
        <WorkspacePicker workspaces={workspaces} value={workspace} onChange={onWorkspaceChange} />
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
      <SessionListView sessions={sessions} activeId={activeId} onSelect={onSelect} />
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
