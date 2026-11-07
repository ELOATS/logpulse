// 后端 API 客户端与类型定义
const BASE = import.meta.env.VITE_API_BASE ?? ''

export type Level = 'DEBUG' | 'INFO' | 'WARN' | 'ERROR' | 'FATAL'
export type Severity = 'low' | 'medium' | 'high'

export interface LogRow {
  id: number
  timestamp: string
  level: Level
  service: string
  host: string
  message: string
  raw: string
  format: string
  attributes: Record<string, unknown>
  pattern_id: number | null
}

export interface LogPage { items: LogRow[]; total: number; total_capped?: boolean }

export interface Overview {
  total_logs: number
  error_logs: number
  error_rate: number
  patterns: number
  open_anomalies: number
  services: number
  first_log: string | null
  last_log: string | null
  levels: Partial<Record<Level, number>>
  top_services: { name: string; count: number; errors: number }[]
}

export interface TimePoint { bucket: string; total: number; errors: number; warns: number }

export interface Pattern {
  id: number
  service: string
  level: Level
  template: string
  sample: string
  count: number
  first_seen: string
  last_seen: string
}

export interface TopPattern { pattern_id: number; template: string; level: Level; count: number }

export interface Anomaly {
  id: number
  kind: 'error_spike' | 'volume_spike' | 'new_pattern'
  severity: Severity
  service: string
  pattern_id: number | null
  window_start: string
  window_end: string
  score: number
  observed: number
  expected: number
  description: string
  details: { top_patterns?: TopPattern[]; template?: string; sample?: string; count?: number }
  acknowledged: boolean
  created_at: string
  ai_insight: AIInsight | null
  ai_model: string | null
  ai_error: string | null
  ai_generated_at: string | null
}

export interface AIInsight {
  summary: string
  impact: string
  likely_causes: { cause: string; evidence: string }[]
  next_steps: string[]
  confidence: 'low' | 'medium' | 'high'
}

export interface Features { ai_insight: boolean; ai_model?: string }

export interface IngestResult { accepted: number; new_patterns: number }
export interface AnalyzeResult { from: string; to: string; services: number; detected: number; new_anomalies: number; duration_ms: number }

export type Params = Record<string, string | number | boolean | undefined | null>

/** 拼接查询串，忽略空值 */
export function qs(params: Params = {}) {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, init)
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(body.error ?? `请求失败（${res.status}）`)
  return body as T
}

export const api = {
  overview: (p?: Params) => request<Overview>(`/api/stats/overview${qs(p)}`),
  timeseries: (p?: Params) => request<TimePoint[]>(`/api/stats/timeseries${qs(p)}`),
  services: () => request<string[]>('/api/services'),
  logs: (p?: Params) => request<LogPage>(`/api/logs${qs(p)}`),
  patterns: (p?: Params) => request<Pattern[]>(`/api/patterns${qs(p)}`),
  anomalies: (p?: Params) => request<Anomaly[]>(`/api/anomalies${qs(p)}`),
  ackAnomaly: (id: number, acknowledged: boolean) =>
    request(`/api/anomalies/${id}/ack`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ acknowledged }),
    }),
  features: () => request<Features>('/api/features'),
  generateInsight: (id: number) => request<Anomaly>(`/api/anomalies/${id}/insight`, { method: 'POST' }),
  analyze: () => request<AnalyzeResult>('/api/analyze', { method: 'POST' }),
  upload: (file: File, service: string) => {
    const fd = new FormData()
    fd.append('file', file)
    if (service) fd.append('service', service)
    return request<IngestResult>('/api/logs/upload', { method: 'POST', body: fd })
  },
  ingestText: (text: string, service: string) =>
    request<IngestResult>(`/api/logs${qs({ service })}`, {
      method: 'POST',
      headers: { 'Content-Type': 'text/plain' },
      body: text,
    }),
}
