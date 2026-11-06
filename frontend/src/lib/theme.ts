import { useEffect, useState } from 'react'

export type ThemePref = 'system' | 'light' | 'dark'
const KEY = 'logpulse-theme'
const media = () => window.matchMedia('(prefers-color-scheme: dark)')

function readPref(): ThemePref {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark') return v
  } catch {
    /* 隐私模式等情况下 localStorage 不可用 */
  }
  return 'system'
}

function apply(pref: ThemePref) {
  const dark = pref === 'dark' || (pref === 'system' && media().matches)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

export function useTheme() {
  const [pref, setPref] = useState<ThemePref>(readPref)
  useEffect(() => {
    apply(pref)
    try {
      if (pref === 'system') localStorage.removeItem(KEY)
      else localStorage.setItem(KEY, pref)
    } catch {
      /* 忽略 */
    }
    if (pref !== 'system') return
    const m = media()
    const onChange = () => apply('system')
    m.addEventListener('change', onChange)
    return () => m.removeEventListener('change', onChange)
  }, [pref])
  return [pref, setPref] as const
}

/** 当前是否处于暗色（供图表等需要具体颜色值的地方使用） */
export function useIsDark() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  useEffect(() => {
    const obs = new MutationObserver(() => setDark(document.documentElement.classList.contains('dark')))
    obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])
  return dark
}
