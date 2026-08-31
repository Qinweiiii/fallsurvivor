-- 回滚：移除 desc_quality 字段

ALTER TABLE jobs DROP COLUMN IF EXISTS desc_quality;
