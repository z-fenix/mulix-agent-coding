// interaction.test.tsx——交互面集成测试（task-12）：App 装配层的六条契约
// （task brief Step 1）+ 回合状态/新建会话/警告横幅三条补充。api/stream 模块级
// mock（vi.mock）——本文件测交互回路接线，不测流协议本身（stream.test.ts /
// api 形态由 task-10 与后端 serve 测试钉住）。
//
// 注意：未引入 @testing-library/jest-dom（清单外依赖），断言一律用原生属性
// （.disabled/.textContent）与 RTL 内建查询。
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, createSession, getSessionState, getWorkspaces, listSessions, openSession, sendApproval, submitText, cancelTurn } from './api'
import App from './App'
import type { SessionState, WireFrame } from './types'

// ---- 模块级 mock：stream（捕获 handlers 以注入帧）；api（保留 ApiError 真身）----

// stream.ts 的 SessionStreamHandlers 结构别名（不 import 模块——mock 工厂自足）。
type StreamHandlers = {
  onEvent: (f: WireFrame) => void
  onWarnings: (w: string[]) => void
}
const streamHandlers = new Map<string, StreamHandlers>()

vi.mock('./stream', () => ({
  openSessionStream: vi.fn((id: string, h: StreamHandlers) => {
    streamHandlers.set(id, h)
    return { close: vi.fn() }
  }),
}))

vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>()
  return {
    ...actual,
    getWorkspaces: vi.fn(),
    listSessions: vi.fn(),
    createSession: vi.fn(),
    openSession: vi.fn(),
    getSessionState: vi.fn(),
    submitText: vi.fn(),
    cancelTurn: vi.fn(),
    sendApproval: vi.fn(),
  }
})

// ---- fixture ----

const SID = 'sess1234'
const SESSIONS = [
  { id: SID, workspace: '/repo', status: 'closed', open: true, last_activity: '2026-10-19T01:02:03Z' },
]
const STATE: SessionState = {
  session_id: SID,
  engine_session_id: 'eng-1',
  model: 'deepseek-chat',
  cli_version: '2.1.301',
  turn: 3,
  running: false,
  ready: 'READY',
  dir: '/tmp/sess1234',
  meter: { turns: 3, uncached: 10, write: 20, read: 30, output: 40, session_hit: 0.5, cost: 0.01, saving: 0.02, cold_turns: 1 },
  turn_tokens: { input_tokens: 10, cache_write_tokens: 20, cache_read_tokens: 30, output_tokens: 40, thinking_tokens: 5 },
}

function mockDefaults() {
  vi.mocked(getWorkspaces).mockResolvedValue(['/repo'])
  vi.mocked(listSessions).mockResolvedValue({ sessions: SESSIONS, warnings: [] })
  vi.mocked(createSession).mockResolvedValue({ id: 'new1', workspace: '/repo', status: 'empty' })
  vi.mocked(openSession).mockResolvedValue({ id: SID, role: 'interactive' })
  vi.mocked(getSessionState).mockResolvedValue(STATE)
  vi.mocked(submitText).mockResolvedValue({ turn: 4 })
  vi.mocked(cancelTurn).mockResolvedValue({ ok: true })
  vi.mocked(sendApproval).mockResolvedValue({ ok: true })
}

// ---- helpers ----

// pushFrame 向已打开会话的事件流注入一帧（act 包裹：reducer/hooks 状态同步落地）。
function pushFrame(frame: WireFrame) {
  const h = streamHandlers.get(SID)
  if (!h) throw new Error('stream not opened for ' + SID)
  act(() => {
    h.onEvent(frame)
  })
}

const btn = (name: string | RegExp): HTMLButtonElement => screen.getByRole('button', { name }) as HTMLButtonElement

