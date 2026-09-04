-- Exploration Playbook：跨站点的探索经验记忆。
--
-- site_recipes 记录“某个站点怎么采”，本表记录“探索新站点时优先尝试什么”。

CREATE TABLE IF NOT EXISTS exploration_playbook (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tactic_key   TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    priority     INTEGER NOT NULL DEFAULT 100,
    hit_count    INTEGER NOT NULL DEFAULT 0,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_exploration_playbook_enabled
    ON exploration_playbook(enabled);

CREATE INDEX IF NOT EXISTS idx_exploration_playbook_rank
    ON exploration_playbook(priority ASC, hit_count DESC);
