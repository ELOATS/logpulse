-- 大模型生成的异常分析
ALTER TABLE anomalies
    ADD COLUMN IF NOT EXISTS ai_insight      JSONB,
    ADD COLUMN IF NOT EXISTS ai_model        TEXT,
    ADD COLUMN IF NOT EXISTS ai_error        TEXT,
    ADD COLUMN IF NOT EXISTS ai_generated_at TIMESTAMPTZ;
