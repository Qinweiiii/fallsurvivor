UPDATE site_recipes
SET strategy_type = 'api'
WHERE strategy_type = 'browser_api';

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_strategy_type;

ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_strategy_type
    CHECK (strategy_type IN ('browser','api','url_template'));
