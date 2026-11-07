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

/** 时间范围选项（由服务端以最新一条日志为锚点解析） */
export const RANGES = [
  { key: '1h', label: '1 小时' },
  { key: '6h', label: '6 小时' },
  { key: '24h', label: '24 小时' },
  { key: '7d', label: '7 天' },
  { key: 'all', label: '全部' },
] as const
export type RangeKey = (typeof RANGES)[number]['key']
