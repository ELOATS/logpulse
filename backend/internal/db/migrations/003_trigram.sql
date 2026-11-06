-- 子串检索（ILIKE '%关键词%'）走三元组索引，避免全表扫描。
-- pg_trgm 是 PostgreSQL 13+ 的"受信任扩展"，数据库属主即可创建，无需超级用户。
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_logs_message_trgm ON logs USING GIN (message gin_trgm_ops);
