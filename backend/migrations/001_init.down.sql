-- 回滚 001_init：按依赖倒序删除
DROP TABLE IF EXISTS interviews;
DROP TABLE IF EXISTS application_events;
DROP TABLE IF EXISTS application_fields;
DROP TABLE IF EXISTS browser_tasks;
DROP TABLE IF EXISTS application_profiles;
DROP TABLE IF EXISTS applications;
DROP TABLE IF EXISTS job_carts;
DROP TABLE IF EXISTS search_tasks;
DROP TABLE IF EXISTS job_sources;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS resumes;
DROP TABLE IF EXISTS job_profiles;
DROP TABLE IF EXISTS user_profiles;
DROP FUNCTION IF EXISTS set_updated_at();
