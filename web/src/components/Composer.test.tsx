// Composer.test.tsx——输入框键盘行为（评审修复轮 1）：
// 1) Enter 发送受控文本并清空；2) IME 组合态（候选词未确认）的 Enter 不发送
// ——中文输入法确认候选词同样触发 keydown Enter，缺守卫会把半成品文本提前
// 发送并清空输入框；组合结束后的 Enter 正常发送。
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Composer from './Composer'

function typeText(text: string) {
  fireEvent.change(screen.getByRole('textbox'), { target: { value: text } })
}

describe('Composer', () => {
  it('Enter 发送受控文本并清空输入框', () => {
    const onSubmit = vi.fn()
    render(<Composer onSubmit={onSubmit} />)
    typeText('你好 mulix')
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit).toHaveBeenCalledWith('你好 mulix')
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('')
  })

  it('IME 组合态 Enter 不发送，组合结束后 Enter 正常发送', () => {
    const onSubmit = vi.fn()
    render(<Composer onSubmit={onSubmit} />)
    typeText('zhong wen')
    const box = screen.getByRole('textbox')
    // 组合中（候选词未确认）：keydown Enter 到达但必须被忽略
    fireEvent.keyDown(box, { key: 'Enter', isComposing: true })
    expect(onSubmit).not.toHaveBeenCalled()
    expect((box as HTMLTextAreaElement).value).toBe('zhong wen')
    // 组合结束后：Enter 正常发送
    fireEvent.keyDown(box, { key: 'Enter', isComposing: false })
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit).toHaveBeenCalledWith('zhong wen')
  })
})
