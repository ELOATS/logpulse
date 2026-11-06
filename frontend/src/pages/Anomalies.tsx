import { useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, AlertOctagon, Check, PartyPopper, RefreshCw, RotateCcw, ScrollText, Sparkle, TrendingUp } from 'lucide-react'
import clsx from 'clsx'
import { api, type Anomaly } from '../lib/api'
import { fmtNumber, fmtRelative, fmtTime } from '../lib/format'
import { severityStripe } from '../lib/levels'
import InsightPanel from '../components/InsightPanel'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, Segmented, SeverityBadge, SkeletonRows, Template, buttonClass, ghostButtonClass } from '../components/ui'

const kinds: Record<Anomaly['kind'], { label: string; icon: typeof Activity }> = {
  error_spike: { label: '错误突增', icon: AlertOctagon },
  volume_spike: { label: '流量突增', icon: TrendingUp },
  new_pattern: { label: '新错误模式', icon: Sparkle },
}
type Status = 'open' | 'acked' | 'all'

export default function Anomalies() {
  const qc = useQueryClient()
  const location = useLocation()
  const [status, setStatus] = useState<Status>('open')
  const [kind, setKind] = useState<'' | Anomaly['kind']>('')
  const anomalies = useQuery({
    queryKey: ['anomalies', status],
    queryFn: () => api.anomalies({
      acknowledged: status === 'all' ? undefined : status === 'acked',
      sort: status === 'open' ? 'severity' : undefined, // 待处理按严重程度排，其余按时间
      limit: 200,
    }),
  })
  const features = useQuery({ queryKey: ['features'], queryFn: api.features, staleTime: Infinity })
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['anomalies'] })
    qc.invalidateQueries({ queryKey: ['overview'] })
  }
  const analyze = useMutation({ mutationFn: api.analyze, onSuccess: invalidate })

  const all = anomalies.data ?? []
  const list = kind ? all.filter((a) => a.kind === kind) : all
  const count = (k: Anomaly['kind']) => all.filter((a) => a.kind === k).length

  // 从总览跳转过来时滚动到对应异常并高亮
  useEffect(() => {
    if (!location.hash || !anomalies.data) return
    const el = document.getElementById(location.hash.slice(1))
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' })
      el.classList.add('flash')
      setTimeout(() => el.classList.remove('flash'), 1700)
    }
  }, [location.hash, anomalies.data])

  return (
    <>
      <PageHeader
        title="异常检测"
        description="基于滑动基线与鲁棒 z-score 检测错误突增、流量突增，并识别首次出现的错误模式"
        actions={
          <button className={buttonClass} onClick={() => analyze.mutate()} disabled={analyze.isPending}>
            <RefreshCw className={clsx('size-4', analyze.isPending && 'animate-spin')} />
            立即分析
          </button>
        }
      />
      {analyze.data && (
        <div className="mb-4 rounded-lg bg-emerald-50 px-4 py-2 text-sm text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300">
          分析完成：检查 {analyze.data.services} 个服务，发现 {analyze.data.detected} 个异常（新增 {analyze.data.new_anomalies} 个），耗时 {analyze.data.duration_ms} ms
        </div>
      )}
      {analyze.error && <div className="mb-4"><ErrorBox error={analyze.error} /></div>}

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Segmented<Status> value={status} onChange={setStatus}
          options={[{ value: 'open', label: '待处理' }, { value: 'acked', label: '已确认' }, { value: 'all', label: '全部' }]} />
        <Segmented<'' | Anomaly['kind']> value={kind} onChange={setKind}
          options={[{ value: '', label: `全部类型 ${all.length}` }, ...(Object.keys(kinds) as Anomaly['kind'][]).map((k) => ({ value: k, label: `${kinds[k].label} ${count(k)}` }))]} />
      </div>

      {anomalies.error ? (
        <ErrorBox error={anomalies.error} />
      ) : anomalies.isLoading ? (
        <Card><SkeletonRows rows={6} /></Card>
      ) : list.length === 0 ? (
        <Card>
          <Empty icon={<PartyPopper className="size-8 text-zinc-300 dark:text-zinc-600" />}>
            {status === 'open' ? '没有待处理的异常' : '没有异常'}
          </Empty>
        </Card>
      ) : (
        <div className="space-y-3">
          {list.map((a) => <AnomalyCard key={a.id} a={a} aiEnabled={!!features.data?.ai_insight} onChanged={invalidate} />)}
        </div>
      )}
    </>
  )
}

