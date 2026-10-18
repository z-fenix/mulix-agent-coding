// Composer.test.tsx——输入框键盘行为（评审修复轮 1；task-12 改造同步更新）：
// 1) Enter 发送受控文本并清空——task-12 起 submit 为 async（409 → 行内提示需
//    await 结果；成功才清空），断言前以 act 落地微任务；2) IME 组合态（候选词
// 未确认）的 Enter 不发送——中文输入法确认候选词同样触发 keydown Enter，缺守卫
// 会把半成品文本提前发送并清空输入框；组合结束后的 Enter 正常发送。
import { act, fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Composer from './Composer'

function typeText(text: string) {
  fireEvent.change(screen.getByRole('textbox'), { target: { value: text } })
}

describe('Composer', () => {
  it('Enter 发送受控文本并清空输入框', async () => {
    const onSubmit = vi.fn()
    render(<Composer onSubmit={onSubmit} />)
    typeText('你好 mulix')
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    await act(async () => {}) // 异步提交落地（成功才清空——task-12 契约）
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit).toHaveBeenCalledWith('你好 mulix')
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('')
  })

  it('IME 组合态 Enter 不发送，组合结束后 Enter 正常发送', async () => {
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
    await act(async () => {})
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit).toHaveBeenCalledWith('zhong wen')
  })
})
