-- 允许搜索任务状态包含 TERMINATED。
-- 服务关闭/重启时会将进行中的任务（PENDING/RUNNING）标记为 TERMINATED，
-- 原检查约束未包含该值，导致 UPDATE 被拒绝、任务永久卡在 RUNNING。

ALTER TABLE search_tasks
    DROP CONSTRAINT IF EXISTS chk_search_tasks_status;

ALTER TABLE search_tasks
    ADD CONSTRAINT chk_search_tasks_status
    CHECK (status IN (
        'PENDING',
        'RUNNING',
        'COMPLETED',
        'COMPLETED_WITH_WARNING',
        'FAILED',
        'TERMINATED'
    ));
