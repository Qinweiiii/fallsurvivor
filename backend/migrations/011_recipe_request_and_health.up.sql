-- 让「怎么发这个请求」与「这条 Recipe 还有效吗」都成为数据，而不是代码分支。
--
-- 背景：此前 Recipe 只记录接口 URL 与方法，请求体靠 Go 代码猜测
-- （曾为某站点硬编码「POST 就发 JSON」的分支）。这导致每接一家新公司
-- 都要改一次后端代码，与「Agent 自行摸索并沉淀路径」的目标背道而驰。
--
-- 补齐请求侧字段后，执行器只需原样复现探索阶段观测到的真实请求；
-- 补齐健康度字段后，Recipe 失效可被自动识别并触发重新探索（Reflection）。

ALTER TABLE site_recipes
    -- 请求侧：POST 型列表接口的可复现关键。
    ADD COLUMN IF NOT EXISTS request_body         TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS request_content_type TEXT NOT NULL DEFAULT '',
    -- 仅存放非凭证类语义头部（渠道 / 语言等），由应用层双层白名单过滤。
    ADD COLUMN IF NOT EXISTS request_headers      JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- 关键词是放在请求体里还是 URL query 里。
    ADD COLUMN IF NOT EXISTS keyword_in_body      BOOLEAN NOT NULL DEFAULT FALSE,

    -- 健康度：驱动「验证通过才保存」与「连续失败自动重探」。
    ADD COLUMN IF NOT EXISTS verify_status        VARCHAR(20) NOT NULL DEFAULT 'unverified',
    ADD COLUMN IF NOT EXISTS verified_at          TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS verified_jobs        INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error           TEXT NOT NULL DEFAULT '';

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_verify_status;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_verify_status
    CHECK (verify_status IN ('unverified', 'verified', 'invalid'));

-- 已存在的预置 Recipe（腾讯 / 字节）走浏览器策略且已人工验证过，
-- 标记为 verified，避免被自愈逻辑误判为待探索。
UPDATE site_recipes
   SET verify_status = 'verified'
 WHERE source = 'preset'
   AND verify_status = 'unverified';

-- 按验证状态查询（自愈逻辑会筛出 invalid 的站点）。
CREATE INDEX IF NOT EXISTS idx_site_recipes_verify_status
    ON site_recipes (verify_status);
