-- 为 jobs.source_type 增加 BROWSER（浏览器半固定脚本来源）。
ALTER TABLE jobs DROP CONSTRAINT IF EXISTS chk_jobs_source_type;
ALTER TABLE jobs ADD CONSTRAINT chk_jobs_source_type
    CHECK (source_type IN ('OFFICIAL','BOSS','TAVILY','MANUAL','BROWSER'));