// openSessionUI 渲染 App 并点开列表首会话：等待 stream 挂载（handlers 在册）
// 后再返回——注入帧/断言审批条的前提。
async function openSessionUI() {
  render(<App />)
  await act(async () => {}) // 挂载期探测/列表微任务落地（act 内，防警告）
  fireEvent.click(screen.getByRole('button', { name: /sess1234/ }))
  await waitFor(() => expect(streamHandlers.has(SID)).toBe(true))
  await act(async () => {}) // open → stream 挂载微任务落地
}

beforeEach(() => {
  streamHandlers.clear()
  mockDefaults()
})

describe('交互面（task-12）', () => {
  it('提交 prompt 并清空输入框', async () => {
    await openSessionUI()
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '帮我跑测试' } })
    await act(async () => {
      fireEvent.click(btn('发送'))
    })
    expect(submitText).toHaveBeenCalledWith(SID, '帮我跑测试')
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('')
  })

  it('409 → 行内「回合未结束」提示，输入保留', async () => {
    vi.mocked(submitText).mockRejectedValueOnce(new ApiError(409, '回合进行中：等待当前回合收口后再提交'))
    await openSessionUI()
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '第二条任务' } })
    await act(async () => {
      fireEvent.click(btn('发送'))
    })
    expect(await screen.findByText('回合未结束')).toBeTruthy()
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('第二条任务')
  })

  it('审批条：倒计时存在；点击 allow / 键位 a、n 三动作；pending 冻结 composer', async () => {
    await openSessionUI()
    // 先输入文本，使「发送禁用」断言归因于审批冻结而非空文本
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '待审中的任务' } })
    pushFrame({ kind: 'approval_request', ev: { seq: 1, turn: 1, tool_use_id: 'tu-1', tool: 'Bash', input: { command: 'go test ./...' }, timeout: 120 } })
    // 倒计时（120s；与 pushFrame 同步断言防计时抖动）+ 工具名/参数摘要
    expect(screen.getByRole('timer').textContent).toContain('120')
    const bar = screen.getByRole('alertdialog')
    expect(bar.textContent).toContain('Bash')
    expect(bar.textContent).toContain('go test ./...')
    // a11y：模态语义 + 焦点陷阱（末按钮 Tab → 首按钮）
    expect(bar.getAttribute('aria-modal')).toBe('true')
    const barButtons = bar.querySelectorAll<HTMLButtonElement>('.approval-actions button')
    expect(barButtons.length).toBe(3)
    barButtons[barButtons.length - 1].focus()
    fireEvent.keyDown(bar, { key: 'Tab' })
    expect(document.activeElement).toBe(barButtons[0])
    // 独占语义：composer 冻结（发送禁用）
    expect(btn('发送').disabled).toBe(true)
    // 点击 允许一次 → api 收 allow → 卡即清（TUI resolve 同语义）
    await act(async () => {
      fireEvent.click(btn(/允许一次/))
    })
    expect(sendApproval).toHaveBeenCalledWith(SID, 'tu-1', 'allow')
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(btn('发送').disabled).toBe(false)
    // 键位 a → allow_session；n → deny；IME 组合态不结算
    pushFrame({ kind: 'approval_request', ev: { seq: 3, turn: 1, tool_use_id: 'tu-2', tool: 'Edit', input: { file_path: '/a.go' }, timeout: 30 } })
    expect(screen.getByRole('timer').textContent).toContain('30')
    fireEvent.keyDown(window, { key: 'a', isComposing: true })
    expect(sendApproval).toHaveBeenCalledTimes(1)
    fireEvent.keyDown(window, { key: 'a' })
    await act(async () => {})
    expect(sendApproval).toHaveBeenLastCalledWith(SID, 'tu-2', 'allow_session')
    pushFrame({ kind: 'approval_request', ev: { seq: 4, turn: 1, tool_use_id: 'tu-3', tool: 'Write', input: { file_path: '/b.go' }, timeout: 30 } })
    fireEvent.keyDown(window, { key: 'n' })
    await act(async () => {})
    expect(sendApproval).toHaveBeenLastCalledWith(SID, 'tu-3', 'deny')
    // 无点击的服务端裁决（如超时）：approval_resolved 到达即清除；焦点恢复到 composer
    pushFrame({ kind: 'approval_request', ev: { seq: 5, turn: 1, tool_use_id: 'tu-4', tool: 'Bash', input: { command: 'ls' }, timeout: 30 } })
    expect(screen.getByRole('alertdialog')).toBeTruthy()
    pushFrame({ kind: 'approval_resolved', ev: { seq: 6, tool_use_id: 'tu-4', decision: 'deny', source: 'timeout', resolved_by: 'timeout' } })
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect((document.activeElement as HTMLElement | null)?.className).toContain('composer-input')
  })

  it('审批 POST 失败：拦截条保留、行内错误、按钮恢复可重试；成功后清卡', async () => {
    await openSessionUI()
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '失败重试流' } })
    pushFrame({ kind: 'approval_request', ev: { seq: 20, turn: 1, tool_use_id: 'tu-9', tool: 'Bash', input: { command: 'make test' }, timeout: 60 } })
    vi.mocked(sendApproval).mockRejectedValueOnce(new ApiError(500, '审批投递失败'))
    await act(async () => {
      fireEvent.click(btn(/允许一次/))
    })
    expect(sendApproval).toHaveBeenCalledWith(SID, 'tu-9', 'allow')
    // 失败不清卡：拦截条保留（服务端仍在等待），行内错误，按钮恢复可重试
    expect(screen.queryByRole('alertdialog')).not.toBeNull()
    expect(screen.getByRole('alertdialog').textContent).toContain('审批投递失败')
    expect(btn(/允许一次/).disabled).toBe(false)
    // 重试成功 → 清卡
    vi.mocked(sendApproval).mockResolvedValueOnce({ ok: true })
    await act(async () => {
      fireEvent.click(btn(/允许一次/))
    })
    expect(sendApproval).toHaveBeenLastCalledWith(SID, 'tu-9', 'allow')
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('只读横幅与接管（403 → 横幅；接管 → open(id, true) → 解除）', async () => {
    vi.mocked(openSession).mockRejectedValueOnce(new ApiError(403, 'readonly: interactive holder required'))
    render(<App />)
    await act(async () => {})
    fireEvent.click(screen.getByRole('button', { name: /sess1234/ }))
    const banner = await screen.findByRole('alert')
    expect(banner.textContent).toContain('只读')
    // 输入框禁用 + 有文本仍禁用发送（归因于只读而非空文本）
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    // 接管 → api.openSession(id, true) → 角色回 interactive → 横幅消失、解除禁用
    vi.mocked(openSession).mockResolvedValueOnce({ id: SID, role: 'interactive' })
    await act(async () => {
      fireEvent.click(btn('接管'))
    })
    expect(openSession).toHaveBeenCalledWith(SID, true)
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '接管后的任务' } })
    expect(btn('发送').disabled).toBe(false)
  })

  it('submit 403 → 只读横幅（评审 C1/M2：交互权丢失可观测）', async () => {
    await openSessionUI()
    // 宽限竞态/被接管后 submit → 服务端 403;App 须降级只读(横幅+输入禁用),
    // 而非静默失败(修复前 App.readonly 恒 false,无接管通路)。
    vi.mocked(submitText).mockRejectedValueOnce(new ApiError(403, 'readonly: interactive holder required'))
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '降级前的输入' } })
    await act(async () => {
      fireEvent.click(btn('发送'))
    })
    const banner = await screen.findByRole('alert')
    expect(banner.textContent).toContain('只读')
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    // 403 不上错误条(与 open 403 同口径——接管按钮即恢复通路),输入保留
    expect(document.querySelector('.app-error')).toBeNull()
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('降级前的输入')
    // 接管 → 角色回交互 → 横幅消失
    vi.mocked(openSession).mockResolvedValueOnce({ id: SID, role: 'interactive' })
    await act(async () => {
      fireEvent.click(btn('接管'))
    })
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false)
  })

  it('approval 403 → 只读横幅、拦截条退场（评审 C1/M2）', async () => {
    await openSessionUI()
    pushFrame({ kind: 'approval_request', ev: { seq: 30, turn: 1, tool_use_id: 'tu-ro', tool: 'Bash', input: { command: 'ls' }, timeout: 30 } })
    expect(screen.getByRole('alertdialog')).toBeTruthy()
    vi.mocked(sendApproval).mockRejectedValueOnce(new ApiError(403, 'readonly: interactive holder required'))
    await act(async () => {
      fireEvent.click(btn(/允许一次/))
    })
    // 交互权丢失:横幅呈现,拦截条随 readonly 门控退场(POST 403 意味着裁决
    // 必然不能落盘,保留拦截条只会造成可裁决的假象)
    const banner = await screen.findByRole('alert')
    expect(banner.textContent).toContain('只读')
    expect(screen.queryByRole('alertdialog')).toBeNull()
  })

  it('工作区门禁：未选工作区 → composer 提交禁用（FR-011）', async () => {
    vi.mocked(getWorkspaces).mockResolvedValue([])
    await openSessionUI()
    // 先输入文本排除「空文本禁用」的平凡通过——禁用必须来自工作区门禁本身
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '有内容也禁用' } })
    expect(btn('发送').disabled).toBe(true)
    expect(btn('新建会话').disabled).toBe(true)
  })

  it('状态条：引擎启动失败（ready=FAILED）如实呈现（FR-013）', async () => {
    vi.mocked(getSessionState).mockResolvedValue({ ...STATE, ready: 'FAILED' })
    await openSessionUI()
    expect(await screen.findByText('启动失败')).toBeTruthy()
    // 就绪/模型等快照字段一并呈现
    expect(screen.getByText(/deepseek-chat/)).toBeTruthy()
  })

  it('回合状态：turn_started → 发送禁用/中断可用；turn_done/engine_exited → 恢复（FR-007）', async () => {
    await openSessionUI()
    // 先输入文本，使禁用断言归因于回合在飞而非空文本
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '跑一轮' } })
    expect(btn('中断').disabled).toBe(true)
    pushFrame({ kind: 'turn_started', ev: { seq: 10, session_id: SID, engine_session_id: 'e', pinned_model: 'm', unattended: false } })
    expect(btn('发送').disabled).toBe(true)
    expect(btn('中断').disabled).toBe(false)
    await act(async () => {
      fireEvent.click(btn('中断'))
    })
    expect(cancelTurn).toHaveBeenCalledWith(SID)
    pushFrame({ kind: 'turn_done', ev: { seq: 11, turn: 4, subtype: 'success', is_error: false, duration_ms: 10, result_text: '' } })
    expect(btn('发送').disabled).toBe(false)
    expect(btn('中断').disabled).toBe(true)
    // engine_exited 同样扫尾在飞态
    pushFrame({ kind: 'turn_started', ev: { seq: 12, session_id: SID, engine_session_id: 'e', pinned_model: 'm', unattended: false } })
    expect(btn('发送').disabled).toBe(true)
    pushFrame({ kind: 'engine_exited', ev: { seq: 13, code: 1, reason: 'crash' } })
    expect(btn('发送').disabled).toBe(false)
    expect(btn('中断').disabled).toBe(true)
  })

  it('新建会话：createSession 后打开新会话事件流', async () => {
    render(<App />)
    await act(async () => {})
    await act(async () => {
      fireEvent.click(btn('新建会话'))
    })
    expect(createSession).toHaveBeenCalledWith('/repo')
    await waitFor(() => expect(streamHandlers.has('new1')).toBe(true))
  })

  it('状态条：损坏段警告横幅列出警告', async () => {
    await openSessionUI()
    act(() => {
      streamHandlers.get(SID)!.onWarnings(['检测到 2 段损坏事件（已隔离转储）'])
    })
    expect(await screen.findByText('检测到 2 段损坏事件（已隔离转储）')).toBeTruthy()
  })
})
