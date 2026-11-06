export const fmtNumber = (n: number) => new Intl.NumberFormat('zh-CN').format(n)

export const fmtPercent = (n: number) => `${(n * 100).toFixed(2)}%`

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
