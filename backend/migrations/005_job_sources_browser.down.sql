ALTER TABLE job_sources DROP CONSTRAINT IF EXISTS chk_job_sources_type;
ALTER TABLE job_sources ADD CONSTRAINT chk_job_sources_type
    CHECK (source_type IN ('OFFICIAL','BOSS','TAVILY','MANUAL'));
