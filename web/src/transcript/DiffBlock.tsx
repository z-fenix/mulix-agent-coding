// DiffBlock.tsx——unified diff 解析与渲染（自研，无第三方 diff 依赖）。
// 解析器覆盖常用形态：diff --git / index / --- / +++ 文件头、@@ hunk 头
// （行数段可省略）、+/-/空格 上下文行、\ No newline 标记。产出行号对齐的
// 加/删/上下文行，配色走 theme.css 的 --diff-* 令牌（底色 + marker 前景）。
// 容错：hunk 之前的未识别行按 meta 收（非 diff 文本不冒充上下文）；空行在
// hunk 内按上下文行推进双侧行号（尾随空白被裁的 diff 常态）。
import { useMemo } from 'react'

export type DiffRowType = 'add' | 'del' | 'context' | 'meta'

export interface DiffRow {
  type: DiffRowType
  oldNo: number | null
  newNo: number | null
  text: string
}

const HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/
const HEADER_PREFIXES = ['diff --git ', 'index ', 'new file mode ', 'deleted file mode ', 'old mode ', 'new mode ', 'similarity index ', 'rename from ', 'rename to ']

// looksLikeUnifiedDiff 快速判形（工具输出是否按 diff 视图渲染）。
export function looksLikeUnifiedDiff(s: string): boolean {
  return HUNK_RE.test(s) || /^diff --git /m.test(s)
}

export function parseUnifiedDiff(source: string): DiffRow[] {
  const rows: DiffRow[] = []
  let oldNo = 0
  let newNo = 0
  let inHeader = false // ---/+++ 之间与 mode 类残余头行区
  for (const raw of source.split('\n')) {
    const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw
    const hunk = HUNK_RE.exec(line)
    if (hunk) {
      rows.push({ type: 'meta', oldNo: null, newNo: null, text: line })
      oldNo = Number(hunk[1])
      newNo = Number(hunk[3])
      inHeader = false
      continue
    }
    if (line.startsWith('--- ') || line.startsWith('+++ ')) {
      rows.push({ type: 'meta', oldNo: null, newNo: null, text: line })
      inHeader = true
      continue
    }
    if (HEADER_PREFIXES.some((p) => line.startsWith(p))) {
      rows.push({ type: 'meta', oldNo: null, newNo: null, text: line })
      inHeader = true
      continue
    }
    if (inHeader) {
      // 头区残余行（diff --git 与首个 hunk 之间的其他元数据）按 meta 容错收。
      rows.push({ type: 'meta', oldNo: null, newNo: null, text: line })
      continue
    }
    if (line.startsWith('+')) {
      rows.push({ type: 'add', oldNo: null, newNo, text: line.slice(1) })
      newNo++
      continue
    }
    if (line.startsWith('-')) {
      rows.push({ type: 'del', oldNo, newNo: null, text: line.slice(1) })
      oldNo++
      continue
    }
    if (line.startsWith('\\')) {
      // "\ No newline at end of file"——标记行，不推进行号。
      rows.push({ type: 'meta', oldNo: null, newNo: null, text: line })
      continue
    }
    if (oldNo === 0 && newNo === 0) {
      // 首个 hunk 之前的未识别行：非 diff 正文（前言等）按 meta 收，不冒充上下文。
      rows.push({ type: 'meta', oldNo: null, newNo: null, text: line })
      continue
    }
    // 上下文行（含被裁掉前导空格的空行）——双侧行号推进。
    const text = line.startsWith(' ') ? line.slice(1) : line
    rows.push({ type: 'context', oldNo, newNo, text })
    oldNo++
    newNo++
  }
  return rows
}

export default function DiffBlock({ diff }: { diff: string }) {
  const rows = useMemo(() => parseUnifiedDiff(diff), [diff])
  return (
    <div className="diff-block" role="table" aria-label="diff">
      {rows.map((r, i) => (
        <div key={i} className={`diff-row diff-row--${r.type}`} role="row">
          <span className="diff-no diff-no--old" aria-label="old line">
            {r.oldNo ?? ''}
          </span>
          <span className="diff-no diff-no--new" aria-label="new line">
            {r.newNo ?? ''}
          </span>
          <span className="diff-marker" aria-hidden>
            {r.type === 'add' ? '+' : r.type === 'del' ? '-' : ''}
          </span>
          <span className="diff-text">{r.text}</span>
        </div>
      ))}
    </div>
  )
}
