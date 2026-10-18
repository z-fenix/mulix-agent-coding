// SystemLine.tsx——系统行（task-11）：非内容转写的弱化单行。tone 三档对齐
// TUI 槽位——dim 通知（·）/回执，warn 告警通知（！），error 失败留痕（✗）。
import type { ReactNode } from 'react'

export interface SystemLineProps {
  tone: 'dim' | 'warn' | 'error'
  children: ReactNode
}

export default function SystemLine({ tone, children }: SystemLineProps) {
  return <div className={`system-line system-line--${tone}`}>{children}</div>
}
