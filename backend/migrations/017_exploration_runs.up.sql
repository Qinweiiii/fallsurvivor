-- Exploration Run：保存站点探索的完整结果，尤其是失败轨迹。
-- Recipe 只有验证成功后才落库；探索失败同样需要可查询证据，避免下次从最终错误反推。
CREATE TABLE IF NOT EXISTS exploration_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    site_key    TEXT NOT NULL,
    keyword     TEXT NOT NULL DEFAULT '',
    entry_url   TEXT NOT NULL DEFAULT '',
    status      VARCHAR(20) NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    trace       JSONB NOT NULL DEFAULT '[]'::jsonb,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('success', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_exploration_runs_site_key_created
    ON exploration_runs(site_key, created_at DESC);
