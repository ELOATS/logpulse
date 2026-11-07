import { useState } from 'react'
import { Link } from 'react-router'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Layers } from 'lucide-react'
import { api } from '../lib/api'
import { fmtNumber, fmtPercent, fmtRelative, fmtTime, svcName } from '../lib/format'
import { LEVELS } from '../lib/levels'
import { logsUrl, useParamPatch } from '../lib/url'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, SearchInput, Segmented, ServiceSelect, SkeletonRows, Template, inputClass } from '../components/ui'

type Sort = 'count' | 'recent' | 'new'

export default function Patterns() {
  const [params, patch] = useParamPatch({ replace: true })
  const set = (k: string, v: string) => patch({ [k]: v })
  const service = params.get('service') ?? ''
  const level = params.get('level') ?? ''
  const sort = (params.get('sort') as Sort) || 'count'
  const [q, setQ] = useState('')

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
        <SearchInput className="min-w-52 flex-1" value={q} onChange={setQ} placeholder="过滤模板或样例…" />
        <ServiceSelect value={service} onChange={(v) => set('service', v)} />
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
                <Link to={logsUrl({ pattern_id: p.id })} className="group grid gap-x-4 gap-y-1.5 px-4 py-3 hover:bg-zinc-50 md:grid-cols-[1fr_12rem] dark:hover:bg-zinc-800/40">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2 text-xs">
                      <LevelBadge level={p.level} />
                      <span className="truncate text-zinc-500">{svcName(p.service)}</span>
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
                      {total > 0 && `${fmtPercent(p.count / total, 1)} · `}最近 {fmtRelative(p.last_seen)}
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
