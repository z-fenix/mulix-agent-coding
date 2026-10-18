// ApprovalBar.tsx——审批独占拦截条（task-12，TUI internal/tui/approval.go 对齐）：
// 焦点拦截（role=alertdialog + aria-modal + 挂载即聚焦，非 toast）、工具名 +
// 参数摘要（复用 transcript/ToolCard 的 toolPrimaryArg 摘要口径）、倒计时
// （event.timeout 秒；0 = 未配置 → 服务端治理默认 120s）、三动作
// allow/allow_session/deny。键盘 y/a/n：IME 组合态守卫（Composer 同模式）、
// 修饰组合（含 Shift）不误触（TUI msg.Mod!=0 同纪律）、可编辑元素聚焦时不拦截。
// 与 TUI 的键位偏差：Enter 不映射允许——web 有真实按钮，Enter 保留给按钮激活，
// 避免「焦点在拒绝按钮上按 Enter 双结算」。
// 裁决提交（评审修复轮 1）：onDecide 返回 Promise——在途 busy 禁用三键防重复
// 提交（按钮与键位同门）；失败保留拦截条可重试（行内错误）；成功由父级清卡
// 卸载本组件（焦点恢复到 composer 输入面）。
import { useEffect, useRef, useState } from 'react'
import { errMessage } from '../api'
import { toolPrimaryArg } from '../transcript/ToolCard'
import { shortID } from '../transcript/reducer'
import type { ApprovalDecision, ApprovalRequestEv } from '../types'

// 0 = 事件未携带超时（guard 未配置）→ 服务端治理窗口默认 120s（TUI 上游同口径）。
const DEFAULT_APPROVAL_TIMEOUT = 120

export interface ApprovalBarProps {
  request: ApprovalRequestEv
  onDecide: (decision: ApprovalDecision) => void | Promise<void>
}

export default function ApprovalBar({ request, onDecide }: ApprovalBarProps) {
  const total = request.timeout > 0 ? request.timeout : DEFAULT_APPROVAL_TIMEOUT
  const [left, setLeft] = useState(total)
  const [busy, setBusy] = useState(false)
  const [decideError, setDecideError] = useState<string | null>(null)
  const barRef = useRef<HTMLDivElement>(null)
  const prevFocusRef = useRef<HTMLElement | null>(null)

  // decide 裁决动作：在途禁用防重复提交；失败行内呈现并恢复可重试；成功不在此
  // 处理状态（父级 clearApproval 卸载本组件——resolve 路径零 setState）。
  const decide = (decision: ApprovalDecision) => {
    if (busy) return
    setBusy(true)
    setDecideError(null)
    void Promise.resolve(onDecide(decision)).catch((e) => {
      setBusy(false)
      setDecideError(errMessage(e))
    })
  }
  // decideRef 保证 window keydown 监听只绑一次而回调取最新（busy/闭包随渲染更新）。
  const decideRef = useRef(decide)
  decideRef.current = decide

  useEffect(() => {
    const timer = setInterval(() => setLeft((v) => (v > 0 ? v - 1 : 0)), 1000)
    return () => clearInterval(timer)
  }, [])

  // 独占焦点：挂载即夺取焦点（App 以 key=tool_use_id 挂载——新请求重置倒计时并
  // 重新聚焦）；卸载恢复到 composer 输入面（会话已切走无 composer 时回退原焦点元素）。
  useEffect(() => {
    prevFocusRef.current = document.activeElement as HTMLElement | null
    barRef.current?.focus()
    return () => {
      const composer = document.querySelector<HTMLElement>('.composer-input')
      if (composer) {
        composer.focus()
      } else {
        prevFocusRef.current?.focus()
      }
    }
  }, [])

  // 焦点陷阱（aria-modal 的 Tab 循环面）：拦截条内三按钮循环，Tab 不外泄。
  const onTrapKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== 'Tab') return
    const buttons = barRef.current?.querySelectorAll<HTMLElement>('.approval-actions button')
    if (!buttons || buttons.length === 0) return
    const first = buttons[0]
    const last = buttons[buttons.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.isComposing) return // IME 组合态（候选词未确认）不结算——Composer 同守卫
      // 修饰组合非授权键位——忽略（TUI msg.Mod!=0 同纪律，含 Shift）。
      if (e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) return
      const target = e.target
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement ||
        (target instanceof HTMLElement && target.isContentEditable)
      ) {
        return // 可编辑元素聚焦时不拦截（composer 冻结后正常不发生；侧栏 select 兜底）
      }
      let decision: ApprovalDecision | null = null
      if (e.key === 'y') decision = 'allow'
      else if (e.key === 'a') decision = 'allow_session'
      else if (e.key === 'n') decision = 'deny'
      if (decision !== null) {
        e.preventDefault()
        decideRef.current(decision)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const arg = toolPrimaryArg(request.tool, request.input)

  return (
    <div
      ref={barRef}
      className="approval-bar"
      role="alertdialog"
      aria-modal="true"
      aria-label="审批请求"
      tabIndex={-1}
      onKeyDown={onTrapKeyDown}
    >
      <div className="approval-head">
        <span className="approval-title">
          ⚠ 工具 {request.tool}（{shortID(request.tool_use_id)}）等待授权
        </span>
        <span className="approval-countdown" role="timer" aria-label={`剩余 ${left} 秒`}>
          {left}s
        </span>
      </div>
      {arg !== '' && <p className="approval-arg">{arg}</p>}
      {decideError && (
        <p className="approval-error" role="alert">
          {decideError}
        </p>
      )}
      <div className="approval-actions">
        <button type="button" className="approval-allow" disabled={busy} onClick={() => decide('allow')}>
          允许一次 <kbd>y</kbd>
        </button>
        <button type="button" className="approval-session" disabled={busy} onClick={() => decide('allow_session')}>
          本会话允许 <kbd>a</kbd>
        </button>
        <button type="button" className="approval-deny" disabled={busy} onClick={() => decide('deny')}>
          拒绝 <kbd>n</kbd>
        </button>
      </div>
    </div>
  )
}
