ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_method;

ALTER TABLE site_recipes
    DROP COLUMN IF EXISTS field_map,
    DROP COLUMN IF EXISTS keyword_param,
    DROP COLUMN IF EXISTS list_path,
    DROP COLUMN IF EXISTS title_field,
    DROP COLUMN IF EXISTS id_field,
    DROP COLUMN IF EXISTS method,
    DROP COLUMN IF EXISTS detail_url_template,
    DROP COLUMN IF EXISTS detail_api,
    DROP COLUMN IF EXISTS list_api;
