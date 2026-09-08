-- 记录 JD 异步补全状态，避免只能从 match_analysis 里猜当前进度。

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS enrichment_status VARCHAR(30) NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS enrichment_error TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS enrichment_updated_at TIMESTAMPTZ;

UPDATE jobs
SET enrichment_status = CASE
        WHEN desc_quality IS NULL OR desc_quality <> 'full' THEN 'pending_enrichment'
        ELSE 'enriched'
    END,
    enrichment_updated_at = COALESCE(updated_at, now())
WHERE enrichment_status = 'none';

COMMENT ON COLUMN jobs.enrichment_status IS 'JD补全状态: none/pending_enrichment/enriching/enriched/enrich_failed';
COMMENT ON COLUMN jobs.enrichment_error IS '最近一次JD补全失败原因';
COMMENT ON COLUMN jobs.enrichment_updated_at IS '最近一次JD补全状态更新时间';
