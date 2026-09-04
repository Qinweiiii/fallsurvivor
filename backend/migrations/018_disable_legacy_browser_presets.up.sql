-- 腾讯、字节不再由内置 browser adapter 采集。保留旧记录以便排查，
-- 但禁止 Registry 命中；后续由 Explorer 生成 browser_observed Recipe。
UPDATE site_recipes
   SET enabled = FALSE,
       last_error = 'legacy browser preset retired; re-explore to create browser_observed recipe',
       updated_at = NOW()
 WHERE source = 'preset'
   AND strategy_type = 'browser'
   AND site_key IN ('tencent', 'bytedance');
