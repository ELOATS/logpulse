import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw, Sparkles } from 'lucide-react'
import clsx from 'clsx'
import { api, type Anomaly } from '../lib/api'
import { fmtTime } from '../lib/format'
import { ghostButtonClass } from './ui'

const confidenceLabel = { low: '低', medium: '中', high: '高' } as const

/** 异常卡片中的 AI 分析区块：展示摘要、可能原因与排查步骤，支持手动生成/重新生成 */
export default function InsightPanel({ anomaly }: { anomaly: Anomaly }) {
  const qc = useQueryClient()
  const features = useQuery({ queryKey: ['features'], queryFn: api.features, staleTime: Infinity })
  const generate = useMutation({
    mutationFn: () => api.generateInsight(anomaly.id),
    onSettled: () => qc.invalidateQueries({ queryKey: ['anomalies'] }),
  })
  const insight = anomaly.ai_insight
  const enabled = features.data?.ai_insight

  if (!insight && !enabled) {
    if (!features.data) return null
    return (
      <p className="mt-3 text-xs text-zinc-400">
        <Sparkles className="mr-1 inline size-3.5" />
        为后端设置 ANTHROPIC_API_KEY 即可启用 AI 异常分析
      </p>
    )
  }

  const button = enabled && (
    <button className={clsx(ghostButtonClass, 'py-1 text-xs')} onClick={() => generate.mutate()} disabled={generate.isPending}>
      {generate.isPending ? <RefreshCw className="size-3.5 animate-spin" /> : <Sparkles className="size-3.5" />}
      {generate.isPending ? '分析中…' : insight ? '重新分析' : 'AI 分析'}
    </button>
  )
  const error = generate.error?.message ?? (!insight ? anomaly.ai_error : null)

  if (!insight) {
    return (
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {button}
        {error && <span className="text-xs text-red-600 dark:text-red-400">{error}</span>}
      </div>
    )
  }

  return (
    <div className="mt-3 rounded-md border border-violet-200 bg-violet-50/60 p-3 text-sm dark:border-violet-500/30 dark:bg-violet-500/10">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="inline-flex items-center gap-1 text-xs font-medium text-violet-700 dark:text-violet-300">
          <Sparkles className="size-3.5" />
          AI 分析
        </span>
        <span className="text-xs text-zinc-500">
          置信度 {confidenceLabel[insight.confidence]}
          {anomaly.ai_model && ` · ${anomaly.ai_model}`}
          {anomaly.ai_generated_at && ` · ${fmtTime(anomaly.ai_generated_at, false)}`}
        </span>
        <span className="ml-auto">{button}</span>
      </div>
      <p className="font-medium">{insight.summary}</p>
      {insight.impact && <p className="mt-1 text-zinc-600 dark:text-zinc-400">影响：{insight.impact}</p>}
      {insight.likely_causes.length > 0 && (
        <div className="mt-3">
          <div className="mb-1 text-xs font-medium text-zinc-500">可能原因</div>
          <ol className="list-decimal space-y-1 pl-5">
            {insight.likely_causes.map((c, i) => (
              <li key={i}>
                {c.cause}
                {c.evidence && <span className="block text-xs text-zinc-500">依据：{c.evidence}</span>}
              </li>
            ))}
          </ol>
        </div>
      )}
      {insight.next_steps.length > 0 && (
        <div className="mt-3">
          <div className="mb-1 text-xs font-medium text-zinc-500">排查步骤</div>
          <ol className="list-decimal space-y-1 pl-5">
            {insight.next_steps.map((s, i) => <li key={i}>{s}</li>)}
          </ol>
        </div>
      )}
      {generate.error && <p className="mt-2 text-xs text-red-600 dark:text-red-400">{generate.error.message}</p>}
      <p className="mt-3 text-xs text-zinc-400">由大模型基于日志自动生成，仅供排查参考</p>
    </div>
  )
}
