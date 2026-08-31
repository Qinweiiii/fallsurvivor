-- ============================================================
-- 秋招 OS 初始化迁移
-- 设计依据：docs/03_数据库设计.md
-- 状态枚举取 03 与 06 两份文档的并集，并用 CHECK 约束固化。
-- ============================================================

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ---------- 触发器：自动维护 updated_at ----------
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ============================================================
-- 1. 用户
-- ============================================================
CREATE TABLE user_profiles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       VARCHAR(100) NOT NULL DEFAULT '',
    email      VARCHAR(255) NOT NULL DEFAULT '',
    phone      VARCHAR(50)  NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TRIGGER trg_user_profiles_updated
    BEFORE UPDATE ON user_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 2. 求职画像
-- ============================================================
CREATE TABLE job_profiles (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    -- 均为 JSONB 数组，preferred_locations / preferred_languages 的数组顺序即优先级
    target_roles         JSONB NOT NULL DEFAULT '[]'::jsonb,
    preferred_languages  JSONB NOT NULL DEFAULT '[]'::jsonb,
    preferred_locations  JSONB NOT NULL DEFAULT '[]'::jsonb,
    company_preferences  JSONB NOT NULL DEFAULT '[]'::jsonb,
    target_industries    JSONB NOT NULL DEFAULT '[]'::jsonb,
    graduation_year      INTEGER NOT NULL DEFAULT 2027,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_job_profiles_user UNIQUE (user_id)
);

CREATE TRIGGER trg_job_profiles_updated
    BEFORE UPDATE ON job_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 3. 简历
-- ============================================================
CREATE TABLE resumes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    file_name       VARCHAR(255) NOT NULL,
    -- 服务端生成的 UUID 文件名（相对 STORAGE_DIR），绝不使用用户上传的原始名
    file_path       VARCHAR(500) NOT NULL,
    file_size       BIGINT NOT NULL DEFAULT 0,
    mime_type       VARCHAR(100) NOT NULL DEFAULT '',
    raw_text        TEXT NOT NULL DEFAULT '',
    structured_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    parse_status    VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    parse_error     TEXT NOT NULL DEFAULT '',
    is_current      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_resumes_parse_status
        CHECK (parse_status IN ('PENDING','RUNNING','COMPLETED','FAILED'))
);

CREATE INDEX idx_resumes_user ON resumes(user_id);
-- 每个用户最多只有一份「当前简历」
CREATE UNIQUE INDEX uq_resumes_current ON resumes(user_id) WHERE is_current;

CREATE TRIGGER trg_resumes_updated
    BEFORE UPDATE ON resumes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 4. 岗位
-- ============================================================
CREATE TABLE jobs (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 以下文本字段用 TEXT（不限长）：
    -- 腾讯校招 department 会拼成「部门(BG) | 部门(BG) | ...」长串（实测 200+ 字符），
    -- 用 VARCHAR 会导致插入失败（SQLSTATE 22001）。
    company_name          TEXT NOT NULL,
    department            TEXT NOT NULL DEFAULT '',
    business              TEXT NOT NULL DEFAULT '',
    title                 TEXT NOT NULL,
    -- 主展示地点；多地点完整列表存 locations
    location              TEXT NOT NULL DEFAULT '',
    locations             JSONB NOT NULL DEFAULT '[]'::jsonb,
    job_type              VARCHAR(50)  NOT NULL DEFAULT '',
    graduation_year       INTEGER,
    description           TEXT NOT NULL DEFAULT '',
    responsibilities      JSONB NOT NULL DEFAULT '[]'::jsonb,
    requirements          JSONB NOT NULL DEFAULT '[]'::jsonb,
    language_requirements JSONB NOT NULL DEFAULT '[]'::jsonb,
    technical_stack       JSONB NOT NULL DEFAULT '[]'::jsonb,
    source_url            TEXT NOT NULL DEFAULT '',
    official_url          TEXT NOT NULL DEFAULT '',
    -- 用于去重的规范化 URL（去 query/fragment/尾斜杠、小写 host）
    normalized_url        TEXT NOT NULL DEFAULT '',
    -- company+title+location(+urlID) 归一化后的指纹，第三层去重用
    -- 含 URLID 后可能较长，故用 TEXT。
    dedup_fingerprint     TEXT NOT NULL DEFAULT '',
    source_type           VARCHAR(30) NOT NULL DEFAULT 'TAVILY',
    published_at          TIMESTAMPTZ,
    deadline              TIMESTAMPTZ,
    crawled_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    match_score           INTEGER NOT NULL DEFAULT 0,
    match_analysis        JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- 岗位在总览页的处理状态
    status                VARCHAR(30) NOT NULL DEFAULT 'NEW',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_jobs_status
        CHECK (status IN ('NEW','IN_CART','PREPARING','SUBMITTED','CLOSED')),
    CONSTRAINT chk_jobs_source_type
        CHECK (source_type IN ('OFFICIAL','BOSS','TAVILY','MANUAL')),
    CONSTRAINT chk_jobs_match_score
        CHECK (match_score BETWEEN 0 AND 100)
);

