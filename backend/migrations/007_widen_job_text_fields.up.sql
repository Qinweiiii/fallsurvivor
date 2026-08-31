-- 放宽 jobs 表文本字段长度限制（主链路优先）。
--
-- 背景：腾讯校招一个岗位常横跨数十个部门，department 字段会拼成
-- 「腾讯金融科技(CDG) | 腾讯营销(CDG) | ... 」这样的长字符串（实测 202+ 字符），
-- 而原表 department/business 等为 VARCHAR(200)，插入时报：
--   ERROR: value too long for type character varying(200) (SQLSTATE 22001)
-- 导致岗位无法入库。
--
-- 策略：把这些「可能很长」的文本字段统一改为 TEXT（不限长），
-- 保留必要的语义约束（NOT NULL / DEFAULT），仅放宽长度。

ALTER TABLE jobs
    ALTER COLUMN company_name      TYPE TEXT,
    ALTER COLUMN department        TYPE TEXT,
    ALTER COLUMN business          TYPE TEXT,
    ALTER COLUMN title             TYPE TEXT,
    ALTER COLUMN location          TYPE TEXT,
    ALTER COLUMN source_url        TYPE TEXT,
    ALTER COLUMN official_url      TYPE TEXT,
    ALTER COLUMN normalized_url    TYPE TEXT,
    ALTER COLUMN dedup_fingerprint TYPE TEXT;

-- job_sources 同理放宽，避免来源名 / URL 较长时插入失败。
ALTER TABLE job_sources
    ALTER COLUMN source_name TYPE TEXT,
    ALTER COLUMN url         TYPE TEXT;
