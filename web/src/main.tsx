// main.tsx——web 前端入口：自托管字体（IBM Plex Sans 400/500/600 + Mono 400/500）
// 与视觉令牌先行加载（design §8），随后挂载 React。data-theme 缺省在 index.html
// <html data-theme="deepseek-dark"> 上声明（JS 加载前即生效）。
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import './theme.css'

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'

const rootEl = document.getElementById('root')
if (!rootEl) {
  throw new Error('missing #root mount point')
}

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
