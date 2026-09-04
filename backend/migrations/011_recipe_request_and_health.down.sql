DROP INDEX IF EXISTS idx_site_recipes_verify_status;

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_verify_status;

ALTER TABLE site_recipes
    DROP COLUMN IF EXISTS request_body,
    DROP COLUMN IF EXISTS request_content_type,
    DROP COLUMN IF EXISTS request_headers,
    DROP COLUMN IF EXISTS keyword_in_body,
    DROP COLUMN IF EXISTS verify_status,
    DROP COLUMN IF EXISTS verified_at,
    DROP COLUMN IF EXISTS verified_jobs,
    DROP COLUMN IF EXISTS consecutive_failures,
    DROP COLUMN IF EXISTS last_error;
