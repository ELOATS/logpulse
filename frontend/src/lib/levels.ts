import { AlertOctagon, Sparkle, TrendingUp, type LucideIcon } from 'lucide-react'
import type { Anomaly, Level, Severity } from './api'

export const LEVELS: Level[] = ['FATAL', 'ERROR', 'WARN', 'INFO', 'DEBUG']

/** 图表用的级别颜色（亮/暗模式下都有足够对比度） */
export const levelColor: Record<Level, string> = {
  FATAL: '#c026d3',
  ERROR: '#ef4444',
  WARN: '#f59e0b',
  INFO: '#0ea5e9',
  DEBUG: '#a1a1aa',
}

/** 卡片左侧色条 */
export const severityStripe: Record<Severity, string> = {
  high: 'border-l-red-500',
  medium: 'border-l-orange-400',
  low: 'border-l-amber-300 dark:border-l-amber-500/60',
}

/** 视为"错误"的级别（与后端 parser.ErrorLevels 一致），以及对应的查询参数值 */
export const ERROR_LEVELS: Level[] = ['ERROR', 'FATAL']
export const ERROR_LEVELS_PARAM = ERROR_LEVELS.join(',')

/** 常见日志库存放异常堆栈的字段名（与后端 parser.StackKeys 一致） */
export const STACK_KEYS = ['stack', 'stacktrace', 'stack_trace', 'error.stack']

/** 异常类型的名称与图标 */
export const anomalyKinds: Record<Anomaly['kind'], { label: string; icon: LucideIcon }> = {
  error_spike: { label: '错误突增', icon: AlertOctagon },
  volume_spike: { label: '流量突增', icon: TrendingUp },
  new_pattern: { label: '新错误模式', icon: Sparkle },
}
