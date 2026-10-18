// ThinkingBlock.tsx——思考折叠块（task-11）：默认折叠（TUI D14 折叠语义——
// 思考流不喧宾夺主），点击展开全文；头部带聚合 token 数（引擎 estimated_tokens
// 口径，reducer 取末次事件值）。
import { useState } from 'react'
import type { ThinkingEntry } from './reducer'

export default function ThinkingBlock({ entry }: { entry: ThinkingEntry }) {
  const [open, setOpen] = useState(false)
  return (
    <div className={open ? 'thinking-block thinking-block--open' : 'thinking-block'}>
      <button
        type="button"
        className="thinking-head"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span aria-hidden>{open ? '▾' : '▸'}</span>
        <span>思考</span>
        {entry.tokens > 0 && <span className="thinking-tokens">{entry.tokens} tok</span>}
      </button>
      {open && <pre className="thinking-text">{entry.text}</pre>}
    </div>
  )
}