-- 文档第 15 节要求的重点索引
CREATE INDEX idx_jobs_company     ON jobs(company_name);
CREATE INDEX idx_jobs_title       ON jobs(title);
CREATE INDEX idx_jobs_location    ON jobs(location);
CREATE INDEX idx_jobs_match_score ON jobs(match_score DESC);
CREATE INDEX idx_jobs_created_at  ON jobs(created_at DESC);
CREATE INDEX idx_jobs_status      ON jobs(status);
CREATE INDEX idx_jobs_deadline    ON jobs(deadline) WHERE deadline IS NOT NULL;
-- 去重依赖的唯一索引（第一、二、三层）
CREATE UNIQUE INDEX uq_jobs_official_url  ON jobs(official_url)    WHERE official_url <> '';
CREATE UNIQUE INDEX uq_jobs_normalized_url ON jobs(normalized_url) WHERE normalized_url <> '';
CREATE UNIQUE INDEX uq_jobs_fingerprint   ON jobs(dedup_fingerprint) WHERE dedup_fingerprint <> '';
-- 技术栈筛选
CREATE INDEX idx_jobs_tech_stack ON jobs USING GIN (technical_stack);

CREATE TRIGGER trg_jobs_updated
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 5. 岗位来源（同一岗位可来自多个来源）
-- ============================================================
CREATE TABLE job_sources (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id        UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    source_type   VARCHAR(30)  NOT NULL,
    source_name   TEXT NOT NULL DEFAULT '',
    url           TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    is_valid      BOOLEAN NOT NULL DEFAULT true,
    CONSTRAINT chk_job_sources_type
        CHECK (source_type IN ('OFFICIAL','BOSS','TAVILY','MANUAL')),
    CONSTRAINT uq_job_sources_job_url UNIQUE (job_id, url)
);

CREATE INDEX idx_job_sources_job ON job_sources(job_id);

-- ============================================================
-- 6. 搜索任务
-- ============================================================
CREATE TABLE search_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    status          VARCHAR(30) NOT NULL DEFAULT 'PENDING',
    queries         JSONB NOT NULL DEFAULT '[]'::jsonb,
    query_count     INTEGER NOT NULL DEFAULT 0,
    found_count     INTEGER NOT NULL DEFAULT 0,
    new_count       INTEGER NOT NULL DEFAULT 0,
    duplicate_count INTEGER NOT NULL DEFAULT 0,
    high_match_count INTEGER NOT NULL DEFAULT 0,
    -- 每个来源的成功/失败明细，单来源失败不影响整体
    source_results  JSONB NOT NULL DEFAULT '[]'::jsonb,
    warnings        JSONB NOT NULL DEFAULT '[]'::jsonb,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_search_tasks_status
        CHECK (status IN ('PENDING','RUNNING','COMPLETED','COMPLETED_WITH_WARNING','FAILED'))
);

CREATE INDEX idx_search_tasks_user_created ON search_tasks(user_id, created_at DESC);

CREATE TRIGGER trg_search_tasks_updated
    BEFORE UPDATE ON search_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 7. 岗位车
-- ============================================================
CREATE TABLE job_carts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    job_id     UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_job_carts_user_job UNIQUE (user_id, job_id)
);

CREATE INDEX idx_job_carts_user ON job_carts(user_id, created_at DESC);

-- ============================================================
-- 8. 投递任务
-- ============================================================
CREATE TABLE applications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    job_id          UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    status          VARCHAR(30) NOT NULL DEFAULT 'PREPARING',
    progress        INTEGER NOT NULL DEFAULT 0,
    application_url VARCHAR(1000) NOT NULL DEFAULT '',
    note            TEXT NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ,
    last_action_at  TIMESTAMPTZ,
    submitted_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 03 与 06 两份文档状态并集
    CONSTRAINT chk_applications_status CHECK (status IN (
        'PREPARING','LOGIN_REQUIRED','FORM_ANALYZING','FORM_FILLING',
        'WAITING_USER','READY_TO_SUBMIT','SUBMITTED',
        'WRITTEN_TEST','INTERVIEW_1','INTERVIEW_2','HR_INTERVIEW',
        'OFFER','REJECTED','WITHDRAWN','FAILED','BLOCKED'
    )),
    CONSTRAINT chk_applications_progress CHECK (progress BETWEEN 0 AND 100),
    -- 同一岗位只允许一条投递任务
    CONSTRAINT uq_applications_user_job UNIQUE (user_id, job_id)
);

