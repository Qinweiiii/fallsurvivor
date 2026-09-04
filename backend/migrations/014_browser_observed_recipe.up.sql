-- 浏览器观测策略保存的是“触发页面请求的动作”，不是一条脱离页面重放的接口。
ALTER TABLE site_recipes
    ADD COLUMN IF NOT EXISTS browser_plan JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_strategy_type;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_strategy_type
    CHECK (strategy_type IN ('browser','api','browser_api','browser_observed','url_template'));

UPDATE site_recipes
   SET strategy_type = 'browser_observed'
 WHERE strategy_type = 'browser_api';

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_strategy_type;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_strategy_type
    CHECK (strategy_type IN ('browser','api','browser_observed','url_template'));