function AnomalyCard({ a, aiEnabled, onChanged }: { a: Anomaly; aiEnabled: boolean; onChanged: () => void }) {
  const ack = useMutation({ mutationFn: (value: boolean) => api.ackAnomaly(a.id, value), onSuccess: onChanged })
  const Icon = kinds[a.kind].icon
  const logsLink = a.kind === 'new_pattern' && a.pattern_id
    ? `/logs?pattern_id=${a.pattern_id}`
    : `/logs?${new URLSearchParams({ service: a.service, from: a.window_start, to: a.window_end, ...(a.kind === 'error_spike' ? { level: 'ERROR,FATAL' } : {}) })}`
  const showAI = aiEnabled || !!a.ai_insight

  return (
    <article id={`a-${a.id}`} className={clsx(
      'scroll-mt-4 rounded-xl border border-l-4 border-y-zinc-200 border-r-zinc-200 bg-white shadow-xs dark:border-y-zinc-800 dark:border-r-zinc-800 dark:bg-zinc-900',
      severityStripe[a.severity], a.acknowledged && 'opacity-60',
    )}>
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-zinc-100 px-4 py-3 dark:border-zinc-800">
        <Icon className="size-4 text-zinc-500" />
        <span className="text-sm font-semibold">{kinds[a.kind].label}</span>
        <SeverityBadge severity={a.severity} />
        <span className="text-sm text-zinc-600 dark:text-zinc-400">{a.service || '(未命名)'}</span>
        <span className="text-xs text-zinc-500" title={`${fmtTime(a.window_start)} ~ ${fmtTime(a.window_end)}`}>
          {fmtTime(a.window_start, false)} ~ {fmtTime(a.window_end, false).slice(11)} · {fmtRelative(a.window_start)}
        </span>
        <div className="ml-auto flex gap-2">
          <Link to={logsLink} className={clsx(ghostButtonClass, 'px-2.5 py-1 text-xs')}><ScrollText className="size-3.5" />查看日志</Link>
          <button className={clsx(ghostButtonClass, 'px-2.5 py-1 text-xs')} onClick={() => ack.mutate(!a.acknowledged)} disabled={ack.isPending}>
            {a.acknowledged ? <><RotateCcw className="size-3.5" />重新打开</> : <><Check className="size-3.5" />确认</>}
          </button>
        </div>
      </header>

      <div className={clsx('grid gap-4 p-4', showAI && 'lg:grid-cols-2')}>
        <div className="min-w-0 space-y-3">
          <p className="text-sm">{a.description}</p>
          {a.kind !== 'new_pattern' && (
            <dl className="grid grid-cols-3 gap-2">
              <Metric label="观测" value={fmtNumber(a.observed)} strong />
              <Metric label="基线" value={fmtNumber(Math.round(a.expected))} />
              <Metric label="峰值 z" value={a.score.toFixed(1)} />
            </dl>
          )}
          {!!a.details.top_patterns?.length && (
            <div>
              <div className="mb-1.5 text-xs font-semibold text-zinc-500">窗口内占比最高的日志模式</div>
              <ul className="space-y-1">
                {a.details.top_patterns.map((p) => (
                  <li key={p.pattern_id}>
                    <Link to={`/logs?pattern_id=${p.pattern_id}`} className="group flex items-center gap-2 rounded-md bg-zinc-50 px-2 py-1.5 text-xs hover:bg-zinc-100 dark:bg-zinc-800/50 dark:hover:bg-zinc-800">
                      <LevelBadge level={p.level} />
                      <Template text={p.template} className="min-w-0 flex-1 truncate group-hover:text-indigo-600 dark:group-hover:text-indigo-400" />
                      <span className="tabular-nums text-zinc-500">{fmtNumber(p.count)}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          )}
          {a.kind === 'new_pattern' && a.details.template && (
            <div className="space-y-1.5 text-xs">
              <div className="font-semibold text-zinc-500">新模式</div>
              <Template text={a.details.template} className="block rounded-md bg-zinc-50 px-2 py-1.5 [overflow-wrap:anywhere] dark:bg-zinc-800/50" />
              {a.details.sample && <div className="font-mono break-all text-zinc-500">例：{a.details.sample}</div>}
              <div className="text-zinc-500">已出现 {fmtNumber(a.details.count ?? 0)} 次</div>
            </div>
          )}
        </div>
        {showAI && <InsightPanel anomaly={a} />}
      </div>
    </article>
  )
}

function Metric({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className="rounded-lg bg-zinc-50 px-3 py-2 dark:bg-zinc-800/50">
      <dt className="text-[11px] text-zinc-500">{label}</dt>
      <dd className={clsx('text-base tabular-nums', strong && 'font-semibold text-red-600 dark:text-red-400')}>{value}</dd>
    </div>
  )
}