CREATE INDEX idx_applications_status     ON applications(status);
CREATE INDEX idx_applications_created_at ON applications(created_at DESC);
CREATE INDEX idx_applications_user       ON applications(user_id, created_at DESC);

CREATE TRIGGER trg_applications_updated
    BEFORE UPDATE ON applications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 9. 可复用申请信息
-- 注意：仅存放普通求职信息。身份证/银行卡/密码/验证码一律不入库。
-- ============================================================
CREATE TABLE application_profiles (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES user_profiles(id) ON DELETE CASCADE,
    profile_data JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_application_profiles_user UNIQUE (user_id)
);

CREATE TRIGGER trg_application_profiles_updated
    BEFORE UPDATE ON application_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 10. 浏览器任务
-- ============================================================
CREATE TABLE browser_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    status          VARCHAR(30) NOT NULL DEFAULT 'PENDING',
    site_key        VARCHAR(50) NOT NULL DEFAULT 'generic',
    current_url     VARCHAR(1000) NOT NULL DEFAULT '',
    step            VARCHAR(100) NOT NULL DEFAULT '',
    field_total     INTEGER NOT NULL DEFAULT 0,
    field_filled    INTEGER NOT NULL DEFAULT 0,
    field_skipped   INTEGER NOT NULL DEFAULT 0,
    -- 需要用户手工处理的字段标签列表（只存 label，不存值）
    pending_fields  JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_message   TEXT NOT NULL DEFAULT '',
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_browser_tasks_status CHECK (status IN (
        'PENDING','RUNNING','WAITING_USER','PAUSED','COMPLETED','FAILED','BLOCKED'
    ))
);

CREATE INDEX idx_browser_tasks_application ON browser_tasks(application_id, created_at DESC);
CREATE INDEX idx_browser_tasks_status      ON browser_tasks(status);

CREATE TRIGGER trg_browser_tasks_updated
    BEFORE UPDATE ON browser_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 11. 表单字段分析结果
-- 只保存字段元信息与映射来源，绝不保存字段实际值。
-- ============================================================
CREATE TABLE application_fields (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    field_name     VARCHAR(200) NOT NULL DEFAULT '',
    field_label    VARCHAR(300) NOT NULL DEFAULT '',
    field_type     VARCHAR(50)  NOT NULL DEFAULT '',
    -- 形如 education.degree；敏感字段为 NULL
    mapped_source  VARCHAR(200),
    confidence     NUMERIC(4,3) NOT NULL DEFAULT 0,
    is_sensitive   BOOLEAN NOT NULL DEFAULT false,
    is_filled      BOOLEAN NOT NULL DEFAULT false,
    is_required    BOOLEAN NOT NULL DEFAULT false,
    skip_reason    VARCHAR(100) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_application_fields_confidence CHECK (confidence BETWEEN 0 AND 1),
    -- 强约束：敏感字段永远不允许被标记为已填写
    CONSTRAINT chk_application_fields_sensitive_never_filled
        CHECK (NOT (is_sensitive AND is_filled)),
    -- 强约束：敏感字段不允许有映射来源
    CONSTRAINT chk_application_fields_sensitive_no_mapping
        CHECK (NOT (is_sensitive AND mapped_source IS NOT NULL))
);

CREATE INDEX idx_application_fields_app ON application_fields(application_id);

CREATE TRIGGER trg_application_fields_updated
    BEFORE UPDATE ON application_fields
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================
-- 12. 投递事件流
-- ============================================================
CREATE TABLE application_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID REFERENCES applications(id) ON DELETE CASCADE,
    job_id         UUID REFERENCES jobs(id) ON DELETE SET NULL,
    event_type     VARCHAR(50) NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    from_status    VARCHAR(30) NOT NULL DEFAULT '',
    to_status      VARCHAR(30) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_application_events_app     ON application_events(application_id, created_at DESC);
CREATE INDEX idx_application_events_created ON application_events(created_at DESC);

-- ============================================================
-- 13. 面试（第一版可选）
-- ============================================================
CREATE TABLE interviews (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    round          VARCHAR(50) NOT NULL,
    scheduled_at   TIMESTAMPTZ,
    format         VARCHAR(50) NOT NULL DEFAULT '',
    status         VARCHAR(30) NOT NULL DEFAULT 'SCHEDULED',
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_interviews_status
        CHECK (status IN ('SCHEDULED','DONE','PASSED','FAILED','CANCELLED'))
);

CREATE INDEX idx_interviews_application ON interviews(application_id);
CREATE INDEX idx_interviews_scheduled   ON interviews(scheduled_at) WHERE scheduled_at IS NOT NULL;

CREATE TRIGGER trg_interviews_updated
    BEFORE UPDATE ON interviews
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
