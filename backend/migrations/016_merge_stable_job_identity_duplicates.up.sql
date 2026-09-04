-- 旧版本把列表请求的临时 query（例如 _csrf）及真实详情 URL 都用于岗位身份，
-- 同一岗位跨会话会产生重复记录。按系统生成的 #job_id 身份合并：保留最早记录。
-- 带用户收藏或投递的重复项不删除，避免丢失用户状态。
CREATE TEMP TABLE job_identity_merge ON COMMIT DROP AS
WITH legacy_ranked AS (
    SELECT id,
           company_name,
           substring(normalized_url FROM '#job_id=([^&#]+)') AS job_id,
           row_number() OVER (
               PARTITION BY company_name, substring(normalized_url FROM '#job_id=([^&#]+)')
               ORDER BY created_at, id
           ) AS rn
      FROM jobs
     WHERE normalized_url ~ '#job_id=[^&#]+'
),
keepers AS (
    SELECT id, company_name, job_id FROM legacy_ranked WHERE rn = 1
),
duplicates AS (
    SELECT l.id AS duplicate_id, k.id AS keeper_id
      FROM legacy_ranked l
      JOIN keepers k ON k.company_name = l.company_name AND k.job_id = l.job_id
     WHERE l.rn > 1
    UNION
    SELECT j.id AS duplicate_id, k.id AS keeper_id
      FROM jobs j
      JOIN keepers k
        ON k.company_name = j.company_name
       AND j.id <> k.id
       AND substring(coalesce(nullif(j.official_url, ''), j.source_url)
                     FROM '/([0-9]{5,})([?]|$)') = k.job_id
     WHERE coalesce(nullif(j.official_url, ''), j.source_url) ~ '/[0-9]{5,}([?]|$)'
)
SELECT d.duplicate_id,
       d.keeper_id,
       coalesce(nullif(j.official_url, ''), j.source_url) AS detail_url
  FROM duplicates d
  JOIN jobs j ON j.id = d.duplicate_id;

INSERT INTO job_sources (job_id, source_type, source_name, url, first_seen_at, last_seen_at, is_valid)
SELECT m.keeper_id, s.source_type, s.source_name, s.url, s.first_seen_at, s.last_seen_at, s.is_valid
  FROM job_identity_merge m
  JOIN job_sources s ON s.job_id = m.duplicate_id
ON CONFLICT (job_id, url) DO UPDATE
   SET first_seen_at = LEAST(job_sources.first_seen_at, EXCLUDED.first_seen_at),
       last_seen_at = GREATEST(job_sources.last_seen_at, EXCLUDED.last_seen_at),
       is_valid = job_sources.is_valid OR EXCLUDED.is_valid;

DELETE FROM jobs j
 USING job_identity_merge m
 WHERE j.id = m.duplicate_id
   AND NOT EXISTS (SELECT 1 FROM job_carts c WHERE c.job_id = j.id)
   AND NOT EXISTS (SELECT 1 FROM applications a WHERE a.job_id = j.id);

WITH detail_urls AS (
    SELECT DISTINCT ON (m.keeper_id) m.keeper_id, m.detail_url
      FROM job_identity_merge m
     WHERE m.detail_url <> ''
       AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = m.duplicate_id)
     ORDER BY m.keeper_id, m.detail_url
)
UPDATE jobs k
   SET source_url = CASE WHEN k.source_url = '' THEN u.detail_url ELSE k.source_url END,
       official_url = CASE WHEN k.official_url = '' THEN u.detail_url ELSE k.official_url END
  FROM detail_urls u
 WHERE k.id = u.keeper_id;
