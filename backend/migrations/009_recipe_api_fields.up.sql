-- 让探索产出的接口信息成为可执行 Recipe，而不只是备注文本。

ALTER TABLE site_recipes
    ADD COLUMN IF NOT EXISTS list_api            TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS detail_api          TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS detail_url_template TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS method              VARCHAR(10) NOT NULL DEFAULT 'GET',
    ADD COLUMN IF NOT EXISTS id_field            TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS title_field         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS list_path           TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS keyword_param       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS field_map           JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_method;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_method
    CHECK (method IN ('GET','POST'));
