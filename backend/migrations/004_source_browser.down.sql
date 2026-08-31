-- 回滚：移除 BROWSER。
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS chk_jobs_source_type;
ALTER TABLE jobs ADD CONSTRAINT chk_jobs_source_type
    CHECK (source_type IN ('OFFICIAL','BOSS','TAVILY','MANUAL'));
