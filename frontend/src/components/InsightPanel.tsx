import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, Sparkles } from 'lucide-react'
import clsx from 'clsx'
import { api, type Anomaly } from '../lib/api'
import { fmtRelative } from '../lib/format'
import { Skeleton, ghostButtonClass } from './ui'

const confidence = {
  low: { label: '置信度低', cls: 'bg-zinc-100 text-zinc-600 dark:bg-zinc-800 dark:text-zinc-400' },
  medium: { label: '置信度中', cls: 'bg-violet-100 text-violet-700 dark:bg-violet-500/20 dark:text-violet-300' },
  high: { label: '置信度高', cls: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-300' },
} as const

/** 异常的 AI 分析：摘要、可能原因与排查步骤，支持生成/重新生成 */
export default function InsightPanel({ anomaly, className }: { anomaly: Anomaly; className?: string }) {
  const qc = useQueryClient()
  const features = useQuery({ queryKey: ['features'], queryFn: api.features, staleTime: Infinity })
  const generate = useMutation({
    mutationFn: () => api.generateInsight(anomaly.id),
    onSettled: () => qc.invalidateQueries({ queryKey: ['anomalies'] }),
  })
  const insight = anomaly.ai_insight
  const enabled = !!features.data?.ai_insight
  const shell = clsx('rounded-lg border border-violet-200/70 bg-violet-50/50 p-4 text-sm dark:border-violet-500/25 dark:bg-violet-500/[0.07]', className)

  const header = (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      <span className="inline-flex items-center gap-1.5 text-xs font-semibold text-violet-700 dark:text-violet-300">
        <Sparkles className="size-3.5" /> AI 分析
      </span>
      {insight && <span className={clsx('rounded px-1.5 py-0.5 text-[11px] font-medium', confidence[insight.confidence].cls)}>{confidence[insight.confidence].label}</span>}
      {enabled && (insight || generate.isPending) && (
        <button className={clsx(ghostButtonClass, 'ml-auto px-2 py-1 text-xs')} onClick={() => generate.mutate()} disabled={generate.isPending}>
          <RefreshCw className={clsx('size-3.5', generate.isPending && 'animate-spin')} />
          {generate.isPending ? '分析中…' : '重新分析'}
        </button>
      )}
    </div>
  )

  if (!features.data && !insight) return null
  if (!insight && !enabled) {
    return (
      <div className={clsx(shell, 'text-xs text-zinc-500 dark:text-zinc-400')}>
        <Sparkles className="mr-1 inline size-3.5 text-violet-500" />
        为后端设置 <code>ANTHROPIC_API_KEY</code> 即可启用 AI 异常分析：自动给出结论、可能原因与排查步骤。
      </div>
    )
  }

  if (generate.isPending && !insight) {
    return (
      <div className={shell}>
        {header}
        <div className="space-y-2">
          <Skeleton className="h-4 w-4/5 bg-violet-200/50 dark:bg-violet-500/15" />
          <Skeleton className="h-4 w-3/5 bg-violet-200/50 dark:bg-violet-500/15" />
          <Skeleton className="h-4 w-2/3 bg-violet-200/50 dark:bg-violet-500/15" />
        </div>
        <p className="mt-3 text-xs text-zinc-500">正在结合窗口内日志与堆栈分析，通常需要十几秒…</p>
      </div>
    )
  }

  if (!insight) {
    const error = generate.error?.message ?? anomaly.ai_error
    return (
      <div className={clsx(shell, 'flex flex-col items-start')}>
        {header}
        <p className="text-xs text-zinc-500 dark:text-zinc-400">让 AI 结合异常窗口内的日志、堆栈和基线，给出结论与排查步骤。</p>
        <button className={clsx(ghostButtonClass, 'mt-3 border-violet-300 text-violet-700 dark:border-violet-500/40 dark:text-violet-300')} onClick={() => generate.mutate()}>
          <Sparkles className="size-4" /> 生成 AI 分析
        </button>
        {error && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{error}</p>}
      </div>
    )
  }

  return (
    <div className={shell}>
      {header}
      <p className="font-medium leading-6">{insight.summary}</p>
      {insight.impact && <p className="mt-1.5 text-zinc-600 dark:text-zinc-400"><span className="text-zinc-500">影响：</span>{insight.impact}</p>}
      {insight.likely_causes.length > 0 && (
        <section className="mt-4">
          <h4 className="mb-1.5 text-xs font-semibold text-zinc-500">可能原因</h4>
          <ol className="space-y-2">
            {insight.likely_causes.map((c, i) => (
              <li key={i} className="flex gap-2">
                <span className="grid size-5 shrink-0 place-items-center rounded-full bg-violet-100 text-[11px] font-semibold text-violet-700 dark:bg-violet-500/20 dark:text-violet-300">{i + 1}</span>
                <div>
                  {c.cause}
                  {c.evidence && <p className="mt-0.5 text-xs text-zinc-500">依据：{c.evidence}</p>}
                </div>
              </li>
            ))}
          </ol>
        </section>
      )}
      {insight.next_steps.length > 0 && (
        <section className="mt-4">
          <h4 className="mb-1.5 text-xs font-semibold text-zinc-500">排查步骤</h4>
          <ol className="list-decimal space-y-1 pl-5 marker:text-zinc-400">
            {insight.next_steps.map((s, i) => <li key={i} className="pl-1">{s}</li>)}
          </ol>
        </section>
      )}
      {generate.error && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{generate.error.message}</p>}
      <p className="mt-4 text-[11px] text-zinc-400">
        {anomaly.ai_model}{anomaly.ai_generated_at && ` · ${fmtRelative(anomaly.ai_generated_at)}`} · 由大模型基于日志生成，仅供参考
      </p>
    </div>
  )
}
