// app.test.tsx——App 空壳冒烟（Task 9 Step 1）：渲染后断言「mulix」标题存在。
// 后续任务按 design §10.9 扩展：markdown/diff/工具卡渲染与审批交互、SSE 去重。
import { act, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import App from './App'

describe('App', () => {
  it('renders the mulix title', async () => {
    render(<App />)
    // 挂载期 healthz 探测的微任务在 act 内落地（jsdom 无同源服务，fetch 必然
    // 拒绝 → 状态点转 down），包裹以避免 React act 警告污染后续测试输出。
    await act(async () => {})
    expect(screen.getByText('mulix')).toBeTruthy()
  })
})
