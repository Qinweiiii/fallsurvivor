-- 临时停用岗位去重唯一索引（主链路优先）。
--
-- 背景：uq_jobs_normalized_url 与 uq_jobs_fingerprint 会在数据库层面拦截插入。
-- 即便应用层去重逻辑已停用，只要这两个唯一索引还在，
-- 同一次爬取中多条记录的插入仍会被数据库拒绝（第二次起全部失败），
-- 表现为「明明爬到了、LLM 也分析完了，但数据库里就是没有」。
--
-- 由于应用层已暂时关闭跨批次与批内去重，这里同步放开数据库约束，
-- 让爬到的岗位都能先入库显示。去重能力后续需要时再以 migration 恢复。
--
-- 保留 uq_jobs_official_url：官方接口 URL 天然唯一，且不影响浏览器爬取链路。

DROP INDEX IF EXISTS uq_jobs_normalized_url;
DROP INDEX IF EXISTS uq_jobs_fingerprint;

-- 降级为普通索引，保留查询性能（去重用不到，但仍可按这些字段检索）。
CREATE INDEX IF NOT EXISTS idx_jobs_normalized_url ON jobs(normalized_url) WHERE normalized_url <> '';
CREATE INDEX IF NOT EXISTS idx_jobs_fingerprint    ON jobs(dedup_fingerprint) WHERE dedup_fingerprint <> '';
