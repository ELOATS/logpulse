import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router'
import { Area, AreaChart, Bar, CartesianGrid, ComposedChart, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { ArrowRight, CheckCircle2, Sparkles, Upload } from 'lucide-react'
import clsx from 'clsx'
import { api, type Anomaly, type Level, type TimePoint } from '../lib/api'
import { durationMs, fmtNumber, fmtPercent, fmtRelative, fmtShortTime, fmtTime, pickBucket, RANGES, type RangeKey } from '../lib/format'
import { LEVELS, levelColor, severityStripe } from '../lib/levels'
import { useIsDark } from '../lib/theme'
import Sparkline from '../components/Sparkline'
import { Card, Empty, ErrorBox, LevelBadge, PageHeader, Segmented, SeverityBadge, Skeleton, SkeletonRows, StatCard, Template } from '../components/ui'

const kindLabel: Record<Anomaly['kind'], string> = { error_spike: '错误突增', volume_spike: '流量突增', new_pattern: '新错误模式' }

export default function Dashboard() {
  const [params, setParams] = useSearchParams()
  const range = (params.get('range') as RangeKey) || '24h'
  const base = useQuery({ queryKey: ['overview', 'all'], queryFn: () => api.overview(), refetchInterval: 30_000 })
  const last = base.data?.last_log
  const ms = RANGES.find((r) => r.key === range)?.ms ?? 0
  const from = last && ms ? new Date(new Date(last).getTime() - ms).toISOString() : undefined
  const bucket = pickBucket(from ?? base.data?.first_log, last)

  const overview = useQuery({
    queryKey: ['overview', range, from],
    queryFn: () => api.overview({ from }),
    enabled: !!base.data,
    refetchInterval: 30_000,
  })
  const series = useQuery({
    queryKey: ['timeseries', bucket, from],
    queryFn: () => api.timeseries({ bucket, from }),
    enabled: !!base.data,
    refetchInterval: 30_000,
  })
  const anomalies = useQuery({
    queryKey: ['anomalies', 'open', 'severity'],
    queryFn: () => api.anomalies({ acknowledged: false, sort: 'severity', limit: 100 }),
    refetchInterval: 30_000,
  })
  const patterns = useQuery({ queryKey: ['patterns', 'top-errors'], queryFn: () => api.patterns({ level: 'ERROR', limit: 6 }) })

  if (base.error) return <ErrorBox error={base.error} />
  if (base.data && base.data.total_logs === 0) {
    return (
      <>
        <PageHeader title="总览" />
        <Card>
          <Empty icon={<Upload className="size-8 text-zinc-300 dark:text-zinc-600" />}>
            还没有日志数据
            <Link to="/ingest" className="text-indigo-600 hover:underline dark:text-indigo-400">导入日志开始分析 →</Link>
          </Empty>
        </Card>
      </>
    )
  }

  const o = overview.data
  const pts = series.data ?? []
  const open = anomalies.data ?? []
  const bySeverity = { high: 0, medium: 0, low: 0 }
  open.forEach((a) => bySeverity[a.severity]++)
  const inRange = open.filter((a) => !from || a.window_end >= from)

  return (
    <>
      <PageHeader
        title="总览"
        description={o?.first_log && o.last_log ? `${fmtTime(o.first_log, false)} ~ ${fmtTime(o.last_log, false)}` : <Skeleton className="h-4 w-64" />}
        actions={
          <Segmented
            value={range}
            onChange={(v) => setParams(v === '24h' ? {} : { range: v }, { replace: true })}
            options={RANGES.map((r) => ({ value: r.key, label: r.label }))}
            size="sm"
          />
        }
      />

      <Attention anomalies={open} loading={anomalies.isLoading} />

      <div className="mt-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <StatCard
          label="日志量" to="/logs"
          value={o ? fmtNumber(o.total_logs) : <Skeleton className="h-8 w-20" />}
          hint={o && `${o.services} 个服务 · ${base.data?.patterns ?? 0} 种模式`}
          chart={<Sparkline data={pts.map((p) => p.total)} color="#6366f1" />}
        />
        <StatCard
          label="错误" to="/logs?level=ERROR,FATAL" tone={o?.error_logs ? 'danger' : undefined}
          value={o ? fmtNumber(o.error_logs) : <Skeleton className="h-8 w-16" />}
          hint={o && `错误率 ${fmtPercent(o.error_rate)}`}
          chart={<Sparkline data={pts.map((p) => p.errors)} color={levelColor.ERROR} />}
        />
        <StatCard
          label="告警" to="/logs?level=WARN"
          value={o ? fmtNumber(o.levels.WARN ?? 0) : <Skeleton className="h-8 w-16" />}
          hint={o && o.total_logs > 0 && `占比 ${fmtPercent((o.levels.WARN ?? 0) / o.total_logs)}`}
          chart={<Sparkline data={pts.map((p) => p.warns)} color={levelColor.WARN} />}
        />
        <StatCard
          label="待处理异常" to="/anomalies"
          tone={bySeverity.high ? 'danger' : open.length ? 'warn' : undefined}
          value={anomalies.data ? open.length : <Skeleton className="h-8 w-10" />}
          hint={open.length ? `高危 ${bySeverity.high} · 中危 ${bySeverity.medium} · 低危 ${bySeverity.low}` : '当前没有待处理异常'}
        />
      </div>

      <Card
        className="mt-4"
        title="日志趋势"
        actions={
          <span className="flex items-center gap-3 text-xs text-zinc-500">
            <span>每 {bucket}</span>
            {inRange.length > 0 && (
              <span className="flex items-center gap-1"><span className="inline-block size-2.5 rounded-sm bg-red-500/20 ring-1 ring-red-500/40" />异常窗口</span>
            )}
          </span>
        }
      >
        {series.isLoading ? <Skeleton className="h-72" /> : (
          <TrendCharts points={pts} anomalies={inRange} bucketMs={durationMs(bucket)} first={o?.first_log ?? undefined} last={o?.last_log ?? undefined} />
        )}
      </Card>

      <div className="mt-4 grid items-start gap-4 lg:grid-cols-3">
        <Card title="服务健康度" className="lg:col-span-2" flush>
          {o ? <ServiceTable services={o.top_services} /> : <div className="p-4"><SkeletonRows /></div>}
        </Card>
        <div className="flex flex-col gap-4">
          <Card title="级别分布">{o ? <LevelBar levels={o.levels} total={o.total_logs} /> : <Skeleton className="h-16" />}</Card>
          <Card
            title="高频错误模式"
            className="flex-1"
            actions={<Link to="/patterns?level=ERROR" className="text-xs text-indigo-600 hover:underline dark:text-indigo-400">全部</Link>}
          >
            {patterns.isLoading ? (
              <SkeletonRows rows={4} />
            ) : patterns.data?.length ? (
              <ul className="-my-1.5 divide-y divide-zinc-100 dark:divide-zinc-800">
                {patterns.data.map((p) => (
                  <li key={p.id}>
                    <Link to={`/logs?pattern_id=${p.id}`} className="group flex items-baseline gap-3 py-2">
                      <span className="min-w-0 flex-1">
                        <Template text={p.template} className="line-clamp-2 text-xs [overflow-wrap:anywhere] group-hover:text-indigo-600 dark:group-hover:text-indigo-400" />
                        <span className="text-[11px] text-zinc-500">{p.service}</span>
                      </span>
                      <span className="shrink-0 text-xs font-medium tabular-nums">{fmtNumber(p.count)}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            ) : (
              <Empty>暂无错误模式</Empty>
            )}
          </Card>
        </div>
      </div>
    </>
  )
}

function Attention({ anomalies, loading }: { anomalies: Anomaly[]; loading: boolean }) {
  if (loading) return <Skeleton className="h-20 rounded-xl" />
  if (!anomalies.length) {
    return (
      <div className="flex items-center gap-2 rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300">
        <CheckCircle2 className="size-4" />
        一切正常，当前没有待处理的异常
      </div>
    )
  }
  const top = anomalies.slice(0, 3)
  return (
    <section className="rounded-xl border border-zinc-200 bg-white shadow-xs dark:border-zinc-800 dark:bg-zinc-900">
      <header className="flex items-center justify-between px-4 pt-3 pb-1">
        <h2 className="text-sm font-semibold">需要关注</h2>
        <Link to="/anomalies" className="inline-flex items-center gap-1 text-xs text-indigo-600 hover:underline dark:text-indigo-400">
          全部 {anomalies.length} 个异常 <ArrowRight className="size-3" />
        </Link>
      </header>
      <ul className="grid gap-2 p-3 pt-2 md:grid-cols-3">
        {top.map((a, i) => (
          <li key={a.id} className={i === 2 ? 'hidden md:block' : undefined}>
            <Link
              to={`/anomalies#a-${a.id}`}
              className={clsx('block h-full rounded-lg border border-l-4 border-y-zinc-200 border-r-zinc-200 p-3 transition-colors hover:bg-zinc-50 dark:border-y-zinc-800 dark:border-r-zinc-800 dark:hover:bg-zinc-800/50', severityStripe[a.severity])}
            >
              <div className="flex flex-wrap items-center gap-1.5 text-xs">
                <SeverityBadge severity={a.severity} />
                <span className="font-medium">{kindLabel[a.kind]}</span>
                <span className="text-zinc-500">· {a.service || '(未命名)'}</span>
                <span className="ml-auto text-zinc-400">{fmtRelative(a.window_start)}</span>
              </div>
              <p className="mt-2 line-clamp-2 text-sm">{a.ai_insight?.summary ?? a.description}</p>
              {a.ai_insight && (
                <span className="mt-1.5 inline-flex items-center gap-1 text-[11px] text-violet-600 dark:text-violet-400">
                  <Sparkles className="size-3" /> AI 分析
                </span>
              )}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}

function TrendCharts({ points, anomalies, bucketMs, first, last }: {
  points: TimePoint[]; anomalies: Anomaly[]; bucketMs: number; first?: string; last?: string
}) {
  const dark = useIsDark()
  const grid = dark ? '#27272a' : '#f4f4f5'
  const axis = { fontSize: 11, fill: dark ? '#71717a' : '#a1a1aa' }
  const data = useMemo(() => {
    // 首尾时间桶通常只覆盖了一部分时间，数量偏低会被误读为"骤降"，桶足够多时不显示
    let rows = points
    if (rows.length > 6) {
      const f = first ? new Date(first).getTime() : -Infinity
      const l = last ? new Date(last).getTime() : Infinity
      rows = rows.filter((p) => {
        const s = new Date(p.bucket).getTime()
        return s >= f && s + bucketMs <= l + 1000
      })
    }
    // 点画在时间桶中点，使柱子覆盖它所代表的时间段，与异常窗口对齐
    return rows.map((p) => ({ ...p, t: new Date(p.bucket).getTime() + bucketMs / 2 }))
  }, [points, bucketMs, first, last])
  if (!data.length) return <Empty>所选时间范围内没有日志</Empty>
  const domain: [number, number] = [data[0].t - bucketMs / 2, data[data.length - 1].t + bucketMs / 2]
  const ticks = data.map((d) => d.t - bucketMs / 2)
  const areas = anomalies.map((a) => {
    const x1 = Math.max(new Date(a.window_start).getTime(), domain[0])
    const x2 = Math.min(new Date(a.window_end).getTime(), domain[1])
    return x2 > x1 ? <ReferenceArea key={a.id} x1={x1} x2={x2} fill="#ef4444" fillOpacity={dark ? 0.18 : 0.1} stroke="none" ifOverflow="hidden" /> : null
  })
  const xAxis = (show: boolean) => (
    <XAxis dataKey="t" type="number" scale="time" domain={domain} ticks={ticks} interval="preserveStartEnd" tickFormatter={(t) => fmtShortTime(new Date(t).toISOString())}
      tick={show ? axis : false} height={show ? 24 : 4} axisLine={false} tickLine={false} minTickGap={40} />
  )
  const tooltip = <Tooltip content={<TrendTooltip />} cursor={{ stroke: dark ? '#52525b' : '#d4d4d8' }} />
  return (
    <div>
      <div className="mb-1 text-xs text-zinc-500">日志量</div>
      <div className="h-40">
        <ResponsiveContainer>
          <AreaChart data={data} syncId="trend" margin={{ top: 4, right: 8, left: -12, bottom: 0 }}>
            <CartesianGrid vertical={false} stroke={grid} />
            {areas}
            {xAxis(false)}
            <YAxis tick={axis} axisLine={false} tickLine={false} allowDecimals={false} width={44} />
            {tooltip}
            <Area type="monotone" dataKey="total" name="日志量" stroke="#6366f1" strokeWidth={1.5} fill="#6366f1" fillOpacity={0.12} isAnimationActive={false} />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="mt-3 mb-1 flex items-center gap-3 text-xs text-zinc-500">
        错误与告警
        <span className="flex items-center gap-1"><span className="inline-block size-2 rounded-sm" style={{ background: levelColor.ERROR }} />错误</span>
        <span className="flex items-center gap-1"><span className="inline-block h-0.5 w-3 rounded" style={{ background: levelColor.WARN }} />告警</span>
      </div>
      <div className="h-32">
        <ResponsiveContainer>
          <ComposedChart data={data} syncId="trend" margin={{ top: 4, right: 8, left: -12, bottom: 0 }}>
            <CartesianGrid vertical={false} stroke={grid} />
            {areas}
            {xAxis(true)}
            <YAxis tick={axis} axisLine={false} tickLine={false} allowDecimals={false} width={44} />
            {tooltip}
            <Bar dataKey="errors" name="错误" fill={levelColor.ERROR} radius={[2, 2, 0, 0]} maxBarSize={10} isAnimationActive={false} />
            <Line type="monotone" dataKey="warns" name="告警" stroke={levelColor.WARN} strokeWidth={1.5} dot={false} isAnimationActive={false} />
          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

function TrendTooltip({ active, payload }: { active?: boolean; payload?: { payload: TimePoint & { t: number } }[] }) {
  if (!active || !payload?.length) return null
  const p = payload[0].payload
  return (
    <div className="rounded-lg border border-zinc-200 bg-white px-3 py-2 text-xs shadow-md dark:border-zinc-700 dark:bg-zinc-900">
      <div className="mb-1 font-medium">{fmtTime(p.bucket, false)}</div>
      <div className="grid grid-cols-[auto_auto] gap-x-4 gap-y-0.5 tabular-nums">
        <span className="text-zinc-500">日志量</span><span className="text-right">{fmtNumber(p.total)}</span>
        <span className="text-zinc-500">错误</span><span className="text-right text-red-600 dark:text-red-400">{fmtNumber(p.errors)}</span>
        <span className="text-zinc-500">告警</span><span className="text-right text-amber-600 dark:text-amber-400">{fmtNumber(p.warns)}</span>
      </div>
    </div>
  )
}

function ServiceTable({ services }: { services: { name: string; count: number; errors: number }[] }) {
  if (!services.length) return <Empty>暂无数据</Empty>
  const max = Math.max(...services.map((s) => s.count))
  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="border-b border-zinc-100 text-left text-xs text-zinc-500 dark:border-zinc-800">
          <th className="px-4 py-2 font-medium">服务</th>
          <th className="px-2 py-2 font-medium">日志量</th>
          <th className="px-2 py-2 text-right font-medium">错误</th>
          <th className="px-4 py-2 text-right font-medium">错误率</th>
        </tr>
      </thead>
      <tbody>
        {services.map((s) => {
          const rate = s.count ? s.errors / s.count : 0
          return (
            <tr key={s.name} className="border-b border-zinc-50 last:border-0 hover:bg-zinc-50 dark:border-zinc-800/60 dark:hover:bg-zinc-800/40">
              <td className="px-4 py-2">
                <Link to={`/logs?service=${encodeURIComponent(s.name)}`} className="font-medium hover:text-indigo-600 dark:hover:text-indigo-400">{s.name || '(未命名)'}</Link>
              </td>
              <td className="w-1/3 px-2 py-2">
                <div className="flex items-center gap-2">
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
                    <div className="h-full rounded-full bg-indigo-500" style={{ width: `${(s.count / max) * 100}%` }} />
                  </div>
                  <span className="w-14 text-right text-xs tabular-nums text-zinc-600 dark:text-zinc-400">{fmtNumber(s.count)}</span>
                </div>
              </td>
              <td className="px-2 py-2 text-right tabular-nums">
                {s.errors ? (
                  <Link to={`/logs?service=${encodeURIComponent(s.name)}&level=ERROR,FATAL`} className="text-red-600 hover:underline dark:text-red-400">{fmtNumber(s.errors)}</Link>
                ) : <span className="text-zinc-400">0</span>}
              </td>
              <td className="px-4 py-2 text-right">
                <span className={clsx('rounded-md px-1.5 py-0.5 text-xs font-medium tabular-nums',
                  rate >= 0.05 ? 'bg-red-50 text-red-700 dark:bg-red-500/15 dark:text-red-300'
                    : rate >= 0.01 ? 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'
                      : 'text-zinc-500')}>
                  {fmtPercent(rate)}
                </span>
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

function LevelBar({ levels, total }: { levels: Partial<Record<Level, number>>; total: number }) {
  const present = LEVELS.filter((l) => (levels[l] ?? 0) > 0)
  if (!total) return <Empty>暂无数据</Empty>
  return (
    <div>
      <div className="flex h-2.5 overflow-hidden rounded-full bg-zinc-100 dark:bg-zinc-800">
        {present.map((l) => (
          <div key={l} style={{ width: `${((levels[l] ?? 0) / total) * 100}%`, background: levelColor[l] }} title={`${l} ${levels[l]}`} />
        ))}
      </div>
      <ul className="mt-3 space-y-1.5">
        {present.map((l) => (
          <li key={l} className="flex items-center gap-2 text-sm">
            <LevelBadge level={l} />
            <Link to={`/logs?level=${l}`} className="tabular-nums hover:text-indigo-600 dark:hover:text-indigo-400">{fmtNumber(levels[l] ?? 0)}</Link>
            <span className="ml-auto text-xs tabular-nums text-zinc-500">{fmtPercent((levels[l] ?? 0) / total)}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
