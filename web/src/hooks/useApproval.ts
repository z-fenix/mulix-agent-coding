// useApproval.ts——待审请求跟踪（task-12）：approval_request 进 → pending；
// 同 tool_use_id 的 approval_resolved 到达 → 清除（裁决事件驱动）。clear()
// 是本地乐观清除（用户动作即清卡——TUI approval.go resolve 同语义：回执事件
// 随后到达时幂等跳过），只对当前请求生效，新请求到达即重置。
import { useEffect, useMemo, useState } from 'react'
import type { ApprovalRequestEv, WireFrame } from '../types'

// derivePending 纯归约：按事件序取最后一个未裁决的 approval_request。
function derivePending(frames: WireFrame[]): ApprovalRequestEv | null {
  let pending: ApprovalRequestEv | null = null
  for (const f of frames) {
    if (f.kind === 'approval_request') pending = f.ev
    else if (f.kind === 'approval_resolved' && pending?.tool_use_id === f.ev.tool_use_id) {
      pending = null
    }
  }
  return pending
}

export interface ApprovalState {
  pending: ApprovalRequestEv | null
  clear: () => void
}

export function useApproval(frames: WireFrame[]): ApprovalState {
  const derived = useMemo(() => derivePending(frames), [frames])
  const derivedId = derived?.tool_use_id ?? null
  const [dismissed, setDismissed] = useState<string | null>(null)
  // 新待审请求（或裁决事件使 derived 变化）重置本地清除——dismissal 只对
  // 产生它的那个请求生效。
  useEffect(() => {
    setDismissed(null)
  }, [derivedId])
  const pending = derived !== null && derived.tool_use_id !== dismissed ? derived : null
  return { pending, clear: () => setDismissed(derivedId) }
}
