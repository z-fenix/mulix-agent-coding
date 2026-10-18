// TranscriptPane.tsx——转录面（task-10 最小态）：键控帧列表，本任务只呈现
// kind 标签（Task 11 换成 markdown/工具卡/审批交互的真实渲染）。空态
// 「选择或新建会话」。
import type { WireFrame } from '../types'

export interface TranscriptPaneProps {
  frames: WireFrame[]
  emptyHint?: string
}

function frameKey(f: WireFrame, index: number): string {
  const seq = (f.ev as { seq?: unknown }).seq
  return typeof seq === 'number' ? `${f.kind}:${seq}` : `idx:${index}`
}

export default function TranscriptPane({ frames, emptyHint = '选择或新建会话' }: TranscriptPaneProps) {
  if (frames.length === 0) {
    return (
      <div className="transcript transcript--empty">
        <p>{emptyHint}</p>
      </div>
    )
  }
  return (
    <div className="transcript">
      <ol className="transcript-list">
        {frames.map((f, i) => (
          <li key={frameKey(f, i)} className="transcript-entry" data-kind={f.kind}>
            {f.kind}
          </li>
        ))}
      </ol>
    </div>
  )
}
