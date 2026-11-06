import { useState } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, RefreshCw, RotateCcw } from 'lucide-react'
import clsx from 'clsx'
import { api, type Anomaly } from '../lib/api'
import { fmtNumber, fmtTime } from '../lib/format'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, SeverityBadge, buttonClass, ghostButtonClass, inputClass } from '../components/ui'

const kindLabel: Record<Anomaly['kind'], string> = {
  error_spike: '错误突增',
  volume_spike: '流量突增',
  new_pattern: '新错误模式',
}

export default function Anomalies() {
  const qc = useQueryClient()
  const [status, setStatus] = useState<'open' | 'acked' | 'all'>('open')
  const [kind, setKind] = useState('')
  const anomalies = useQuery({
    queryKey: ['anomalies', status, kind],
    queryFn: () => api.anomalies({ kind, acknowledged: status === 'all' ? undefined : status === 'acked', limit: 200 }),
  })
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['anomalies'] })
    qc.invalidateQueries({ queryKey: ['overview'] })
  }
  const analyze = useMutation({ mutationFn: api.analyze, onSuccess: invalidate })
  const ack = useMutation({ mutationFn: ({ id, value }: { id: number; value: boolean }) => api.ackAnomaly(id, value), onSuccess: invalidate })

  return (
    <>
      <PageHeader
        title="异常检测"
        description="基于滑动基线与鲁棒 z-score（中位数/MAD）检测错误突增、流量突增，并识别首次出现的错误模式"
        actions={
          <button className={buttonClass} onClick={() => analyze.mutate()} disabled={analyze.isPending}>
            <RefreshCw className={clsx('size-4', analyze.isPending && 'animate-spin')} />
            立即分析
          </button>
        }
      />
      {analyze.data && (
        <div className="mb-4 rounded-md bg-emerald-50 px-4 py-2 text-sm text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300">
          分析完成：检查 {analyze.data.services} 个服务，发现 {analyze.data.detected} 个异常（新增 {analyze.data.new_anomalies} 个），耗时 {analyze.data.duration_ms} ms
        </div>
      )}
      {analyze.error && <div className="mb-4"><ErrorBox error={analyze.error} /></div>}

      <div className="mb-4 flex flex-wrap gap-2">
        <div className="inline-flex rounded-md border border-zinc-300 p-0.5 text-sm dark:border-zinc-700">
          {(['open', 'acked', 'all'] as const).map((s) => (
            <button
              key={s}
              onClick={() => setStatus(s)}
              className={clsx('rounded px-3 py-1', status === s ? 'bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900' : 'text-zinc-600 dark:text-zinc-400')}
            >
              {{ open: '待处理', acked: '已确认', all: '全部' }[s]}
            </button>
          ))}
        </div>
        <select value={kind} onChange={(e) => setKind(e.target.value)} className={inputClass} aria-label="类型">
          <option value="">全部类型</option>
          {Object.entries(kindLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
        </select>
      </div>

      {anomalies.error ? (
        <ErrorBox error={anomalies.error} />
      ) : anomalies.data?.length === 0 ? (
        <Card><Empty>没有异常 🎉</Empty></Card>
      ) : (
        <div className="space-y-3">
          {anomalies.data?.map((a) => (
            <Card key={a.id} className={clsx(a.acknowledged && 'opacity-60')}>
              <div className="flex flex-wrap items-center gap-2">
                <SeverityBadge severity={a.severity} />
                <span className="text-sm font-medium">{kindLabel[a.kind]}</span>
                <span className="text-xs text-zinc-500">{a.service || '(未命名)'}</span>
                <span className="text-xs text-zinc-500">
                  {fmtTime(a.window_start, false)} ~ {fmtTime(a.window_end, false)}
                </span>
                <button
                  className={clsx(ghostButtonClass, 'ml-auto py-1 text-xs')}
                  onClick={() => ack.mutate({ id: a.id, value: !a.acknowledged })}
                  disabled={ack.isPending}
                >
                  {a.acknowledged ? <><RotateCcw className="size-3.5" />重新打开</> : <><Check className="size-3.5" />确认</>}
                </button>
              </div>
              <p className="mt-2 text-sm">{a.description}</p>
              {a.kind !== 'new_pattern' && (
                <div className="mt-2 flex gap-4 text-xs text-zinc-500">
                  <span>观测 <b className="text-zinc-800 tabular-nums dark:text-zinc-200">{fmtNumber(a.observed)}</b></span>
                  <span>基线 <b className="text-zinc-800 tabular-nums dark:text-zinc-200">{fmtNumber(Math.round(a.expected))}</b></span>
                  <span>峰值 z <b className="text-zinc-800 tabular-nums dark:text-zinc-200">{a.score.toFixed(1)}</b></span>
                </div>
              )}
              {!!a.details.top_patterns?.length && (
                <div className="mt-3 rounded-md bg-zinc-50 p-3 dark:bg-zinc-800/50">
                  <div className="mb-2 text-xs font-medium text-zinc-500">根因线索：窗口内占比最高的日志模式</div>
                  <ul className="space-y-1.5">
                    {a.details.top_patterns.map((p) => (
                      <li key={p.pattern_id} className="flex items-center gap-2 text-xs">
                        <LevelBadge level={p.level} />
                        <Link
                          to={`/logs?pattern_id=${p.pattern_id}`}
                          className="min-w-0 flex-1 truncate font-mono hover:text-indigo-600 dark:hover:text-indigo-400"
                          title={p.template}
                        >
                          {p.template}
                        </Link>
                        <span className="tabular-nums text-zinc-500">{fmtNumber(p.count)}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {a.kind === 'new_pattern' && a.details.sample && (
                <div className="mt-3 rounded-md bg-zinc-50 p-3 font-mono text-xs break-all dark:bg-zinc-800/50">
                  例：{a.details.sample}
                  {a.pattern_id && (
                    <Link to={`/logs?pattern_id=${a.pattern_id}`} className="ml-2 font-sans text-indigo-600 hover:underline dark:text-indigo-400">
                      查看全部 {fmtNumber(a.details.count ?? 0)} 条
                    </Link>
                  )}
                </div>
              )}
            </Card>
          ))}
        </div>
      )}
    </>
  )
}
