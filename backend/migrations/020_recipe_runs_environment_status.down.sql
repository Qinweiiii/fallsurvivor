-- 回滚：把 recipe_runs.status 约束恢复为原来的 (success, empty, failed)。
ALTER TABLE recipe_runs
    DROP CONSTRAINT IF EXISTS chk_recipe_runs_status;

ALTER TABLE recipe_runs
    ADD CONSTRAINT chk_recipe_runs_status
    CHECK (status IN ('success', 'empty', 'failed'));
