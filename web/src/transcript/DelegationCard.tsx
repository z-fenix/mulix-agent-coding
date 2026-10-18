// DelegationCard.tsx——委派卡（task-11）：子代理生命周期单卡流转（023 FR-016
// TUI 对齐——启动 ● accent / 完成 ✓ green（附 token/成本明细）/ 取消 ⏹ dim /
// 失败 ⊘ red）。mulix 无逐条进度流，卡只有开/收两态（TUI 同口径降级）。
import { shortID } from './reducer'
import type { DelegationEntry, DelegationState } from './reducer'

const stateMeta: Record<DelegationState, { glyph: string; label: string }> = {
  running: { glyph: '●', label: '子代理启动' },
  done: { glyph: '✓', label: '子代理完成' },
  cancelled: { glyph: '⏹', label: '子代理已取消' },
  failed: { glyph: '⊘', label: '子代理失败' },
}

export default function DelegationCard({ entry }: { entry: DelegationEntry }) {
  const meta = stateMeta[entry.state]
  const detail =
    entry.state === 'running'
      ? entry.task
      : entry.state === 'done'
        ? entry.summary
        : entry.reason
  return (
    <div className={`delegation delegation--${entry.state}`}>
      <div className="delegation-head">
        <span className="delegation-glyph" aria-hidden>
          {meta.glyph}
        </span>
        <span className="delegation-label">{meta.label}</span>
        <span className="delegation-id">{shortID(entry.subSessionId)}</span>
        {detail !== '' && <span className="delegation-detail">{detail}</span>}
      </div>
      {entry.state === 'done' && (entry.tokens > 0 || entry.costCents > 0) && (
        <div className="delegation-receipt">
          ⎿ {entry.tokens} tok · ${(entry.costCents / 100).toFixed(2)}
        </div>
      )}
    </div>
  )
}
