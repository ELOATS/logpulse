import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { api, type Level } from '../lib/api'
import { fmtNumber, fmtPercent, fmtShortTime, fmtTime, pickBucket } from '../lib/format'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, SeverityBadge, StatCard } from '../components/ui'

const LEVELS: Level[] = ['FATAL', 'ERROR', 'WARN', 'INFO', 'DEBUG']
const tooltipStyle = { fontSize: 12, borderRadius: 6 }

export default function Dashboard() {
  const overview = useQuery({ queryKey: ['overview'], queryFn: () => api.overview(), refetchInterval: 30_000 })
  const o = overview.data
  const bucket = pickBucket(o?.first_log, o?.last_log)
  const series = useQuery({
    queryKey: ['timeseries', bucket],
    queryFn: () => api.timeseries({ bucket }),
    enabled: !!o,
    refetchInterval: 30_000,
  })
  const anomalies = useQuery({ queryKey: ['anomalies', 'open'], queryFn: () => api.anomalies({ acknowledged: false, limit: 5 }) })
  const patterns = useQuery({ queryKey: ['patterns', 'errors'], queryFn: () => api.patterns({ level: 'ERROR', limit: 5 }) })

  if (overview.error) return <ErrorBox error={overview.error} />
  if (o && o.total_logs === 0) {
    return (
      <>
        <PageHeader title="总览" />
        <Card>
          <Empty>
            还没有日志数据，<Link to="/ingest" className="text-indigo-600 hover:underline dark:text-indigo-400">去导入日志</Link>
          </Empty>
        </Card>
      </>
    )
  }

  const chartData = (series.data ?? []).map((p) => ({ ...p, label: fmtShortTime(p.bucket), info: p.total - p.errors - p.warns }))

  return (
    <>
      <PageHeader
        title="总览"
        description={o?.first_log && o.last_log ? `数据范围：${fmtTime(o.first_log)} ~ ${fmtTime(o.last_log)}` : undefined}
      />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <StatCard label="日志总数" value={o ? fmtNumber(o.total_logs) : '—'} hint={o && `${o.services} 个服务`} />
        <StatCard label="错误日志" value={o ? fmtNumber(o.error_logs) : '—'} tone="danger" />
        <StatCard label="错误率" value={o ? fmtPercent(o.error_rate) : '—'} />
        <StatCard label="日志模式" value={o ? fmtNumber(o.patterns) : '—'} hint="自动聚类得出" />
        <StatCard label="待处理异常" value={o ? o.open_anomalies : '—'} tone={o?.open_anomalies ? 'danger' : undefined} />
      </div>

      <Card title={`日志量趋势（每 ${bucket}）`} className="mt-4">
        <div className="h-64">
          <ResponsiveContainer>
            <AreaChart data={chartData} margin={{ left: -10, right: 8 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="currentColor" strokeOpacity={0.1} />
              <XAxis dataKey="label" tick={{ fontSize: 11 }} minTickGap={24} />
              <YAxis tick={{ fontSize: 11 }} allowDecimals={false} />
              <Tooltip contentStyle={tooltipStyle} labelFormatter={(_, p) => (p?.[0] ? fmtTime(p[0].payload.bucket) : '')} />
              <Legend wrapperStyle={{ fontSize: 12 }} />
              <Area type="monotone" dataKey="info" name="其他" stackId="1" stroke="#0ea5e9" fill="#0ea5e9" fillOpacity={0.15} />
              <Area type="monotone" dataKey="warns" name="告警" stackId="1" stroke="#f59e0b" fill="#f59e0b" fillOpacity={0.3} />
              <Area type="monotone" dataKey="errors" name="错误" stackId="1" stroke="#ef4444" fill="#ef4444" fillOpacity={0.4} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </Card>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card title="服务分布（Top 10）">
          <div className="h-64">
            <ResponsiveContainer>
              <BarChart data={o?.top_services ?? []} layout="vertical" margin={{ left: 20, right: 8 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="currentColor" strokeOpacity={0.1} />
                <XAxis type="number" tick={{ fontSize: 11 }} />
                <YAxis type="category" dataKey="name" tick={{ fontSize: 11 }} width={110} />
                <Tooltip contentStyle={tooltipStyle} />
                <Bar dataKey="count" name="总数" fill="#6366f1" radius={[0, 3, 3, 0]} />
                <Bar dataKey="errors" name="错误" fill="#ef4444" radius={[0, 3, 3, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </Card>
        <Card title="级别分布">
          <div className="space-y-3">
            {LEVELS.map((l) => {
              const n = o?.levels[l] ?? 0
              const pct = o?.total_logs ? (n / o.total_logs) * 100 : 0
              return (
                <div key={l} className="flex items-center gap-3 text-sm">
                  <div className="w-14"><LevelBadge level={l} /></div>
                  <div className="h-2 flex-1 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
                    <div className="h-full rounded-full bg-indigo-500" style={{ width: `${pct}%` }} />
                  </div>
                  <div className="w-24 text-right tabular-nums text-zinc-600 dark:text-zinc-400">{fmtNumber(n)}</div>
                </div>
              )
            })}
          </div>
        </Card>
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-2">
        <Card title="最新异常" actions={<Link to="/anomalies" className="text-xs text-indigo-600 hover:underline dark:text-indigo-400">全部</Link>}>
          {anomalies.data?.length ? (
            <ul className="divide-y divide-zinc-100 dark:divide-zinc-800">
              {anomalies.data.map((a) => (
                <li key={a.id} className="flex items-start gap-2 py-2.5 text-sm first:pt-0 last:pb-0">
                  <SeverityBadge severity={a.severity} />
                  <span className="min-w-0 flex-1">
                    {a.ai_insight?.summary ?? a.description}
                    {a.ai_insight && <span className="ml-1 text-xs text-violet-600 dark:text-violet-400">· AI</span>}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <Empty>暂无待处理异常</Empty>
          )}
        </Card>
        <Card title="高频错误模式" actions={<Link to="/patterns" className="text-xs text-indigo-600 hover:underline dark:text-indigo-400">全部</Link>}>
          {patterns.data?.length ? (
            <ul className="divide-y divide-zinc-100 dark:divide-zinc-800">
              {patterns.data.map((p) => (
                <li key={p.id} className="flex items-center gap-3 py-2.5 text-sm first:pt-0 last:pb-0">
                  <Link to={`/logs?pattern_id=${p.id}`} className="min-w-0 flex-1 truncate font-mono text-xs hover:text-indigo-600 dark:hover:text-indigo-400" title={p.template}>
                    {p.template}
                  </Link>
                  <span className="shrink-0 text-xs text-zinc-500">{p.service}</span>
                  <span className="w-14 shrink-0 text-right tabular-nums">{fmtNumber(p.count)}</span>
                </li>
              ))}
            </ul>
          ) : (
            <Empty>暂无错误模式</Empty>
          )}
        </Card>
      </div>
    </>
  )
}
