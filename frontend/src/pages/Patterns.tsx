import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Layers, Search } from 'lucide-react'
import { api } from '../lib/api'
import { fmtNumber, fmtRelative, fmtTime } from '../lib/format'
import { LEVELS } from '../lib/levels'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, Segmented, SkeletonRows, Template, inputClass } from '../components/ui'

type Sort = 'count' | 'recent' | 'new'

export default function Patterns() {
  const [params, setParams] = useSearchParams()
  const service = params.get('service') ?? ''
  const level = params.get('level') ?? ''
  const sort = (params.get('sort') as Sort) || 'count'
  const [q, setQ] = useState('')
  const set = (k: string, v: string) => {
    const next = new URLSearchParams(params)
    if (v) next.set(k, v)
    else next.delete(k)
    setParams(next, { replace: true })
  }

  const services = useQuery({ queryKey: ['services'], queryFn: api.services })
  const patterns = useQuery({
    queryKey: ['patterns', service, level, sort, q],
    queryFn: () => api.patterns({ service, level, sort, q, limit: 200 }),
    placeholderData: keepPreviousData,
  })
  const list = patterns.data ?? []
  const total = list.reduce((s, p) => s + p.count, 0)
  const max = Math.max(1, ...list.map((p) => p.count))

  return (
    <>
      <PageHeader
        title="日志模式"
        description="数字、IP、UUID、路径参数等可变部分被替换为占位符，相同结构的日志归为一类"
      />
      <Card bodyClassName="flex flex-wrap items-center gap-2">
        <div className="relative min-w-52 flex-1">
          <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-zinc-400" />
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="过滤模板或样例…" className={`${inputClass} w-full pl-9`} />
        </div>
        <select value={service} onChange={(e) => set('service', e.target.value)} className={inputClass} aria-label="服务">
          <option value="">全部服务</option>
          {services.data?.map((s) => <option key={s} value={s}>{s || '(未命名)'}</option>)}
        </select>
        <select value={level} onChange={(e) => set('level', e.target.value)} className={inputClass} aria-label="级别">
          <option value="">全部级别</option>
          {LEVELS.map((l) => <option key={l}>{l}</option>)}
        </select>
        <Segmented
          value={sort}
          onChange={(v) => set('sort', v === 'count' ? '' : v)}
          options={[{ value: 'count', label: '按数量' }, { value: 'recent', label: '最近出现' }, { value: 'new', label: '最新发现' }]}
        />
      </Card>

      <Card className="mt-4" flush title={patterns.data ? `${fmtNumber(list.length)} 种模式 · ${fmtNumber(total)} 条日志` : '加载中…'}>
        {patterns.error ? (
          <div className="p-4"><ErrorBox error={patterns.error} /></div>
        ) : patterns.isLoading ? (
          <div className="p-4"><SkeletonRows rows={8} /></div>
        ) : list.length === 0 ? (
          <Empty icon={<Layers className="size-8 text-zinc-300 dark:text-zinc-600" />}>没有匹配的模式</Empty>
        ) : (
          <ul className={patterns.isPlaceholderData ? 'opacity-60' : ''}>
            {list.map((p) => (
              <li key={p.id} className="border-b border-zinc-100 last:border-0 dark:border-zinc-800">
                <Link to={`/logs?pattern_id=${p.id}`} className="group grid gap-x-4 gap-y-1.5 px-4 py-3 hover:bg-zinc-50 md:grid-cols-[1fr_12rem] dark:hover:bg-zinc-800/40">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 text-xs">
                      <LevelBadge level={p.level} />
                      <span className="truncate text-zinc-500">{p.service || '(未命名)'}</span>
                    </div>
                    <Template text={p.template} className="mt-1.5 block text-sm [overflow-wrap:anywhere] group-hover:text-indigo-700 dark:group-hover:text-indigo-300" />
                    <div className="mt-1 truncate font-mono text-xs text-zinc-400" title={p.sample}>{p.sample}</div>
                  </div>
                  <div className="flex flex-col justify-center gap-1.5 md:items-end">
                    <div className="flex w-full items-center gap-2">
                      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
                        <div className="h-full rounded-full bg-indigo-500" style={{ width: `${(p.count / max) * 100}%` }} />
                      </div>
                      <span className="w-16 text-right text-sm font-medium tabular-nums">{fmtNumber(p.count)}</span>
                    </div>
                    <div className="text-xs text-zinc-500 tabular-nums" title={`首次 ${fmtTime(p.first_seen)} · 最近 ${fmtTime(p.last_seen)}`}>
                      {total > 0 && `${((p.count / total) * 100).toFixed(1)}% · `}最近 {fmtRelative(p.last_seen)}
                    </div>
                  </div>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  )
}
