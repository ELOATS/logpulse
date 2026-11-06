-- 把 logs 改为按天（UTC）分区的表：
--   * 按时间过滤的查询只扫描相关分区；
--   * 数据保留通过删除整个分区实现：耗时与数据量基本无关，磁盘空间立即释放；
--     DELETE 则要逐行删除并维护所有索引，删除后的空间要等 VACUUM 才能复用。
-- 新分区在写入时按需创建（见 store.ensurePartitions）。
-- 已有数据会被迁移到新表，id 与序列保持不变。

-- 1. 旧表改名，并去掉会与新表重名的约束和索引
ALTER TABLE logs RENAME TO logs_old;
ALTER TABLE logs_old RENAME CONSTRAINT logs_pkey TO logs_old_pkey;
ALTER TABLE logs_old DROP CONSTRAINT IF EXISTS logs_pattern_id_fkey;
DROP INDEX IF EXISTS idx_logs_ts, idx_logs_level_ts, idx_logs_service_ts, idx_logs_pattern,
    idx_logs_search, idx_logs_attributes, idx_logs_message_trgm;
-- 序列归属旧表，删除旧表时会被一并删除：先解除归属
ALTER SEQUENCE logs_id_seq OWNED BY NONE;

-- 2. 新建分区表（主键需包含分区键）
CREATE TABLE logs (
    id          BIGINT      NOT NULL DEFAULT nextval('logs_id_seq'),
    ts          TIMESTAMPTZ NOT NULL,
    level       TEXT        NOT NULL,
    service     TEXT        NOT NULL DEFAULT '',
    host        TEXT        NOT NULL DEFAULT '',
    message     TEXT        NOT NULL,
    raw         TEXT        NOT NULL,
    format      TEXT        NOT NULL DEFAULT 'plain',
    attributes  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    pattern_id  BIGINT      REFERENCES log_patterns (id) ON DELETE SET NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    search      TSVECTOR GENERATED ALWAYS AS (
                    to_tsvector('simple', coalesce(service, '') || ' ' || coalesce(host, '') || ' ' || message)
                ) STORED,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);
ALTER SEQUENCE logs_id_seq OWNED BY logs.id;

-- 3. 为已有数据涉及的每一天建分区
DO $$
DECLARE
    d DATE;
BEGIN
    FOR d IN SELECT DISTINCT (ts AT TIME ZONE 'UTC')::date FROM logs_old LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF logs FOR VALUES FROM (%L) TO (%L)',
            'logs_p' || to_char(d, 'YYYYMMDD'),
            (d::timestamp AT TIME ZONE 'UTC'),
            ((d + 1)::timestamp AT TIME ZONE 'UTC'));
    END LOOP;
END $$;

-- 4. 迁移数据（search 为生成列，自动计算），再建索引（先导数据后建索引更快）
INSERT INTO logs (id, ts, level, service, host, message, raw, format, attributes, pattern_id, ingested_at)
SELECT id, ts, level, service, host, message, raw, format, attributes, pattern_id, ingested_at FROM logs_old;
DROP TABLE logs_old;

CREATE INDEX idx_logs_ts ON logs (ts DESC);
CREATE INDEX idx_logs_level_ts ON logs (level, ts DESC);
CREATE INDEX idx_logs_service_ts ON logs (service, ts DESC);
CREATE INDEX idx_logs_pattern ON logs (pattern_id);
CREATE INDEX idx_logs_search ON logs USING GIN (search);
CREATE INDEX idx_logs_attributes ON logs USING GIN (attributes jsonb_path_ops);
CREATE INDEX idx_logs_message_trgm ON logs USING GIN (message gin_trgm_ops);

-- 新分区还没有统计信息，规划器可能选出很差的计划（实测全文检索在迁移后的
-- 前一两分钟慢到 2.8 秒，自动 ANALYZE 完成后恢复到个位数毫秒）。立即收集统计信息。
ANALYZE logs;
