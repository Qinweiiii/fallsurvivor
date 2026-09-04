-- 回滚：把本次迁移标记的记录还原为未验证。
--
-- 无法精确还原（不知道原本是 unverified 还是 verified），
-- 按 last_error 特征识别本次改动的记录并置回 unverified。

UPDATE site_recipes
   SET verify_status = 'unverified',
       last_error    = '',
       updated_at    = now()
 WHERE verify_status = 'invalid'
   AND (
        last_error LIKE '历史遗留配置缺少可执行字段%'
     OR last_error LIKE '探索产物不应为浏览器策略%'
   );
