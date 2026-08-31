-- 添加岗位描述质量标志字段
-- 用于区分完整 JD / 搜索摘要 / 空数据（网页标题被过滤）

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS desc_quality VARCHAR(10) NOT NULL DEFAULT 'full';

COMMENT ON COLUMN jobs.desc_quality IS '岗位描述数据质量: full=完整JD, snippet=搜索摘要, empty=无有效内容';
