// render.test.tsx——transcript 渲染组件契约测试（task-11 Step 1）。
// Review Focus 5：markdown/HTML 注入面必须过 DOMPurify——script 节点不得幸存。
// 另覆盖：fenced code 高亮类、unified diff 加/删行类、工具卡折叠摘要与展开详情、
// thinking 默认折叠、TranscriptPane 对 engine_exited 扫尾的真实渲染。
import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import MarkdownText from './MarkdownText'
import DiffBlock from './DiffBlock'
import ToolCard from './ToolCard'
import ThinkingBlock from './ThinkingBlock'
import TranscriptPane from '../components/TranscriptPane'
import { reduceTranscript, type ToolEntry, type ThinkingEntry } from './reducer'
import type { EventKind, EvMap, WireFrame } from '../types'

function fr<K extends EventKind>(kind: K, ev: EvMap[K]): WireFrame {
  return { kind, ev } as WireFrame
}

describe('MarkdownText', () => {
  it('sanitizes script injection in markdown (Review Focus 5)', () => {
    const { container } = render(
      <MarkdownText text={'hello <script>alert(1)</script> <img src=x onerror="alert(1)"> world'} />,
    )
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('img[onerror]')).toBeNull()
    expect(container.textContent).toContain('hello')
    expect(container.textContent).toContain('world')
  })

  it('renders fenced code with highlight classes', () => {
    const { container } = render(<MarkdownText text={'```js\nconst a = 1\n```'} />)
    const code = container.querySelector('pre > code')
    expect(code).toBeTruthy()
    expect(code?.className).toContain('hljs')
    expect(code?.className).toContain('language-js')
    // 高亮真实生效：js 语法里 const 是 keyword 类。
    expect(container.querySelector('.hljs-keyword')).toBeTruthy()
  })

  it('keeps plain paragraphs and inline formatting', () => {
    const { container } = render(<MarkdownText text={'**bold** and `code`'} />)
    expect(container.querySelector('strong')?.textContent).toBe('bold')
    expect(container.querySelector('code')?.textContent).toBe('code')
  })
})

describe('DiffBlock', () => {
  const diff = [
    '--- a/foo.txt',
    '+++ b/foo.txt',
    '@@ -1,3 +1,3 @@',
    ' context',
    '-deleted line',
    '+added line',
    ' more context',
  ].join('\n')

  it('renders unified diff rows with add/del classes and line numbers', () => {
    const { container } = render(<DiffBlock diff={diff} />)
    const addRows = container.querySelectorAll('.diff-row--add')
    const delRows = container.querySelectorAll('.diff-row--del')
    expect(addRows).toHaveLength(1)
    expect(delRows).toHaveLength(1)
    expect(addRows[0].textContent).toContain('added line')
    expect(delRows[0].textContent).toContain('deleted line')
    // 行号：hunk 自 1 起——context(1,1) → del 占旧 2 → add 占新 2 → context(3,3)。
    expect(addRows[0].querySelector('.diff-no--new')?.textContent).toBe('2')
    expect(delRows[0].querySelector('.diff-no--old')?.textContent).toBe('2')
    const ctx = container.querySelectorAll('.diff-row--context')
    expect(ctx).toHaveLength(2)
  })

  it('renders hunk and file headers as meta rows', () => {
    const { container } = render(<DiffBlock diff={diff} />)
    const metas = Array.from(container.querySelectorAll('.diff-row--meta'))
    expect(metas.some((m) => m.textContent?.includes('@@ -1,3 +1,3 @@'))).toBe(true)
    expect(metas.some((m) => m.textContent?.includes('--- a/foo.txt'))).toBe(true)
  })

  it('treats ---/+++ content lines inside a hunk as del/add, not file headers', () => {
    // 回归（评审 Important）：hunk 内删除行内容「-- item」在 diff 原文里呈
    // 「--- item」，与文件头同前缀——不得误判吞掉余下 hunk。
    const tricky = [
      '@@ -1,4 +1,4 @@',
      ' before',
      '--- item',
      '+++ item',
      ' after',
      '@@ -10,2 +10,2 @@',
      '-gone',
      '+here',
    ].join('\n')
    const { container } = render(<DiffBlock diff={tricky} />)
    const dels = container.querySelectorAll('.diff-row--del')
    const adds = container.querySelectorAll('.diff-row--add')
    // hunk1：context(1,1) → ---item 删行 old 2 → +++item 加行 new 2 → context(3,3)。
    expect(dels).toHaveLength(2)
    expect(dels[0].querySelector('.diff-text')?.textContent).toBe('-- item')
    expect(dels[0].querySelector('.diff-no--old')?.textContent).toBe('2')
    expect(adds).toHaveLength(2)
    expect(adds[0].querySelector('.diff-text')?.textContent).toBe('++ item')
    expect(adds[0].querySelector('.diff-no--new')?.textContent).toBe('2')
    // 后续 hunk 不受污染：第二 hunk 行号接 @@ 起点，类正常。
    expect(dels[1].querySelector('.diff-no--old')?.textContent).toBe('10')
    expect(dels[1].querySelector('.diff-text')?.textContent).toBe('gone')
    expect(adds[1].querySelector('.diff-no--new')?.textContent).toBe('10')
    expect(adds[1].querySelector('.diff-text')?.textContent).toBe('here')
  })
})

