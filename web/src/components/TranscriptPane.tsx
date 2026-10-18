// TranscriptPane.tsx——转录面真实渲染（task-11；task-12 小改）：WireFrame[] 经
// reduceTranscript 归约为语义条目（reducer.ts），按条目种类分发组件——markdown
// 正文（DOMPurify 消毒）、工具卡、思考折叠块、委派卡、系统行。task-12 增
// activeApprovalId：交互式 ApprovalBar 在场的请求在转录留痕行上标注
// （transcript-entry--approval-active），留痕与拦截条视觉同源。空态「选择或
// 新建会话」。
import { useMemo } from 'react'
import {
  receiptLine,
  reduceTranscript,
  shortID,
  type ApprovalEntry,
  type NoticeEntry,
  type TranscriptEntry,
} from '../transcript/reducer'
import MarkdownText from '../transcript/MarkdownText'
import ToolCard from '../transcript/ToolCard'
import ThinkingBlock from '../transcript/ThinkingBlock'
import SystemLine from '../transcript/SystemLine'
import DelegationCard from '../transcript/DelegationCard'
import '../transcript/transcript.css'
import type { WireFrame } from '../types'

export interface TranscriptPaneProps {
  frames: WireFrame[]
  emptyHint?: string
  // 在场待审请求的 tool_use_id（ApprovalBar 消费中；无 = 无待审）。
  activeApprovalId?: string
}

export default function TranscriptPane({ frames, emptyHint = '选择或新建会话', activeApprovalId }: TranscriptPaneProps) {
  const entries = useMemo(() => reduceTranscript(frames), [frames])
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
        {entries.map((entry) => {
          const active =
            entry.kind === 'approval' && !entry.resolved && entry.toolUseId === activeApprovalId
          return (
            <li
              key={`${entry.kind}:${entry.seq}`}
              className={`transcript-entry${active ? ' transcript-entry--approval-active' : ''}`}
              data-kind={entry.kind}
            >
              {renderEntry(entry)}
            </li>
          )
        })}
      </ol>
    </div>
  )
}

function renderEntry(entry: TranscriptEntry) {
  switch (entry.kind) {
    case 'text':
      return <MarkdownText text={entry.text} />
    case 'thinking':
      return <ThinkingBlock entry={entry} />
    case 'tool':
      return <ToolCard entry={entry} />
    case 'approval':
      return <ApprovalLine entry={entry} />
    case 'notice':
      return <NoticeLine entry={entry} />
    case 'usage':
      return <SystemLine tone="dim">{receiptLine(entry.ev)}</SystemLine>
    case 'turn_error':
      return <SystemLine tone="error">{`✗ 回合失败 t${entry.turn}（${entry.subtype}）`}</SystemLine>
    case 'engine_exited':
      return <SystemLine tone="error">{`✗ 引擎已退出（${entry.reason}）`}</SystemLine>
    case 'delegation':
      return <DelegationCard entry={entry} />
  }
}

// ApprovalLine 审批留痕行：请求态「[审批] 工具 X（id）需要授权」（warn 槽），
// 裁决态「[裁决] id → decision/source」（TUI 同文案口径；交互卡归 Task 12）。
function ApprovalLine({ entry }: { entry: ApprovalEntry }) {
  if (entry.resolved) {
    return <SystemLine tone="dim">{`[裁决] ${shortID(entry.toolUseId)} → ${entry.decision}/${entry.source}`}</SystemLine>
  }
  return (
    <SystemLine tone="warn">{`[审批] 工具 ${entry.tool}（${shortID(entry.toolUseId)}）需要授权`}</SystemLine>
  )
}

// NoticeLine 通知分级（TUI appendNotice 同口径）：warning 级 ASCII "! " 前缀
// 告警槽（TUI warnLine("! " + content) 同款），其余 · 前缀弱化行。
function NoticeLine({ entry }: { entry: NoticeEntry }) {
  if (entry.level === 'warning') {
    return <SystemLine tone="warn">{`! ${entry.content}`}</SystemLine>
  }
  return <SystemLine tone="dim">{`· ${entry.content}`}</SystemLine>
}
