-- 恢复 jobs 表文本字段长度限制（与 007_widen_job_text_fields.up.sql 反向）。
--
-- 注意：若库中已有超长数据，本操作会失败。
-- 需先截断超长值（例如用 left(department, 200)）后再执行本文件。

ALTER TABLE jobs
    ALTER COLUMN company_name      TYPE VARCHAR(200),
    ALTER COLUMN department        TYPE VARCHAR(200),
    ALTER COLUMN business          TYPE VARCHAR(200),
    ALTER COLUMN title             TYPE VARCHAR(300),
    ALTER COLUMN location          TYPE VARCHAR(200),
    ALTER COLUMN source_url        TYPE VARCHAR(1000),
    ALTER COLUMN official_url      TYPE VARCHAR(1000),
    ALTER COLUMN normalized_url    TYPE VARCHAR(1000),
    ALTER COLUMN dedup_fingerprint TYPE VARCHAR(200);

ALTER TABLE job_sources
    ALTER COLUMN source_name TYPE VARCHAR(200),
    ALTER COLUMN url         TYPE VARCHAR(1000);
