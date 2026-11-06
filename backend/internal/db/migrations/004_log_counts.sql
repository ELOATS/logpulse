-- 按分钟预聚合的日志计数：总览、趋势图、异常检测读取此表，
-- 查询耗时只与时间范围内的分钟数有关，不再随日志总量增长。
-- 写入日志时在同一事务中累加（见 store.Ingest），保证与 logs 表一致。
CREATE TABLE IF NOT EXISTS log_counts (
    bucket  TIMESTAMPTZ NOT NULL,          -- 分钟起点（按 Unix 时间取整，与时区无关）
    service TEXT        NOT NULL,
    level   TEXT        NOT NULL,
    count   BIGINT      NOT NULL,
    PRIMARY KEY (bucket, service, level)
);

-- 回填已有数据
INSERT INTO log_counts (bucket, service, level, count)
SELECT to_timestamp(floor(extract(epoch FROM ts) / 60) * 60), service, level, count(*)
FROM logs
GROUP BY 1, 2, 3
ON CONFLICT (bucket, service, level) DO UPDATE SET count = EXCLUDED.count;