describe('ToolCard', () => {
  function pairedCard(): ToolEntry {
    const entries = reduceTranscript([
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 'tu1', tool: 'Bash', input: { command: 'ls -la' } }),
      fr('tool_result', { seq: 2, turn: 1, tool_use_id: 'tu1', content: 'hello world\nsecond line', is_error: false }),
    ])
    return entries[0] as ToolEntry
  }

  it('renders collapsed summary (tool + arg + state) without detail', () => {
    const { container, getByRole } = render(<ToolCard entry={pairedCard()} />)
    const head = getByRole('button')
    expect(head.textContent).toContain('Bash')
    expect(head.textContent).toContain('ls -la')
    expect(head.textContent).toContain('完成')
    expect(container.querySelector('.tool-card-detail')).toBeNull()
    expect(getByRole('button').getAttribute('aria-expanded')).toBe('false')
  })

  it('expands to full input JSON and result output on click', () => {
    const { container, getByRole } = render(<ToolCard entry={pairedCard()} />)
    fireEvent.click(getByRole('button'))
    expect(getByRole('button').getAttribute('aria-expanded')).toBe('true')
    const detail = container.querySelector('.tool-card-detail')
    expect(detail).toBeTruthy()
    expect(container.querySelector('.tool-input')?.textContent).toContain('"command"')
    expect(container.querySelector('.tool-output')?.textContent).toContain('hello world')
  })

  it('renders failed state detail (always visible, TUI parity) with expandable output', () => {
    const entries = reduceTranscript([
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 'tu2', tool: 'Bash', input: {} }),
      fr('tool_result', { seq: 2, turn: 1, tool_use_id: 'tu2', content: 'boom', is_error: true }),
    ])
    const { container, getByRole } = render(<ToolCard entry={entries[0] as ToolEntry} />)
    expect(container.querySelector('.tool-card--failed')).toBeTruthy()
    expect(getByRole('button').textContent).toContain('失败')
    // 失败详情常显（TUI 失败卡头行下直接出详情行——不受展开态门控）。
    expect(container.querySelector('.tool-fail-detail')?.textContent).toContain('boom')
    fireEvent.click(getByRole('button'))
    expect(container.querySelector('.tool-output')?.textContent).toContain('boom')
  })
})

describe('ThinkingBlock', () => {
  it('is collapsed by default and expands on click', () => {
    const entry: ThinkingEntry = { kind: 'thinking', seq: 1, turn: 1, text: 'deep thought', tokens: 42 }
    const { container, getByRole } = render(<ThinkingBlock entry={entry} />)
    expect(container.querySelector('.thinking-text')).toBeNull()
    expect(getByRole('button').getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(getByRole('button'))
    expect(container.querySelector('.thinking-text')?.textContent).toBe('deep thought')
  })
})

describe('TranscriptPane — 真实渲染集成', () => {
  it('renders engine_exited sweep: running card becomes failed + exit line', () => {
    const frames = [
      fr('tool_use_requested', { seq: 1, turn: 1, tool_use_id: 'tuE', tool: 'Read', input: { file_path: 'a.go' } }),
      fr('engine_exited', { seq: 2, code: 1, reason: 'killed' }),
    ]
    const { container } = render(<TranscriptPane frames={frames} />)
    expect(container.querySelectorAll('.tool-card--failed')).toHaveLength(1)
    expect(container.textContent).toContain('引擎已退出，工具未返回')
    expect(container.textContent).toContain('引擎已退出（killed）')
  })

  it('renders markdown text entries, not raw kind labels', () => {
    const frames = [
      fr('assistant_text', { seq: 1, turn: 1, text: 'plain **markdown**' }),
      fr('notice', { seq: 2, level: 'warning', content: 'careful', is_meta: false }),
    ]
    const { container } = render(<TranscriptPane frames={frames} />)
    expect(container.querySelector('.md-text strong')?.textContent).toBe('markdown')
    // 警告前缀与 TUI 同款 ASCII "! "（评审 Minor）。
    expect(container.textContent).toContain('! careful')
    expect(container.textContent).not.toContain('assistant_text')
  })
})
