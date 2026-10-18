// MarkdownText.tsx——markdown 渲染（marked）+ 代码高亮（highlight.js 常用语言集）
// + DOMPurify 消毒。XSS 防线（Review Focus 5）：任何 markdown/HTML 注入面必须过
// DOMPurify——marked 允许内联 HTML 透传，脚本/事件属性在消毒层剥离；消毒作用于
// 高亮后的最终 HTML（单一收口面，无旁路）。
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/common'
import { marked, type RendererObject, type Tokens } from 'marked'

// escapeHtml 高亮退避路径的转义（hljs.highlight 内部自转义，此处兜底非法语言/
// 异常情形——绝不让原文以 HTML 形态进入 pre）。
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

const renderer: RendererObject = {
  // marked v13+ 渲染器签名：token 对象（v18 一致）。围栏代码块经 highlight.js
  // 着色；无语言标注 → 自动探测（注册范围内），未知语言 → 转义原文不高亮。
  code({ text, lang }: Tokens.Code): string {
    const language = (lang ?? '').trim().split(/\s+/)[0] ?? ''
    try {
      if (language && hljs.getLanguage(language)) {
        const value = hljs.highlight(text, { language, ignoreIllegals: true }).value
        return `<pre><code class="hljs language-${language}">${value}</code></pre>`
      }
      if (language === '') {
        return `<pre><code class="hljs">${hljs.highlightAuto(text).value}</code></pre>`
      }
    } catch {
      // 高亮异常（病态语法等）：退避转义原文——内容不丢，安全不破。
    }
    return `<pre><code class="hljs">${escapeHtml(text)}</code></pre>`
  },
}

marked.use({ gfm: true, breaks: false, renderer })

export default function MarkdownText({ text }: { text: string }) {
  const raw = marked.parse(text) as string
  // DOMPurify：默认 HTML profile + 禁 style/form（缩小注入面）；脚本与 on* 事件
  // 属性默认即剥离。
  const html = DOMPurify.sanitize(raw, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'form'],
    FORBID_ATTR: ['style'],
  })
  return <div className="md-text" dangerouslySetInnerHTML={{ __html: html }} />
}
