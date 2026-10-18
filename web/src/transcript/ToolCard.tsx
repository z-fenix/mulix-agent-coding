// ToolCard.tsx——工具卡（task-11）：折叠 = 工具名 + 主参摘要 + 状态（FR-005
// 状态语言：运行 ● accent / 完成 ✓ green / 失败 ✗ red）；展开 = 完整 input JSON
// + 失败详情 + 结果输出（结果形似 unified diff 时走 DiffBlock 视图）。
// 主参键提取对齐 TUI toolArgKeys（toolcard.go——claude CLI 工具集）。
import { useState } from 'react'
import DiffBlock, { looksLikeUnifiedDiff } from './DiffBlock'
import type { ToolEntry, ToolState } from './reducer'

const toolArgKeys: Record<string, string[]> = {
  Read: ['file_path', 'path'],
  Write: ['file_path', 'path'],
  Edit: ['file_path', 'path'],
  NotebookEdit: ['notebook_path', 'path'],
  Bash: ['command', 'cmd'],
  Glob: ['pattern'],
  Grep: ['pattern'],
  WebFetch: ['url'],
  WebSearch: ['query'],
  Task: ['description', 'prompt'],
}

// toolPrimaryArg 主参摘要：按候选键提取标量；未知工具/缺键退化为压缩单行 JSON
// 摘要（截断 64——TUI toolArgSummaryMax 同口径）。
export function toolPrimaryArg(tool: string, input: unknown): string {
  if (input !== null && typeof input === 'object') {
    const m = input as Record<string, unknown>
    for (const key of toolArgKeys[tool] ?? []) {
      const v = m[key]
      if (typeof v === 'string' && v.trim() !== '') return clamp(v.trim(), 64)
      if (typeof v === 'number') return clamp(String(v), 64)
      if (Array.isArray(v)) {
        const parts = v.filter((e): e is string => typeof e === 'string')
        if (parts.length > 0) return clamp(parts.join(', '), 64)
      }
    }
  }
  try {
    const s = JSON.stringify(input)
    return s === undefined ? '' : clamp(s, 64)
  } catch {
    return ''
  }
}

function clamp(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + '…' : s
}

const stateMeta: Record<ToolState, { glyph: string; label: string }> = {
  running: { glyph: '●', label: '运行中' },
  done: { glyph: '✓', label: '完成' },
  failed: { glyph: '✗', label: '失败' },
}

export default function ToolCard({ entry }: { entry: ToolEntry }) {
  const [open, setOpen] = useState(false)
  const meta = stateMeta[entry.state]
  const arg = toolPrimaryArg(entry.tool, entry.input)
  const diffLike = looksLikeUnifiedDiff(entry.output)
  return (
    <div className={`tool-card tool-card--${entry.state}`} data-tool={entry.tool}>
      <button
        type="button"
        className="tool-card-head"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="tool-glyph" aria-hidden>
          {meta.glyph}
        </span>
        <span className="tool-name">{entry.tool}</span>
        {arg !== '' && <span className="tool-arg">({arg})</span>}
        <span className="tool-state">{meta.label}</span>
      </button>
      {/* 失败详情常显（TUI toolCard.renderInner 同语义：失败卡头行下直接出详情行，
          不受展开态门控）；展开面不重复渲染。 */}
      {entry.state === 'failed' && entry.failedDetail !== '' && (
        <p className="tool-fail-detail">{entry.failedDetail}</p>
      )}
      {open && (
        <div className="tool-card-detail">
          <pre className="tool-input">{JSON.stringify(entry.input, null, 2) ?? 'null'}</pre>
          {entry.output !== '' &&
            (diffLike ? (
              <DiffBlock diff={entry.output} />
            ) : (
              <pre className="tool-output">{entry.output}</pre>
            ))}
        </div>
      )}
    </div>
  )
}
