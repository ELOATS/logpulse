-- 删除从未被查询使用、只增加写入成本的索引：
--   * idx_logs_attributes：没有任何查询按 attributes 过滤（GIN 索引的维护是写入成本的大头之一）；
--   * log_patterns 的 count / last_seen 索引：每个写入批次都会更新这两列，有索引时无法做
--     HOT 更新，每次都要写所有索引；该表只有数千行，排序取前 N 条不需要索引。
DROP INDEX IF EXISTS idx_logs_attributes;
DROP INDEX IF EXISTS idx_patterns_count;
DROP INDEX IF EXISTS idx_patterns_last_seen;
