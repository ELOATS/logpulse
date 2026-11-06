-- 日志模式（模板聚类结果）
CREATE TABLE IF NOT EXISTS log_patterns (
    id          BIGSERIAL PRIMARY KEY,
    hash        TEXT        NOT NULL UNIQUE,
    service     TEXT        NOT NULL DEFAULT '',
    level       TEXT        NOT NULL DEFAULT 'INFO',
    template    TEXT        NOT NULL,
    sample      TEXT        NOT NULL DEFAULT '',
    count       BIGINT      NOT NULL DEFAULT 0,
    first_seen  TIMESTAMPTZ NOT NULL,
    last_seen   TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_patterns_last_seen ON log_patterns (last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_patterns_count ON log_patterns (count DESC);

-- 原始日志
CREATE TABLE IF NOT EXISTS logs (
    id          BIGSERIAL PRIMARY KEY,
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
                ) STORED
);
CREATE INDEX IF NOT EXISTS idx_logs_ts ON logs (ts DESC);
CREATE INDEX IF NOT EXISTS idx_logs_level_ts ON logs (level, ts DESC);
CREATE INDEX IF NOT EXISTS idx_logs_service_ts ON logs (service, ts DESC);
CREATE INDEX IF NOT EXISTS idx_logs_pattern ON logs (pattern_id);
CREATE INDEX IF NOT EXISTS idx_logs_search ON logs USING GIN (search);
CREATE INDEX IF NOT EXISTS idx_logs_attributes ON logs USING GIN (attributes jsonb_path_ops);

-- 异常检测结果
CREATE TABLE IF NOT EXISTS anomalies (
    id            BIGSERIAL PRIMARY KEY,
    dedup_key     TEXT        NOT NULL UNIQUE,
    kind          TEXT        NOT NULL,           -- error_spike | volume_spike | new_pattern
    severity      TEXT        NOT NULL,           -- low | medium | high
    service       TEXT        NOT NULL DEFAULT '',
    pattern_id    BIGINT      REFERENCES log_patterns (id) ON DELETE CASCADE,
    window_start  TIMESTAMPTZ NOT NULL,
    window_end    TIMESTAMPTZ NOT NULL,
    score         DOUBLE PRECISION NOT NULL DEFAULT 0,
    observed      DOUBLE PRECISION NOT NULL DEFAULT 0,
    expected      DOUBLE PRECISION NOT NULL DEFAULT 0,
    description   TEXT        NOT NULL,
    details       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    acknowledged  BOOLEAN     NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_anomalies_window ON anomalies (window_start DESC);
