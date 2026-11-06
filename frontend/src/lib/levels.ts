import type { Level, Severity } from './api'

export const LEVELS: Level[] = ['FATAL', 'ERROR', 'WARN', 'INFO', 'DEBUG']

/** 图表用的级别颜色（亮/暗模式下都有足够对比度） */
export const levelColor: Record<Level, string> = {
  FATAL: '#c026d3',
  ERROR: '#ef4444',
  WARN: '#f59e0b',
  INFO: '#0ea5e9',
  DEBUG: '#a1a1aa',
}

export const severityRank: Record<Severity, number> = { high: 0, medium: 1, low: 2 }

/** 卡片左侧色条 */
export const severityStripe: Record<Severity, string> = {
  high: 'border-l-red-500',
  medium: 'border-l-orange-400',
  low: 'border-l-amber-300 dark:border-l-amber-500/60',
}
