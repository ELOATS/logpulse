import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Check, ChevronDown, ChevronRight, Copy, Search, SearchX, X } from 'lucide-react'
import clsx from 'clsx'
import { api, type Level, type LogRow } from '../lib/api'
import { fmtCompact, fmtNumber, fmtTime } from '../lib/format'
import { Card, Empty, ErrorBox, Highlight, LevelBadge, PageHeader, SkeletonRows, buttonClass, codeBlockClass, ghostButtonClass, inputClass } from '../components/ui'

const PAGE_SIZE = 50
const LEVEL_CHIPS: { label: string; levels: Level[]; cls: string }[] = [
  { label: '错误', levels: ['ERROR', 'FATAL'], cls: 'data-[on=true]:border-red-300 data-[on=true]:bg-red-50 data-[on=true]:text-red-700 dark:data-[on=true]:border-red-500/40 dark:data-[on=true]:bg-red-500/15 dark:data-[on=true]:text-red-300' },
  { label: '告警', levels: ['WARN'], cls: 'data-[on=true]:border-amber-300 data-[on=true]:bg-amber-50 data-[on=true]:text-amber-700 dark:data-[on=true]:border-amber-500/40 dark:data-[on=true]:bg-amber-500/15 dark:data-[on=true]:text-amber-300' },
  { label: '信息', levels: ['INFO'], cls: 'data-[on=true]:border-sky-300 data-[on=true]:bg-sky-50 data-[on=true]:text-sky-700 dark:data-[on=true]:border-sky-500/40 dark:data-[on=true]:bg-sky-500/15 dark:data-[on=true]:text-sky-300' },
  { label: '调试', levels: ['DEBUG'], cls: 'data-[on=true]:border-zinc-400 data-[on=true]:bg-zinc-100 data-[on=true]:text-zinc-700 dark:data-[on=true]:bg-zinc-800 dark:data-[on=true]:text-zinc-300' },
]
const rowAccent: Partial<Record<Level, string>> = {
  FATAL: 'border-l-fuchsia-500', ERROR: 'border-l-red-500', WARN: 'border-l-amber-400',
}

