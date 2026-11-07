import { queryOptions } from '@tanstack/react-query'
import { api } from './api'

// 多处共用的查询：键与选项定义在一处，缓存才能真正共享

/** 全部时间范围的总览（侧边栏角标与总览页共用） */
export const overviewAllQuery = queryOptions({ queryKey: ['overview', 'all'], queryFn: () => api.overview(), refetchInterval: 30_000 })

export const featuresQuery = queryOptions({ queryKey: ['features'], queryFn: api.features, staleTime: Infinity })

export const servicesQuery = queryOptions({ queryKey: ['services'], queryFn: api.services })
