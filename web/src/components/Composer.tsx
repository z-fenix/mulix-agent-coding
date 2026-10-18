// Composer.tsx——输入框（task-12 交互面改造）：受控 textarea + 发送/中断。
// 回合中（running）发送禁用、中断可用（FR-006/FR-007）；409 → 行内提示
// 「回合未结束」（ApiError.status 判别，输入保留——非 409 错误经 onError 上抛
// App 错误条）；readonly → 输入与发送全禁 + 只读提示；disabled（工作区门禁
// FR-011 / 审批冻结）→ 发送禁用。onSubmit 现返回 Promise：成功才清空输入。
// Enter 发送、Shift+Enter 换行；IME 组合态（候选词未确认）的 Enter 不发送。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'

const TURN_RUNNING_HINT = '回合未结束'
const READONLY_HINT = '只读模式：接管后可提交'

export interface ComposerProps {
  onSubmit: (text: string) => void | Promise<void>
  onCancel?: () => void
  disabled?: boolean
  running?: boolean
  readonly?: boolean
  onError?: (e: unknown) => void
}

export default function Composer({
  onSubmit,
  onCancel,
  disabled = false,
  running = false,
  readonly = false,
  onError,
}: ComposerProps) {
  const [text, setText] = useState('')
  const [notice, setNotice] = useState<string | null>(readonly ? READONLY_HINT : null)

  // 只读态切换（接管成功/失去交互权）同步提示，接管成功清只读提示。
  useEffect(() => {
    if (readonly) setNotice(READONLY_HINT)
    else setNotice((n) => (n === READONLY_HINT ? null : n))
  }, [readonly])

  const submit = async () => {
    const t = text.trim()
    if (disabled || readonly || running || t === '') return
    setNotice(null)
    try {
      await onSubmit(t)
      setText('')
    } catch (e) {
      // 409 = 回合在飞（FR-006 契约提示语）：行内提示 + 输入保留；其余错误
      // 上抛 App 错误条（如 403 只读、400 空 text）。
      if (e instanceof ApiError && e.status === 409) {
        setNotice(TURN_RUNNING_HINT)
      } else {
        onError?.(e)
      }
    }
  }

  const sendDisabled = disabled || readonly || running || text.trim() === ''
  return (
    <div className="composer">
      {notice && (
        <p className="composer-notice" role="status" aria-live="polite">
          {notice}
        </p>
      )}
      <div className="composer-row">
        <textarea
          className="composer-input"
          value={text}
          placeholder="输入任务…（Enter 发送，Shift+Enter 换行）"
          disabled={readonly}
          onChange={(e) => {
            setText(e.target.value)
            setNotice((n) => (n === TURN_RUNNING_HINT ? null : n))
          }}
          onKeyDown={(e) => {
            // IME 组合态（候选词未确认）的 Enter 不发送——中文输入法确认候选词
            // 同样触发 keydown Enter，缺守卫会把半成品文本提前发送并清空输入框。
            if (e.nativeEvent.isComposing) return
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              void submit()
            }
          }}
        />
        <div className="composer-actions">
          <button type="button" className="composer-cancel" onClick={() => onCancel?.()} disabled={!running || !onCancel}>
            中断
          </button>
          <button
            type="button"
            className="composer-send"
            onClick={() => void submit()}
            disabled={sendDisabled}
            title={running ? '回合进行中——等待收口或中断' : undefined}
          >
            发送
          </button>
        </div>
      </div>
    </div>
  )
}