export default function Logs() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const level = params.get('level') ?? ''
  const service = params.get('service') ?? ''
  const patternId = params.get('pattern_id') ?? ''
  const from = params.get('from') ?? ''
  const to = params.get('to') ?? ''
  const page = Number(params.get('page') ?? 0)
  const [draft, setDraft] = useState(q)
  const searchRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const activeLevels = new Set(level.split(',').filter(Boolean))

  useEffect(() => setDraft(q), [q])

  // 快捷键：按 / 聚焦搜索框
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName)) {
        e.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const services = useQuery({ queryKey: ['services'], queryFn: api.services })
  const logs = useQuery({
    queryKey: ['logs', q, level, service, patternId, from, to, page],
    queryFn: () => api.logs({ q, level, service, pattern_id: patternId, from, to, limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
    placeholderData: keepPreviousData,
  })

  const update = (patch: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    if (!('page' in patch)) next.delete('page')
    setParams(next)
  }
  const toggleLevels = (levels: Level[]) => {
    const on = levels.every((l) => activeLevels.has(l))
    levels.forEach((l) => (on ? activeLevels.delete(l) : activeLevels.add(l)))
    update({ level: [...activeLevels].join(',') })
  }
  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    update({ q: draft.trim() })
  }
  const goPage = (n: number) => {
    update({ page: n ? String(n) : '' })
    listRef.current?.scrollIntoView({ block: 'start' })
  }
  const hasFilters = !!(q || level || service || patternId || from || to)
  const clearAll = () => {
    setDraft('')
    setParams({})
  }

  const total = logs.data?.total ?? 0
  const capped = !!logs.data?.total_capped
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const hasNext = page + 1 < pages || (capped && (logs.data?.items.length ?? 0) === PAGE_SIZE)
  const totalLabel = `${fmtNumber(total)}${capped ? '+' : ''}`
  const start = total ? page * PAGE_SIZE + 1 : 0
  const end = Math.min(total, (page + 1) * PAGE_SIZE)

  return (
    <>
      <PageHeader title="日志检索" description={<>全文检索支持 <code className="text-xs">timeout -health</code>、<code className="text-xs">"connection refused"</code> 等写法</>} />
      <Card bodyClassName="space-y-3">
        <form onSubmit={onSubmit} className="flex gap-2">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-zinc-400" />
            <input
              ref={searchRef}
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Escape') { setDraft(''); if (q) update({ q: '' }) } }}
              placeholder="搜索日志内容…"
              aria-label="搜索日志"
              className={`${inputClass} w-full pr-10 pl-9`}
            />
            <kbd className="pointer-events-none absolute top-2 right-2.5 hidden rounded border border-zinc-200 px-1.5 font-mono text-[11px] text-zinc-400 sm:block dark:border-zinc-700">/</kbd>
          </div>
          <button className={buttonClass}>搜索</button>
        </form>
        <div className="flex flex-wrap items-center gap-2">
          {LEVEL_CHIPS.map((c) => (
            <button
              key={c.label}
              data-on={c.levels.every((l) => activeLevels.has(l))}
              onClick={() => toggleLevels(c.levels)}
              className={clsx('h-8 rounded-full border border-zinc-300 px-3 text-xs font-medium text-zinc-600 transition-colors hover:bg-zinc-50 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800', c.cls)}
            >
              {c.label}
            </button>
          ))}
          <select value={service} onChange={(e) => update({ service: e.target.value })} className={`${inputClass} h-8 text-xs`} aria-label="服务">
            <option value="">全部服务</option>
            {services.data?.map((s) => <option key={s} value={s}>{s || '(未命名)'}</option>)}
          </select>
          {patternId && <FilterChip label={`模式 #${patternId}`} onClear={() => update({ pattern_id: '' })} />}
          {(from || to) && (
            <FilterChip label={`${from ? fmtTime(from, false) : '…'} ~ ${to ? fmtTime(to, false) : '…'}`} onClear={() => update({ from: '', to: '' })} />
          )}
          {hasFilters && (
            <button onClick={clearAll} className="ml-auto text-xs text-zinc-500 hover:text-zinc-800 dark:hover:text-zinc-200">清除筛选</button>
          )}
        </div>
      </Card>

      <div ref={listRef} className="scroll-mt-4" />
      <Card
        className="mt-4"
        flush
        title={logs.data ? <>共 {totalLabel} 条{total > 0 && <span className="ml-2 font-normal text-zinc-500">显示 {fmtNumber(start)}–{fmtNumber(end)}</span>}</> : '加载中…'}
      >
        {logs.error ? (
          <div className="p-4"><ErrorBox error={logs.error} /></div>
        ) : logs.isLoading ? (
          <div className="p-4"><SkeletonRows rows={10} /></div>
        ) : logs.data?.items.length === 0 ? (
          <Empty icon={<SearchX className="size-8 text-zinc-300 dark:text-zinc-600" />}>
            没有匹配的日志
            {hasFilters && <button onClick={clearAll} className="text-indigo-600 hover:underline dark:text-indigo-400">清除筛选条件</button>}
          </Empty>
        ) : (
          <div className={clsx('divide-y divide-zinc-100 dark:divide-zinc-800', logs.isPlaceholderData && 'opacity-60')}>
            <div className="hidden grid-cols-[1.25rem_8.5rem_3.5rem_9rem_1fr] gap-3 bg-zinc-50 px-4 py-2 text-xs font-medium text-zinc-500 md:grid dark:bg-zinc-800/40">
              <span /><span>时间</span><span>级别</span><span>服务</span><span>消息</span>
            </div>
            {logs.data?.items.map((row) => <LogRowView key={row.id} row={row} query={q} />)}
          </div>
        )}
      </Card>

      {(pages > 1 || hasNext) && (
        <div className="mt-4 flex items-center justify-end gap-2 text-sm">
          <button className={ghostButtonClass} disabled={page === 0} onClick={() => goPage(page - 1)}>上一页</button>
          <span className="px-2 tabular-nums text-zinc-500">{page + 1} / {fmtNumber(pages)}{capped ? '+' : ''}</span>
          <button className={ghostButtonClass} disabled={!hasNext} onClick={() => goPage(page + 1)}>下一页</button>
        </div>
      )}
    </>
  )
}

function FilterChip({ label, onClear }: { label: string; onClear: () => void }) {
  return (
    <span className="inline-flex h-8 items-center gap-1 rounded-full bg-indigo-50 pr-1.5 pl-3 text-xs text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300">
      {label}
      <button onClick={onClear} aria-label={`清除 ${label}`} className="rounded-full p-0.5 hover:bg-indigo-100 dark:hover:bg-indigo-500/25"><X className="size-3.5" /></button>
    </span>
  )
}

function LogRowView({ row, query }: { row: LogRow; query: string }) {
  const [open, setOpen] = useState(false)
  const attrs = Object.entries(row.attributes ?? {}).filter(([k]) => k !== 'stack' && k !== 'stacktrace')
  const stack = (row.attributes?.stack ?? row.attributes?.stacktrace) as string | undefined
  const location = row.attributes?.error_location as string | undefined
  return (
    <div className={clsx('border-l-2', rowAccent[row.level] ?? 'border-l-transparent')}>
      <button
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="grid w-full grid-cols-[1.25rem_1fr] gap-x-3 gap-y-1 px-4 py-2 text-left hover:bg-zinc-50 md:grid-cols-[1.25rem_8.5rem_3.5rem_9rem_1fr] md:items-baseline dark:hover:bg-zinc-800/40"
      >
        <span className="row-span-2 pt-0.5 text-zinc-400 md:row-span-1">{open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}</span>
        {/* 移动端：时间·级别·服务在一行 */}
        <span className="flex items-center gap-2 text-xs md:contents">
          <span className="font-mono whitespace-nowrap text-zinc-500 tabular-nums" title={fmtTime(row.timestamp)}>{fmtCompact(row.timestamp)}</span>
          <span><LevelBadge level={row.level} /></span>
          <span className="truncate text-zinc-600 dark:text-zinc-400">{row.service}</span>
        </span>
        <span className="min-w-0 font-mono text-xs leading-5 break-all">
          <Highlight text={row.message} query={query} />
          {location && <span className="block truncate text-[11px] text-zinc-400">@ {location}</span>}
        </span>
      </button>
      {open && <LogDetail row={row} attrs={attrs} stack={stack} />}
    </div>
  )
}

function LogDetail({ row, attrs, stack }: { row: LogRow; attrs: [string, unknown][]; stack?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => navigator.clipboard?.writeText(row.raw).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500) })
  return (
    <div className="space-y-3 bg-zinc-50/70 px-4 pt-1 pb-4 text-xs md:pl-12 dark:bg-zinc-800/20">
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
        <dt className="text-zinc-500">时间</dt><dd className="font-mono">{fmtTime(row.timestamp)}</dd>
        <dt className="text-zinc-500">格式</dt><dd>{row.format}</dd>
        {row.host && (<><dt className="text-zinc-500">主机</dt><dd>{row.host}</dd></>)}
        {row.pattern_id && (
          <><dt className="text-zinc-500">模式</dt><dd><Link className="text-indigo-600 hover:underline dark:text-indigo-400" to={`/logs?pattern_id=${row.pattern_id}`}>#{row.pattern_id} 查看同类日志</Link></dd></>
        )}
        {attrs.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="font-mono text-zinc-500">{k}</dt>
            <dd className="font-mono break-all">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</dd>
          </div>
        ))}
      </dl>
      {stack && (
        <div>
          <div className="mb-1 text-zinc-500">堆栈</div>
          <pre className={`${codeBlockClass} max-h-72`}>{stack}</pre>
        </div>
      )}
      <div>
        <div className="mb-1 flex items-center justify-between text-zinc-500">
          原始日志
          <button onClick={copy} className="inline-flex items-center gap-1 hover:text-zinc-800 dark:hover:text-zinc-200">
            {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}{copied ? '已复制' : '复制'}
          </button>
        </div>
        <pre className={`${codeBlockClass} max-h-72 break-all whitespace-pre-wrap`}>{row.raw}</pre>
      </div>
    </div>
  )
}
