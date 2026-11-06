import { NavLink, Outlet } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Activity, AlertTriangle, LayoutDashboard, Layers, ScrollText, Upload } from 'lucide-react'
import clsx from 'clsx'
import { api } from '../lib/api'

const nav = [
  { to: '/', label: '总览', icon: LayoutDashboard, end: true },
  { to: '/logs', label: '日志检索', icon: ScrollText },
  { to: '/patterns', label: '日志模式', icon: Layers },
  { to: '/anomalies', label: '异常检测', icon: AlertTriangle },
  { to: '/ingest', label: '导入日志', icon: Upload },
]

export default function Layout() {
  const { data } = useQuery({ queryKey: ['overview'], queryFn: () => api.overview(), refetchInterval: 30_000 })
  return (
    <div className="flex h-full flex-col md:flex-row">
      <aside className="flex shrink-0 flex-col border-b border-zinc-200 bg-white md:w-56 md:border-r md:border-b-0 dark:border-zinc-800 dark:bg-zinc-900">
        <div className="flex items-center gap-2 px-5 py-4 text-lg font-semibold">
          <Activity className="size-5 text-indigo-600 dark:text-indigo-400" />
          LogPulse
        </div>
        <nav className="flex gap-1 overflow-x-auto px-3 pb-3 md:flex-col md:pb-0">
          {nav.map(({ to, label, icon: Icon, end }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                clsx(
                  'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm whitespace-nowrap transition-colors',
                  isActive
                    ? 'bg-indigo-50 font-medium text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300'
                    : 'text-zinc-600 hover:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800',
                )
              }
            >
              <Icon className="size-4" />
              {label}
              {to === '/anomalies' && !!data?.open_anomalies && (
                <span className="ml-auto rounded-full bg-red-500 px-1.5 text-xs font-medium text-white">
                  {data.open_anomalies}
                </span>
              )}
            </NavLink>
          ))}
        </nav>
      </aside>
      <main className="min-w-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-7xl px-4 py-6 md:px-8">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
