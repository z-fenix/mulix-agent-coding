// WorkspacePicker.tsx——工作区选择器（task-12 自 Sidebar 抽出）：清单来自
// GET /api/workspaces；value 空表示未选择——App 侧以 disabled 门禁提交入口
// 与新建会话（FR-011：未选择工作区时提交入口不可用）。
export interface WorkspacePickerProps {
  workspaces: string[]
  value: string
  onChange: (ws: string) => void
  disabled?: boolean
}

export default function WorkspacePicker({ workspaces, value, onChange, disabled = false }: WorkspacePickerProps) {
  return (
    <select
      id="workspace-picker"
      className="workspace-picker"
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
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
  )
}
