import { useSearchParams } from 'react-router'
import { qs, type Params } from './api'

/** 日志检索页链接，如 logsUrl({ pattern_id: 3 }) → /logs?pattern_id=3 */
export const logsUrl = (params: Params) => `/logs${qs(params)}`

/**
 * 读写 URL 查询参数：patch 中值为空的键会被删除。
 * resetPage：修改筛选条件时回到第一页；replace：不新增浏览历史。
 */
export function useParamPatch({ replace = false, resetPage = false } = {}) {
  const [params, setParams] = useSearchParams()
  const patch = (p: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(p)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    if (resetPage && !('page' in p)) next.delete('page')
    setParams(next, { replace })
  }
  return [params, patch, setParams] as const
}
