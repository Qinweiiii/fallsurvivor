/**
 * 与后端 API 对齐的类型定义。
 * 字段命名保持 snake_case，避免转换层带来的偏差。
 */

export interface ApiEnvelope<T> {
  code: number;
  message: string;
  data: T;
}

export interface Pagination {
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
}

export interface Page<T> {
  items: T[];
  pagination: Pagination;
}

// ---------------- 求职画像 ----------------

export interface JobProfile {
  id?: string;
  user_id?: string;
  target_roles: string[];
  preferred_languages: string[];
  preferred_locations: string[];
  company_preferences: string[];
  target_industries: string[];
  graduation_year: number;
}

// ---------------- 岗位 ----------------

export type JobStatus = 'NEW' | 'IN_CART' | 'PREPARING' | 'SUBMITTED' | 'CLOSED';
export type SourceType = 'OFFICIAL' | 'BOSS' | 'TAVILY' | 'MANUAL';

export interface MatchAnalysis {
  score?: number;
  rule_score?: number;
  llm_score?: number;
  reasons?: string[];
  risks?: string[];
  summary?: string;
  analyzer?: string;
  analyzed_at?: string;
}

export interface Job {
  id: string;
  company_name: string;
  department: string;
  business: string;
  title: string;
  location: string;
  locations: string[];
  job_type: string;
  graduation_year: number | null;
  description: string;
  desc_quality: 'full' | 'snippet' | 'empty';
  responsibilities: string[];
  requirements: string[];
  language_requirements: string[];
  technical_stack: string[];
  source_url: string;
  official_url: string;
  source_type: SourceType;
  published_at: string | null;
  deadline: string | null;
  crawled_at: string;
  match_score: number;
  match_analysis: MatchAnalysis;
  status: JobStatus;
  created_at: string;
  updated_at: string;
  in_cart?: boolean;
}

export interface JobSource {
  id: string;
  job_id: string;
  source_type: SourceType;
  source_name: string;
  url: string;
  first_seen_at: string;
  last_seen_at: string;
  is_valid: boolean;
}

export interface JobDetail extends Job {
  sources: JobSource[];
  in_cart: boolean;
  application_id: string | null;
}

export interface CartItem extends Job {
  added_at: string;
}

export interface FilterOptions {
  companies: string[];
  locations: string[];
  statuses: JobStatus[];
  sources: SourceType[];
  languages: string[];
}

// ---------------- 搜索任务 ----------------

export type SearchTaskStatus =
  | 'PENDING'
  | 'RUNNING'
  | 'COMPLETED'
  | 'COMPLETED_WITH_WARNING'
  | 'FAILED'
  | 'TERMINATED';

export interface SourceResult {
  source_type: string;
  source_name: string;
  success: boolean;
  count: number;
  error?: string;
  duration_ms: number;
}

export interface SearchTask {
  id: string;
  status: SearchTaskStatus;
  queries: string[];
  query_count: number;
  found_count: number;
  new_count: number;
  duplicate_count: number;
  high_match_count: number;
  source_results: { sources?: SourceResult[] };
  warnings: string[];
  started_at: string | null;
  finished_at: string | null;
  error_message: string;
  created_at: string;
}

// ---------------- 投递任务 ----------------

export type ApplicationStatus =
  | 'PREPARING'
  | 'LOGIN_REQUIRED'
  | 'FORM_ANALYZING'
  | 'FORM_FILLING'
  | 'WAITING_USER'
  | 'READY_TO_SUBMIT'
  | 'SUBMITTED'
  | 'WRITTEN_TEST'
  | 'INTERVIEW_1'
  | 'INTERVIEW_2'
  | 'HR_INTERVIEW'
  | 'OFFER'
  | 'REJECTED'
  | 'WITHDRAWN'
  | 'FAILED'
  | 'BLOCKED';

export interface Application {
  id: string;
  job_id: string;
  status: ApplicationStatus;
  progress: number;
  application_url: string;
  note: string;
  started_at: string | null;
  last_action_at: string | null;
  submitted_at: string | null;
  created_at: string;
  updated_at: string;
  job?: Job;
}

export interface ApplicationField {
  id: string;
  field_name: string;
  field_label: string;
  field_type: string;
  mapped_source: string | null;
  confidence: number;
  is_sensitive: boolean;
  is_filled: boolean;
  is_required: boolean;
  skip_reason: string;
}

