-- 允许 recipe_runs.status 取值 'environment'。
--
-- 'environment' 表示本次执行因浏览器环境故障（Worker 未启动 / 登录态过期）而失败，
-- 与 Recipe 配置无关：只记录执行历史供观测，不计入连续失败、不触发 Recipe 失效。
-- 这样「只是 Worker 没开 / 登录掉了」这类用户一小时内能修好的事，
-- 不会把一条好配置废掉、下次命中回退重探、白白烧 token。
ALTER TABLE recipe_runs
    DROP CONSTRAINT IF EXISTS chk_recipe_runs_status;

ALTER TABLE recipe_runs
    ADD CONSTRAINT chk_recipe_runs_status
    CHECK (status IN ('success', 'empty', 'failed', 'environment'));
