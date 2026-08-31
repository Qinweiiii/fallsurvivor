-- 回滚：恢复仅包含原始状态的检查约束。

ALTER TABLE search_tasks
    DROP CONSTRAINT IF EXISTS chk_search_tasks_status;

ALTER TABLE search_tasks
    ADD CONSTRAINT chk_search_tasks_status
    CHECK (status IN (
        'PENDING',
        'RUNNING',
        'COMPLETED',
        'COMPLETED_WITH_WARNING',
        'FAILED'
    ));
