UPDATE site_recipes
   SET enabled = TRUE,
       last_error = '',
       updated_at = NOW()
 WHERE source = 'preset'
   AND strategy_type = 'browser'
   AND site_key IN ('tencent', 'bytedance');
