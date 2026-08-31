DROP INDEX IF EXISTS idx_recipe_runs_created;
DROP INDEX IF EXISTS idx_recipe_runs_site_key;
DROP TABLE IF EXISTS recipe_runs;

DROP INDEX IF EXISTS idx_site_recipes_enabled;
DROP INDEX IF EXISTS idx_site_recipes_domain;
DROP TABLE IF EXISTS site_recipes;
