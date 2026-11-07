export const fmtNumber = (n: number) => new Intl.NumberFormat('zh-CN').format(n)

export const fmtPercent = (n: number, digits = 2) => `${(n * 100).toFixed(digits)}%`

/** 服务名为空时显示"(未命名)" */
export const svcName = (s: string) => s || '(未命名)'

export function fmtTime(iso: string, withSeconds = true) {
  const d = new Date(iso)
  const pad = (x: number) => String(x).padStart(2, '0')
  const base = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  return withSeconds ? `${base}:${pad(d.getSeconds())}` : base
}

export function fmtShortTime(iso: string) {
  const d = new Date(iso)
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

/** 根据时间跨度选择合适的聚合粒度，让图表保持约 60~120 个点 */
export function pickBucket(first?: string | null, last?: string | null) {
  if (!first || !last) return '1m'
  const span = (new Date(last).getTime() - new Date(first).getTime()) / 1000
  const options = [60, 300, 900, 1800, 3600, 3 * 3600, 6 * 3600, 86400]
  const sec = options.find((o) => span / o <= 120) ?? 86400
  return sec >= 3600 ? `${sec / 3600}h` : `${sec / 60}m`
}

/** 将 "15m"、"1h"、"30s" 这样的时长转为毫秒 */
export function durationMs(d: string) {
  const m = /^(\d+)(s|m|h|d)$/.exec(d)
  if (!m) return 60_000
  return Number(m[1]) * { s: 1000, m: 60_000, h: 3600_000, d: 86400_000 }[m[2] as 's' | 'm' | 'h' | 'd']
}

/** 紧凑时间：MM-DD HH:mm:ss */
export function fmtCompact(iso: string) {
  const d = new Date(iso)
  const pad = (x: number) => String(x).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 相对时间：刚刚 / 5 分钟前 / 3 小时前 / 2 天前 */
export function fmtRelative(iso: string, now = Date.now()) {
  const sec = Math.round((now - new Date(iso).getTime()) / 1000)
  if (sec < 45) return '刚刚'
  if (sec < 3600) return `${Math.round(sec / 60)} 分钟前`
  if (sec < 86400) return `${Math.round(sec / 3600)} 小时前`
  if (sec < 30 * 86400) return `${Math.round(sec / 86400)} 天前`
  return fmtTime(iso, false)
}

/** 时间范围选项（相对最新一条日志） */
export const RANGES = [
  { key: '1h', label: '1 小时', ms: 3600_000 },
  { key: '6h', label: '6 小时', ms: 6 * 3600_000 },
  { key: '24h', label: '24 小时', ms: 24 * 3600_000 },
  { key: '7d', label: '7 天', ms: 7 * 86400_000 },
  { key: 'all', label: '全部', ms: 0 },
] as const
export type RangeKey = (typeof RANGES)[number]['key']
