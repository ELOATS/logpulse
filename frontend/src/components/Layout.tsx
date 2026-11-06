import { useEffect, useRef } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity, AlertTriangle, LayoutDashboard, Layers, Monitor, Moon, ScrollText, Sun, Upload } from 'lucide-react'
import clsx from 'clsx'
import { api } from '../lib/api'
import { useTheme, type ThemePref } from '../lib/theme'

const nav = [
  { to: '/', label: '总览', short: '总览', icon: LayoutDashboard, end: true },
  { to: '/logs', label: '日志检索', short: '日志', icon: ScrollText },
  { to: '/patterns', label: '日志模式', short: '模式', icon: Layers },
  { to: '/anomalies', label: '异常检测', short: '异常', icon: AlertTriangle },
  { to: '/ingest', label: '导入日志', short: '导入', icon: Upload },
]

const themeOptions: { value: ThemePref; icon: typeof Sun; label: string }[] = [
  { value: 'light', icon: Sun, label: '浅色' },
  { value: 'system', icon: Monitor, label: '跟随系统' },
  { value: 'dark', icon: Moon, label: '深色' },
]

function ThemeSwitch() {
  const [pref, setPref] = useTheme()
  return (
    <div className="inline-flex rounded-lg border border-zinc-200 p-0.5 dark:border-zinc-800" role="radiogroup" aria-label="主题">
      {themeOptions.map(({ value, icon: Icon, label }) => (
        <button
          key={value}
          role="radio"
          aria-checked={pref === value}
          title={label}
          aria-label={label}
          onClick={() => setPref(value)}
          className={clsx(
            'rounded-md p-1.5 transition-colors',
            pref === value
              ? 'bg-zinc-100 text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
              : 'text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200',
          )}
        >
          <Icon className="size-3.5" />
        </button>
      ))}
    </div>
  )
}

function Brand() {
  return (
    <div className="flex items-center gap-2 text-base font-semibold tracking-tight">
      <span className="grid size-7 place-items-center rounded-lg bg-indigo-600 text-white">
        <Activity className="size-4" />
      </span>
      LogPulse
    </div>
  )
}

export default function Layout() {
  const mainRef = useRef<HTMLElement>(null)
  const { pathname, hash } = useLocation()
  useEffect(() => {
    if (!hash) mainRef.current?.scrollTo({ top: 0 })
  }, [pathname, hash])
  const { data } = useQuery({ queryKey: ['overview', 'all'], queryFn: () => api.overview(), refetchInterval: 30_000 })
  const open = data?.open_anomalies ?? 0
  const badge = (to: string) =>
    to === '/anomalies' && open > 0 ? (
      <span className="rounded-full bg-red-500 px-1.5 text-[11px] leading-4 font-semibold text-white tabular-nums">{open > 99 ? '99+' : open}</span>
    ) : null

  return (
    <div className="flex h-full">
      {/* 桌面端侧边栏 */}
      <aside className="hidden w-56 shrink-0 flex-col border-r border-zinc-200 bg-white md:flex dark:border-zinc-800 dark:bg-zinc-900">
        <div className="px-4 py-4"><Brand /></div>
        <nav className="flex flex-1 flex-col gap-0.5 px-3">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                clsx(
                  'flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors',
                  isActive
                    ? 'bg-indigo-50 font-medium text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300'
                    : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800',
                )
              }
            >
              <Icon className="size-4" />
              {label}
              <span className="ml-auto">{badge(to)}</span>
            </NavLink>
          ))}
        </nav>
        <div className="flex items-center justify-between border-t border-zinc-100 px-4 py-3 text-xs text-zinc-400 dark:border-zinc-800">
          主题
          <ThemeSwitch />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* 移动端顶栏 */}
        <header className="flex items-center justify-between border-b border-zinc-200 bg-white px-4 py-2.5 md:hidden dark:border-zinc-800 dark:bg-zinc-900">
          <Brand />
          <ThemeSwitch />
        </header>
        <main ref={mainRef} className="min-w-0 flex-1 overflow-y-auto pb-20 md:pb-0">
          <div className="mx-auto max-w-7xl px-4 py-5 md:px-8 md:py-7">
            <Outlet />
          </div>
        </main>
      </div>

      {/* 移动端底部导航 */}
      <nav className="fixed inset-x-0 bottom-0 z-20 grid grid-cols-5 border-t border-zinc-200 bg-white/95 pb-[env(safe-area-inset-bottom)] backdrop-blur md:hidden dark:border-zinc-800 dark:bg-zinc-900/95">
        {nav.map(({ to, short, icon: Icon, end }) => (
          <NavLink
            key={to}
            to={to}
            end={end}
            className={({ isActive }) =>
              clsx('relative flex flex-col items-center gap-0.5 py-2 text-[11px]', isActive ? 'text-indigo-600 dark:text-indigo-400' : 'text-zinc-500')
            }
          >
            <Icon className="size-5" />
            {short}
            <span className="absolute top-1 left-1/2 ml-2">{badge(to)}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  )
}
