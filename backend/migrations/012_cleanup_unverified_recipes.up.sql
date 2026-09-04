-- 清理「未经验证就落库」时期产生的脏 Recipe。
--
-- 背景：在引入「验证通过才保存」之前，探索产出的配置会直接落库，
-- 其中包含一些根本跑不通的记录（如 api 策略却 list_api / list_path 为空）。
-- 这类记录会被 Registry 命中，导致 Fast Path 每次都失败，
-- 而且因为它「存在」，还会阻止系统重新探索该站点。
--
-- 处理方式：标记为 invalid 而不是删除。
--   - invalid 会让 Fast Path 主动跳过并触发重新探索（自愈闭环）；
--   - 保留记录便于追溯该站点曾被探索过、失败原因是什么；
--   - 一旦重探并验证通过，状态会自动回到 verified。

UPDATE site_recipes
   SET verify_status = 'invalid',
       last_error    = '历史遗留配置缺少可执行字段（list_api / list_path / title_field），已交回自动探索重建',
       updated_at    = now()
 WHERE strategy_type = 'api'
   AND verify_status <> 'invalid'
   AND (
        btrim(list_api)    = ''
     OR btrim(list_path)   = ''
     OR btrim(title_field) = ''
   );

-- 一致性规则：探索产物只可能是 api 策略。
--
-- browser 策略需要 Worker 侧存在对应的站点适配器（代码实现），
-- 探索器无法凭空生成适配器，因此「source=exploration 且 strategy=browser」
-- 必然是早期版本的产物——其 adapter_key 只是照抄了 site_key，
-- Worker 侧并不存在该适配器，执行时必定失败。
--
-- 注意这不是针对某个站点的特判，而是数据自身的逻辑矛盾。
UPDATE site_recipes
   SET verify_status = 'invalid',
       last_error    = '探索产物不应为浏览器策略（Worker 侧无对应适配器），已交回自动探索重建',
       updated_at    = now()
 WHERE source        = 'exploration'
   AND strategy_type = 'browser'
   AND verify_status <> 'invalid';

-- 探索产出但从未验证过的配置：不确定是否仍然可用，
-- 保持 unverified 即可（用户可在配置页点「验证」，或下次执行时自动累计健康度）。
-- 这里刻意不批量标记 invalid——那会触发一轮不必要的全量重探。
