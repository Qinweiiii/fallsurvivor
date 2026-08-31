-- Site Recipe Registry：把「已知招聘站点怎么采」固化为数据，而非硬编码分支。
--
-- 设计原则（避免过度设计）：
--   * 第一版只表达当前已验证站点需要的字段，不做通用采集 DSL；
--   * 腾讯 / 字节已验证的浏览器抽取逻辑不推倒重写，
--     而是以「adapter_key」的形式挂到 Recipe 上，由 Worker 侧复用；
--   * Recipe 只是描述「用哪个适配器 + 限制是什么」，真正的执行逻辑仍在代码里。
--     这样新增一家公司 = 新增一条 Recipe 记录，不改 Go 代码。

CREATE TABLE IF NOT EXISTS site_recipes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- 站点标识：与 Worker 侧 adapter、browser 持久登录态目录一一对应。
    site_key            TEXT NOT NULL UNIQUE,
    -- 展示用公司名，落库时写入 jobs.company_name 的权威值。
    company_name        TEXT NOT NULL,
    -- 站点域名，用于判断某个 URL 是否属于本站（Fast Path 命中判定）。
    domain              TEXT NOT NULL,
    -- 校招入口 URL（搜索起点）。
    campus_url          TEXT NOT NULL DEFAULT '',

    -- 采集策略：browser（浏览器抽取）/ api（直接调接口）/ url_template（按 ID 拼 URL）。
    strategy_type       VARCHAR(30) NOT NULL DEFAULT 'browser',
    -- browser 策略下使用的 Worker adapter key（如 tencent / bytedance）。
    adapter_key         TEXT NOT NULL DEFAULT '',

    enabled             BOOLEAN NOT NULL DEFAULT true,

    -- ---- 成本控制 ----
    -- 单次搜索最多返回多少条岗位（对应 SearchQuery.MaxResultsPerQuery）。
    max_jobs_per_search INTEGER NOT NULL DEFAULT 20,
    -- 最多对多少条岗位抓取详情正文（LLM 与网络开销的主要来源）。
    max_detail_fetches  INTEGER NOT NULL DEFAULT 8,

    -- 备注：记录站点特征、已知坑、登录要求等，便于人工维护。
    notes               TEXT NOT NULL DEFAULT '',

    -- 来源：preset（内置预置）/ manual（人工配置）/ exploration（探索产物）。
    source              VARCHAR(30) NOT NULL DEFAULT 'manual',
    -- 版本号：探索重新生成或人工修订时递增。
    version             INTEGER NOT NULL DEFAULT 1,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_site_recipes_domain  ON site_recipes(domain);
CREATE INDEX IF NOT EXISTS idx_site_recipes_enabled ON site_recipes(enabled);

-- 约束：strategy_type 取值白名单。
ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_strategy_type;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_strategy_type
    CHECK (strategy_type IN ('browser','api','url_template'));

ALTER TABLE site_recipes DROP CONSTRAINT IF EXISTS chk_site_recipes_source;
ALTER TABLE site_recipes ADD CONSTRAINT chk_site_recipes_source
    CHECK (source IN ('preset','manual','exploration'));

-- Recipe 执行记录：用于观测与后续 Reflection（Recipe 失效自愈）的输入。
CREATE TABLE IF NOT EXISTS recipe_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 不加强制外键：Recipe 被删除后仍希望保留历史运行记录用于排查。
    recipe_id       UUID,
    site_key        TEXT NOT NULL,
    keyword         TEXT NOT NULL DEFAULT '',

    status          VARCHAR(20) NOT NULL,   -- success / empty / failed
    jobs_found      INTEGER NOT NULL DEFAULT 0,
    jobs_ingested   INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT NOT NULL DEFAULT '',
    duration_ms     INTEGER NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_recipe_runs_site_key  ON recipe_runs(site_key);
CREATE INDEX IF NOT EXISTS idx_recipe_runs_created   ON recipe_runs(created_at DESC);

ALTER TABLE recipe_runs DROP CONSTRAINT IF EXISTS chk_recipe_runs_status;
ALTER TABLE recipe_runs ADD CONSTRAINT chk_recipe_runs_status
    CHECK (status IN ('success','empty','failed'));
