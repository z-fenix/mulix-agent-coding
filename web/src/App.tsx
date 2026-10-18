// App.tsx——应用壳（Task 9 空壳形态）：顶栏「mulix」标题 + /api/healthz 状态点
// （design §8.2 布局顶栏最小形态；会话/转录/输入框随后续任务挂载）。
import { useEffect, useState } from 'react'

type Health = 'checking' | 'up' | 'down'

export default function App() {
  const [health, setHealth] = useState<Health>('checking')

  useEffect(() => {
    const ctrl = new AbortController()
    fetch('/api/healthz', { signal: ctrl.signal })
      .then((res) => {
        setHealth(res.ok ? 'up' : 'down')
      })
      .catch(() => {
        // 网络失败/中止：中止由卸载触发，不得再改状态；其余按不可达处理。
        if (!ctrl.signal.aborted) setHealth('down')
      })
    return () => ctrl.abort()
  }, [])

  return (
    <div className="app">
      <header className="app-header">
        <h1 className="app-title">mulix</h1>
        <span
          className={`status-dot status-dot--${health}`}
          role="status"
          aria-label={`服务状态: ${health}`}
          title={`服务状态: ${health}`}
        />
      </header>
    </div>
  )
}
