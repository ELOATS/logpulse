import { useState } from 'react'
import { Link } from 'react-router'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { fmtNumber, fmtTime } from '../lib/format'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, inputClass } from '../components/ui'

export default function Patterns() {
  const [service, setService] = useState('')
  const [level, setLevel] = useState('')
  const [sort, setSort] = useState('count')
  const [q, setQ] = useState('')
  const services = useQuery({ queryKey: ['services'], queryFn: api.services })
  const patterns = useQuery({
    queryKey: ['patterns', service, level, sort, q],
    queryFn: () => api.patterns({ service, level, sort, q, limit: 200 }),
    placeholderData: keepPreviousData,
  })
  const max = Math.max(1, ...(patterns.data ?? []).map((p) => p.count))

  return (
    <>
      <PageHeader title="日志模式" description="将可变部分（数字、IP、UUID、路径参数等）替换为占位符后自动聚类，海量日志一眼看清有哪几类" />
      <Card>
        <div className="flex flex-wrap gap-2">
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="过滤模板…" className={`${inputClass} min-w-52 flex-1`} />
          <select value={service} onChange={(e) => setService(e.target.value)} className={inputClass} aria-label="服务">
            <option value="">全部服务</option>
            {services.data?.map((s) => <option key={s} value={s}>{s || '(未命名)'}</option>)}
          </select>
          <select value={level} onChange={(e) => setLevel(e.target.value)} className={inputClass} aria-label="级别">
            <option value="">全部级别</option>
            {['DEBUG', 'INFO', 'WARN', 'ERROR', 'FATAL'].map((l) => <option key={l}>{l}</option>)}
          </select>
          <select value={sort} onChange={(e) => setSort(e.target.value)} className={inputClass} aria-label="排序">
            <option value="count">按数量</option>
            <option value="recent">最近出现</option>
            <option value="new">最新发现</option>
          </select>
        </div>
      </Card>
      <Card className="mt-4">
        {patterns.error ? (
          <ErrorBox error={patterns.error} />
        ) : patterns.data?.length === 0 ? (
          <Empty>暂无模式</Empty>
        ) : (
          <ul className="-my-4 divide-y divide-zinc-100 dark:divide-zinc-800">
            {patterns.data?.map((p) => (
              <li key={p.id} className="py-3">
                <div className="flex flex-wrap items-center gap-2">
                  <LevelBadge level={p.level} />
                  <span className="text-xs text-zinc-500">{p.service || '(未命名)'}</span>
                  <span className="ml-auto text-xs text-zinc-500">
                    首次 {fmtTime(p.first_seen, false)} · 最近 {fmtTime(p.last_seen, false)}
                  </span>
                </div>
                <Link to={`/logs?pattern_id=${p.id}`} className="mt-1.5 block font-mono text-sm break-all hover:text-indigo-600 dark:hover:text-indigo-400">
                  {p.template}
                </Link>
                <div className="mt-1 truncate font-mono text-xs text-zinc-400" title={p.sample}>例：{p.sample}</div>
                <div className="mt-2 flex items-center gap-3">
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
                    <div className="h-full rounded-full bg-indigo-500" style={{ width: `${(p.count / max) * 100}%` }} />
                  </div>
                  <span className="w-20 text-right text-xs tabular-nums">{fmtNumber(p.count)} 条</span>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  )
}
