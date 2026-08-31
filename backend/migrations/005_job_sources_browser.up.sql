-- job_sources.source_type 同样需要允许 BROWSER（浏览器半固定脚本来源）。
-- 004 只改了 jobs 表，漏掉了 job_sources，导致插入浏览器来源记录时
-- 违反 chk_job_sources_type 检查约束 (SQLSTATE 23514)。
ALTER TABLE job_sources DROP CONSTRAINT IF EXISTS chk_job_sources_type;
ALTER TABLE job_sources ADD CONSTRAINT chk_job_sources_type
    CHECK (source_type IN ('OFFICIAL','BOSS','TAVILY','MANUAL','BROWSER'));
