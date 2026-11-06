import { useState, type FormEvent } from 'react'
import { Link, useSearchParams } from 'react-router'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Search, X } from 'lucide-react'
import { api, type Level, type LogRow } from '../lib/api'
import { fmtNumber, fmtTime } from '../lib/format'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, buttonClass, ghostButtonClass, inputClass } from '../components/ui'

const PAGE_SIZE = 50
const LEVELS: Level[] = ['DEBUG', 'INFO', 'WARN', 'ERROR', 'FATAL']

export default function Logs() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const level = params.get('level') ?? ''
  const service = params.get('service') ?? ''
  const patternId = params.get('pattern_id') ?? ''
  const page = Number(params.get('page') ?? 0)
  const [draft, setDraft] = useState(q)

  const services = useQuery({ queryKey: ['services'], queryFn: api.services })
  const logs = useQuery({
    queryKey: ['logs', q, level, service, patternId, page],
    queryFn: () => api.logs({ q, level, service, pattern_id: patternId, limit: PAGE_SIZE, offset: page * PAGE_SIZE }),
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
  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    update({ q: draft.trim() })
  }

  const total = logs.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <>
      <PageHeader title="日志检索" description="支持全文检索（如 timeout -health、&quot;connection refused&quot;）与级别、服务筛选" />
      <Card>
        <form onSubmit={onSubmit} className="flex flex-wrap gap-2">
          <div className="relative min-w-60 flex-1">
            <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-zinc-400" />
            <input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="搜索日志内容…" className={`${inputClass} w-full pl-8`} />
          </div>
          <select value={level} onChange={(e) => update({ level: e.target.value })} className={inputClass} aria-label="级别">
            <option value="">全部级别</option>
            <option value="ERROR,FATAL">ERROR + FATAL</option>
            {LEVELS.map((l) => <option key={l} value={l}>{l}</option>)}
          </select>
          <select value={service} onChange={(e) => update({ service: e.target.value })} className={inputClass} aria-label="服务">
            <option value="">全部服务</option>
            {services.data?.map((s) => <option key={s} value={s}>{s || '(未命名)'}</option>)}
          </select>
          <button className={buttonClass}>搜索</button>
        </form>
        {patternId && (
          <div className="mt-3 inline-flex items-center gap-1 rounded-full bg-indigo-50 px-2.5 py-1 text-xs text-indigo-700 dark:bg-indigo-500/15 dark:text-indigo-300">
            模式 #{patternId}
            <button onClick={() => update({ pattern_id: '' })} aria-label="清除模式筛选"><X className="size-3.5" /></button>
          </div>
        )}
      </Card>

      <Card className="mt-4" title={`共 ${fmtNumber(total)} 条`}>
        {logs.error ? (
          <ErrorBox error={logs.error} />
        ) : logs.data?.items.length === 0 ? (
          <Empty>没有匹配的日志</Empty>
        ) : (
          <div className="-mx-4 -my-4 overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="bg-zinc-50 text-left text-xs text-zinc-500 dark:bg-zinc-800/50 dark:text-zinc-400">
                <tr>
                  <th className="w-6 px-2 py-2" />
                  <th className="px-2 py-2 font-medium whitespace-nowrap">时间</th>
                  <th className="px-2 py-2 font-medium">级别</th>
                  <th className="px-2 py-2 font-medium">服务</th>
                  <th className="px-2 py-2 font-medium">消息</th>
                </tr>
              </thead>
              <tbody className={logs.isPlaceholderData ? 'opacity-60' : ''}>
                {logs.data?.items.map((row) => <LogRowView key={row.id} row={row} />)}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {pages > 1 && (
        <div className="mt-4 flex items-center justify-end gap-2 text-sm">
          <button className={ghostButtonClass} disabled={page === 0} onClick={() => update({ page: String(page - 1) })}>上一页</button>
          <span className="tabular-nums text-zinc-500">{page + 1} / {pages}</span>
          <button className={ghostButtonClass} disabled={page + 1 >= pages} onClick={() => update({ page: String(page + 1) })}>下一页</button>
        </div>
      )}
    </>
  )
}

function LogRowView({ row }: { row: LogRow }) {
  const [open, setOpen] = useState(false)
  const attrs = Object.entries(row.attributes ?? {})
  return (
    <>
      <tr onClick={() => setOpen(!open)} className="cursor-pointer border-t border-zinc-100 align-top hover:bg-zinc-50 dark:border-zinc-800 dark:hover:bg-zinc-800/40">
        <td className="px-2 py-1.5 text-zinc-400">{open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}</td>
        <td className="px-2 py-1.5 font-mono text-xs whitespace-nowrap text-zinc-500">{fmtTime(row.timestamp)}</td>
        <td className="px-2 py-1.5"><LevelBadge level={row.level} /></td>
        <td className="px-2 py-1.5 text-xs whitespace-nowrap">{row.service}</td>
        <td className="px-2 py-1.5 font-mono text-xs break-all">{row.message}</td>
      </tr>
      {open && (
        <tr className="bg-zinc-50 dark:bg-zinc-800/30">
          <td />
          <td colSpan={4} className="px-2 py-3 text-xs">
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
              <dt className="text-zinc-500">格式</dt><dd>{row.format}</dd>
              {row.host && (<><dt className="text-zinc-500">主机</dt><dd>{row.host}</dd></>)}
              {row.pattern_id && (<><dt className="text-zinc-500">模式</dt><dd><Link className="text-indigo-600 hover:underline dark:text-indigo-400" to={`/logs?pattern_id=${row.pattern_id}`}>#{row.pattern_id}（查看同类日志）</Link></dd></>)}
              {attrs.map(([k, v]) => (
                <div key={k} className="contents"><dt className="font-mono text-zinc-500">{k}</dt><dd className="font-mono break-all">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</dd></div>
              ))}
            </dl>
            <pre className="mt-3 rounded bg-zinc-900 p-3 break-all whitespace-pre-wrap text-zinc-100">{row.raw}</pre>
          </td>
        </tr>
      )}
    </>
  )
}
