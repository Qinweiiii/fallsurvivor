ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_strategy_type;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_strategy_type
    CHECK (strategy_type IN ('browser','api','browser_api','url_template'));

UPDATE site_recipes
   SET strategy_type = 'browser_api'
 WHERE strategy_type = 'browser_observed';

ALTER TABLE site_recipes DROP COLUMN IF EXISTS browser_plan;