export interface ApplicationEvent {
  id: string;
  event_type: string;
  description: string;
  from_status: string;
  to_status: string;
  created_at: string;
}

export type BrowserTaskStatus =
  | 'PENDING'
  | 'RUNNING'
  | 'WAITING_USER'
  | 'PAUSED'
  | 'COMPLETED'
  | 'FAILED'
  | 'BLOCKED';

export interface BrowserTask {
  id: string;
  application_id: string;
  status: BrowserTaskStatus;
  site_key: string;
  current_url: string;
  step: string;
  field_total: number;
  field_filled: number;
  field_skipped: number;
  pending_fields: string[];
  error_message: string;
}

export interface FieldSummary {
  total: number;
  filled: number;
  skipped: number;
  sensitive: number;
  pending_labels: string[];
}

export interface ApplicationDetail {
  application: Application;
  job: Job | null;
  browser_task: BrowserTask | null;
  fields: ApplicationField[];
  events: ApplicationEvent[];
  allowed_next: ApplicationStatus[];
  field_summary: FieldSummary;
}

export interface StatusOption {
  value: ApplicationStatus;
  label: string;
}

export interface BrowserStartResult {
  browser_task: BrowserTask;
  needs_login: boolean;
  message: string;
}

// ---------------- Dashboard ----------------

export interface DashboardStats {
  jobs: { total: number; new: number };
  cart: number;
  applications: number;
  pending: number;
  submitted: number;
  written_test: number;
  interview: number;
  offer: number;
  rejected: number;
}

export interface DeadlineItem {
  job_id: string;
  company_name: string;
  title: string;
  deadline: string;
  days_left: number;
}

export interface RecentEvent {
  id: string;
  event_type: string;
  description: string;
  company_name: string;
  title: string;
  created_at: string;
}

export interface DashboardData {
  stats: DashboardStats;
  recent_high_match: Job[];
  upcoming_deadlines: DeadlineItem[];
  recent_events: RecentEvent[];
  latest_search_task: SearchTask | null;
}

// ---------------- 简历与申请信息 ----------------

export interface Resume {
  id: string;
  file_name: string;
  file_size: number;
  mime_type: string;
  parse_status: 'PENDING' | 'RUNNING' | 'COMPLETED' | 'FAILED';
  parse_error: string;
  is_current: boolean;
  structured_data: Record<string, unknown>;
  raw_text?: string;
  created_at: string;
}

export interface ApplicationProfileData {
  basic: {
    name: string;
    phone: string;
    email: string;
    gender: string;
    hometown: string;
    homepage: string;
    self_intro: string;
  };
  education: {
    school: string;
    major: string;
    degree: string;
    start_date: string;
    graduation_date: string;
    gpa: string;
  };
  preference: { city: string; role: string };
  skills: { summary: string; languages: string };
  experience: { projects: string; internships: string; awards: string };
}

// ---------------- 站点 Recipe（数据驱动采集） ----------------

/** 采集策略。第一版仅 browser 已实现。 */
export type SiteStrategy = 'browser' | 'api' | 'url_template';

/** Recipe 来源。 */
export type SiteRecipeSource = 'preset' | 'manual' | 'exploration';

/** 站点采集配置：描述「某公司校招站点怎么采」。 */
export interface SiteRecipe {
  id: string;
  site_key: string;
  company_name: string;
  domain: string;
  campus_url: string;
  strategy_type: SiteStrategy;
  /** browser 策略下对应 Worker 侧的站点适配器 key。 */
  adapter_key: string;
  enabled: boolean;
  /** 单次搜索最多返回岗位数。 */
  max_jobs_per_search: number;
  /** 最多对多少条岗位抓取详情正文（0 = 不抓详情，用抽取阶段的 JD）。 */
  max_detail_fetches: number;
  notes: string;
  source: SiteRecipeSource;
  version: number;
  created_at: string;
  updated_at: string;
}

/** Recipe 执行状态。 */
export type SiteRunStatus = 'success' | 'empty' | 'failed';

/** 一次 Recipe 执行记录。 */
export interface SiteRecipeRun {
  id: string;
  recipe_id: string | null;
  site_key: string;
  keyword: string;
  status: SiteRunStatus;
  jobs_found: number;
  jobs_ingested: number;
  error_message: string;
  duration_ms: number;
  created_at: string;
}
