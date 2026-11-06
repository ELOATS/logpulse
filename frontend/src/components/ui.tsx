import type { ReactNode } from 'react'
import { Link } from 'react-router'
import clsx from 'clsx'
import type { Level, Severity } from '../lib/api'

export function Card({ title, actions, children, className, bodyClassName, flush }: {
  title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string; bodyClassName?: string
  flush?: boolean // 内容区不留内边距（列表、表格）
}) {
  return (
    <section className={clsx('rounded-xl border border-zinc-200 bg-white shadow-xs dark:border-zinc-800 dark:bg-zinc-900', className)}>
      {(title || actions) && (
        <header className="flex min-h-12 items-center justify-between gap-2 border-b border-zinc-100 px-4 py-2.5 dark:border-zinc-800">
          <h2 className="text-sm font-semibold">{title}</h2>
          {actions}
        </header>
      )}
      <div className={clsx(!flush && 'p-4', bodyClassName)}>{children}</div>
    </section>
  )
}

export function PageHeader({ title, description, actions }: { title: string; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{description}</p>}
      </div>
      {actions}
    </div>
  )
}

const levelStyles: Record<Level, string> = {
  DEBUG: 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400',
  INFO: 'bg-sky-50 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300',
  WARN: 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  ERROR: 'bg-red-50 text-red-700 dark:bg-red-500/15 dark:text-red-300',
  FATAL: 'bg-fuchsia-50 text-fuchsia-700 dark:bg-fuchsia-500/15 dark:text-fuchsia-300',
}

export function LevelBadge({ level }: { level: Level }) {
  return <span className={clsx('inline-block w-12 shrink-0 rounded px-1 py-0.5 text-center font-mono text-[11px] font-semibold', levelStyles[level])}>{level}</span>
}

const severityStyles: Record<Severity, string> = {
  low: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
  medium: 'bg-orange-50 text-orange-700 ring-orange-200 dark:bg-orange-500/10 dark:text-orange-300 dark:ring-orange-500/30',
  high: 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-500/30',
}
export const severityLabel: Record<Severity, string> = { low: '低危', medium: '中危', high: '高危' }

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={clsx('inline-block shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset', severityStyles[severity])}>
      {severityLabel[severity]}
    </span>
  )
}

export function StatCard({ label, value, hint, tone, to, chart }: {
  label: string; value: ReactNode; hint?: ReactNode; tone?: 'danger' | 'warn'; to?: string; chart?: ReactNode
}) {
  const body = (
    <>
      <div className="flex items-center justify-between text-xs text-zinc-500 dark:text-zinc-400">{label}</div>
      <div className="mt-1 flex items-end justify-between gap-2">
        <div className={clsx('text-2xl font-semibold tracking-tight tabular-nums',
          tone === 'danger' && 'text-red-600 dark:text-red-400', tone === 'warn' && 'text-amber-600 dark:text-amber-400')}>
          {value}
        </div>
        {chart && <div className="h-9 w-24 shrink-0">{chart}</div>}
      </div>
      {hint && <div className="mt-1 truncate text-xs text-zinc-500 dark:text-zinc-400">{hint}</div>}
    </>
  )
  const cls = 'block rounded-xl border border-zinc-200 bg-white p-4 shadow-xs dark:border-zinc-800 dark:bg-zinc-900'
  return to ? (
    <Link to={to} className={clsx(cls, 'transition-colors hover:border-indigo-300 dark:hover:border-indigo-500/50')}>{body}</Link>
  ) : (
    <div className={cls}>{body}</div>
  )
}

export function Empty({ children, icon }: { children: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 py-10 text-center text-sm text-zinc-500 dark:text-zinc-400">
      {icon}
      {children}
    </div>
  )
}

export function ErrorBox({ error }: { error: unknown }) {
  return (
    <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
      {error instanceof Error ? error.message : String(error)}
    </div>
  )
}

export function Skeleton({ className }: { className?: string }) {
  return <div className={clsx('animate-pulse rounded-md bg-zinc-200/70 dark:bg-zinc-800', className)} />
}

export function SkeletonRows({ rows = 6 }: { rows?: number }) {
  return (
    <div className="space-y-3">
      {Array.from({ length: rows }, (_, i) => <Skeleton key={i} className="h-5" />)}
    </div>
  )
}

/** 分段选择器 */
export function Segmented<T extends string>({ value, onChange, options, size = 'md' }: {
  value: T; onChange: (v: T) => void; options: { value: T; label: ReactNode }[]; size?: 'sm' | 'md'
}) {
  return (
    <div className="inline-flex shrink-0 rounded-lg border border-zinc-200 bg-zinc-100/60 p-0.5 dark:border-zinc-800 dark:bg-zinc-900">
      {options.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          aria-pressed={value === o.value}
          className={clsx(
            'rounded-md whitespace-nowrap transition-colors',
            size === 'sm' ? 'px-2 py-0.5 text-xs' : 'px-3 py-1 text-sm',
            value === o.value
              ? 'bg-white font-medium text-zinc-900 shadow-xs dark:bg-zinc-700 dark:text-zinc-50'
              : 'text-zinc-500 hover:text-zinc-800 dark:text-zinc-400 dark:hover:text-zinc-200',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

/** 高亮模板中的占位符（<NUM>、<IP> …），让固定文本与可变部分一目了然 */
export function Template({ text, className }: { text: string; className?: string }) {
  const parts = text.split(/(<[A-Z?]+>\+?)/g)
  return (
    <span className={clsx('font-mono', className)}>
      {parts.map((p, i) =>
        /^<[A-Z?]+>\+?$/.test(p) ? (
          <span key={i} className="rounded bg-indigo-50 px-0.5 text-indigo-600 dark:bg-indigo-500/15 dark:text-indigo-300">{p}</span>
        ) : (
          <span key={i}>{p}</span>
        ),
      )}
    </span>
  )
}

/** 高亮搜索词 */
export function Highlight({ text, query }: { text: string; query: string }) {
  const terms = query
    .split(/\s+/)
    .map((t) => t.replace(/^[-"]+|"+$/g, ''))
    .filter((t) => t.length > 1 && !/^(or|and)$/i.test(t))
  if (!terms.length) return <>{text}</>
  const re = new RegExp(`(${terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'gi')
  return (
    <>
      {text.split(re).map((p, i) =>
        i % 2 === 1 ? <mark key={i} className="rounded-sm bg-yellow-200/80 text-inherit dark:bg-yellow-500/30">{p}</mark> : <span key={i}>{p}</span>,
      )}
    </>
  )
}

export const inputClass =
  'h-9 rounded-lg border border-zinc-300 bg-white px-3 text-sm outline-none transition-shadow focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-zinc-700 dark:bg-zinc-900'

export const buttonClass =
  'inline-flex h-9 items-center justify-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 text-sm font-medium text-white shadow-xs transition-colors hover:bg-indigo-500 disabled:opacity-50'

export const ghostButtonClass =
  'inline-flex items-center justify-center gap-1.5 rounded-lg border border-zinc-300 bg-white px-3 py-1.5 text-sm transition-colors hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:bg-zinc-900 dark:hover:bg-zinc-800'
