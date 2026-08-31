-- 恢复去重唯一索引（与 006_disable_dedup_indexes.up.sql 反向）。

DROP INDEX IF EXISTS idx_jobs_normalized_url;
DROP INDEX IF EXISTS idx_jobs_fingerprint;

-- 注意：若库中已有重复数据，重建唯一索引会失败。
-- 需先清理重复记录（保留 id 最小的一条）后再执行本文件。
CREATE UNIQUE INDEX uq_jobs_normalized_url ON jobs(normalized_url) WHERE normalized_url <> '';
CREATE UNIQUE INDEX uq_jobs_fingerprint   ON jobs(dedup_fingerprint) WHERE dedup_fingerprint <> '';
