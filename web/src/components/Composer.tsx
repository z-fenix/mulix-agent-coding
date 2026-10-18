// Composer.tsx——输入框（task-10 布局壳）：受控 textarea + 发送/中断。
// 回调上抛（App 装配到 submit/cancel API）；Enter 发送、Shift+Enter 换行；
// 与回合状态的联动禁用（running 时发送禁用等）由 Task 11 接管。
import { useState } from 'react'

export interface ComposerProps {
  onSubmit: (text: string) => void
  onCancel?: () => void
  disabled?: boolean
}

export default function Composer({ onSubmit, onCancel, disabled = false }: ComposerProps) {
  const [text, setText] = useState('')
  const submit = () => {
    const t = text.trim()
    if (disabled || t === '') return
    onSubmit(t)
    setText('')
  }
  return (
    <div className="composer">
      <textarea
        className="composer-input"
        value={text}
        placeholder="输入任务…（Enter 发送，Shift+Enter 换行）"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault()
            submit()
          }
        }}
      />
      <div className="composer-actions">
        <button
          type="button"
          className="composer-cancel"
          onClick={() => onCancel?.()}
          disabled={disabled || !onCancel}
        >
          中断
        </button>
        <button
          type="button"
          className="composer-send"
          onClick={submit}
          disabled={disabled || text.trim() === ''}
        >
          发送
        </button>
      </div>
    </div>
  )
}
