// Header.tsx——顶栏（task-10 布局壳）：标题 + 服务状态点 + 主题切换器。
// 主题切换在 5 accent 预设 × dark/light 共 10 形态间循环，写 <html data-theme>
// （theme.css 的 10 块预设消费；预设名与 TUI accentPresets 对齐）。
import { useState } from 'react'

// Health 是 /api/healthz 探测三态（App 持有，Header 呈现）。
export type Health = 'checking' | 'up' | 'down'

export const THEME_PRESETS = [
  'deepseek-dark',
  'deepseek-light',
  'graphite-dark',
  'graphite-light',
  'ember-dark',
  'ember-light',
  'aurora-dark',
  'aurora-light',
  'midnight-dark',
  'midnight-light',
] as const

export type ThemePreset = (typeof THEME_PRESETS)[number]

export const DEFAULT_THEME: ThemePreset = 'deepseek-dark'

export function currentTheme(): ThemePreset {
  const t = document.documentElement.getAttribute('data-theme')
  return (THEME_PRESETS as readonly string[]).includes(t ?? '') ? (t as ThemePreset) : DEFAULT_THEME
}

export function applyTheme(theme: ThemePreset) {
  document.documentElement.setAttribute('data-theme', theme)
}

export interface HeaderProps {
  title: string
  health: Health
}

export default function Header({ title, health }: HeaderProps) {
  const [theme, setTheme] = useState<ThemePreset>(() => currentTheme())
  const cycleTheme = () => {
    const next = THEME_PRESETS[(THEME_PRESETS.indexOf(theme) + 1) % THEME_PRESETS.length]
    applyTheme(next)
    setTheme(next)
  }
  return (
    <header className="app-header">
      <h1 className="app-title">{title}</h1>
      <span
        className={`status-dot status-dot--${health}`}
        role="status"
        aria-label={`服务状态: ${health}`}
        title={`服务状态: ${health}`}
      />
      <span className="header-spacer" />
      <button type="button" className="theme-button" onClick={cycleTheme} title="切换主题（accent 预设 × 明暗循环）">
        主题：{theme}
      </button>
    </header>
  )
}
