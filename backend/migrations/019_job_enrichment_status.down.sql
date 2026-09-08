-- 回滚 JD 异步补全状态字段。

ALTER TABLE jobs
    DROP COLUMN IF EXISTS enrichment_updated_at,
    DROP COLUMN IF EXISTS enrichment_error,
    DROP COLUMN IF EXISTS enrichment_status;
