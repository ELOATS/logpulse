import type { ReactNode } from 'react'
import clsx from 'clsx'
import type { Level, Severity } from '../lib/api'

export function Card({ title, actions, children, className }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={clsx('rounded-lg border border-zinc-200 bg-white dark:border-zinc-800 dark:bg-zinc-900', className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-2 border-b border-zinc-100 px-4 py-3 dark:border-zinc-800">
          <h2 className="text-sm font-medium">{title}</h2>
          {actions}
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  )
}

export function PageHeader({ title, description, actions }: { title: string; description?: string; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold">{title}</h1>
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
  return <span className={clsx('inline-block rounded px-1.5 py-0.5 font-mono text-xs font-medium', levelStyles[level])}>{level}</span>
}

const severityStyles: Record<Severity, string> = {
  low: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/10 dark:text-amber-300 dark:ring-amber-500/30',
  medium: 'bg-orange-50 text-orange-700 ring-orange-200 dark:bg-orange-500/10 dark:text-orange-300 dark:ring-orange-500/30',
  high: 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-500/10 dark:text-red-300 dark:ring-red-500/30',
}
const severityLabel: Record<Severity, string> = { low: '低', medium: '中', high: '高' }

export function SeverityBadge({ severity }: { severity: Severity }) {
  return (
    <span className={clsx('inline-block rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset', severityStyles[severity])}>
      {severityLabel[severity]}
    </span>
  )
}

export function StatCard({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: 'danger' }) {
  return (
    <div className="rounded-lg border border-zinc-200 bg-white p-4 dark:border-zinc-800 dark:bg-zinc-900">
      <div className="text-xs text-zinc-500 dark:text-zinc-400">{label}</div>
      <div className={clsx('mt-1 text-2xl font-semibold tabular-nums', tone === 'danger' && 'text-red-600 dark:text-red-400')}>{value}</div>
      {hint && <div className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">{hint}</div>}
    </div>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="py-10 text-center text-sm text-zinc-500 dark:text-zinc-400">{children}</div>
}

export function ErrorBox({ error }: { error: unknown }) {
  return (
    <div className="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
      {error instanceof Error ? error.message : String(error)}
    </div>
  )
}

export const inputClass =
  'rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/20 dark:border-zinc-700 dark:bg-zinc-900'

export const buttonClass =
  'inline-flex items-center gap-1.5 rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50'

export const ghostButtonClass =
  'inline-flex items-center gap-1.5 rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:hover:bg-zinc-800'
