package model

import "time"

// 岗位在总览页的处理状态。
const (
	JobStatusNew       = "NEW"       // 未处理
	JobStatusInCart    = "IN_CART"   // 已在岗位车
	JobStatusPreparing = "PREPARING" // 已准备投递
	JobStatusSubmitted = "SUBMITTED" // 已投递
	JobStatusClosed    = "CLOSED"    // 已结束
)

// AllJobStatuses 用于参数校验。
var AllJobStatuses = []string{
	JobStatusNew, JobStatusInCart, JobStatusPreparing, JobStatusSubmitted, JobStatusClosed,
}

// 岗位来源类型。
const (
	SourceOfficial = "OFFICIAL"
	SourceBoss     = "BOSS"
	SourceTavily   = "TAVILY"
	SourceManual   = "MANUAL"
	SourceBrowser  = "BROWSER"
)

// AllSourceTypes 用于参数校验。
var AllSourceTypes = []string{SourceOfficial, SourceBoss, SourceTavily, SourceManual, SourceBrowser}

// MatchAnalysis 是匹配分析结果，结构固定，便于前端直接渲染。
type MatchAnalysis struct {
	Score      int      `json:"score"`
	RuleScore  int      `json:"rule_score"`
	LLMScore   int      `json:"llm_score"`
	Reasons    []string `json:"reasons"`
	Risks      []string `json:"risks"`
	Summary    string   `json:"summary"`
	Analyzer   string   `json:"analyzer"` // rule | rule+llm
	AnalyzedAt string   `json:"analyzed_at"`
}

// Job 是标准化后的岗位。
type Job struct {
	ID                   ID              `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	CompanyName          string          `json:"company_name" gorm:"column:company_name"`
	Department           string          `json:"department" gorm:"column:department"`
	Business             string          `json:"business" gorm:"column:business"`
	Title                string          `json:"title" gorm:"column:title"`
	Location             string          `json:"location" gorm:"column:location"`
	Locations            JSONStringArray `json:"locations" gorm:"column:locations;type:jsonb"`
	JobType              string          `json:"job_type" gorm:"column:job_type"`
	GraduationYear       *int            `json:"graduation_year" gorm:"column:graduation_year"`
	Description          string          `json:"description" gorm:"column:description"`
	DescQuality          string          `json:"desc_quality" gorm:"column:desc_quality;default:full"`
	Responsibilities     JSONStringArray `json:"responsibilities" gorm:"column:responsibilities;type:jsonb"`
	Requirements         JSONStringArray `json:"requirements" gorm:"column:requirements;type:jsonb"`
	LanguageRequirements JSONStringArray `json:"language_requirements" gorm:"column:language_requirements;type:jsonb"`
	TechnicalStack       JSONStringArray `json:"technical_stack" gorm:"column:technical_stack;type:jsonb"`
	SourceURL            string          `json:"source_url" gorm:"column:source_url"`
	OfficialURL          string          `json:"official_url" gorm:"column:official_url"`
	NormalizedURL        string          `json:"-" gorm:"column:normalized_url"`
	DedupFingerprint     string          `json:"-" gorm:"column:dedup_fingerprint"`
	SourceType           string          `json:"source_type" gorm:"column:source_type"`
	PublishedAt          *time.Time      `json:"published_at" gorm:"column:published_at"`
	Deadline             *time.Time      `json:"deadline" gorm:"column:deadline"`
	CrawledAt            time.Time       `json:"crawled_at" gorm:"column:crawled_at"`
	MatchScore           int             `json:"match_score" gorm:"column:match_score"`
	MatchAnalysis        JSONMap         `json:"match_analysis" gorm:"column:match_analysis;type:jsonb"`
	Status               string          `json:"status" gorm:"column:status"`
	Timestamps
}

// TableName 指定表名。
func (Job) TableName() string { return "jobs" }

// JobSource 记录岗位在各来源出现的情况。
type JobSource struct {
	ID          ID        `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	JobID       ID        `json:"job_id" gorm:"column:job_id;type:uuid"`
	SourceType  string    `json:"source_type" gorm:"column:source_type"`
	SourceName  string    `json:"source_name" gorm:"column:source_name"`
	URL         string    `json:"url" gorm:"column:url"`
	FirstSeenAt time.Time `json:"first_seen_at" gorm:"column:first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at" gorm:"column:last_seen_at"`
	IsValid     bool      `json:"is_valid" gorm:"column:is_valid"`
}

// TableName 指定表名。
func (JobSource) TableName() string { return "job_sources" }

// 搜索任务状态。
const (
	SearchTaskPending       = "PENDING"
	SearchTaskRunning       = "RUNNING"
	SearchTaskCompleted     = "COMPLETED"
	SearchTaskCompletedWarn = "COMPLETED_WITH_WARNING"
	SearchTaskFailed        = "FAILED"
	// SearchTaskTerminated 表示任务被用户取消或服务关闭/重启而终止。
	SearchTaskTerminated = "TERMINATED"
)

// SearchTask 是一次「获取岗位」任务。
type SearchTask struct {
	ID             ID              `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID         ID              `json:"user_id" gorm:"column:user_id;type:uuid"`
	Status         string          `json:"status" gorm:"column:status"`
	Queries        JSONStringArray `json:"queries" gorm:"column:queries;type:jsonb"`
	QueryCount     int             `json:"query_count" gorm:"column:query_count"`
	FoundCount     int             `json:"found_count" gorm:"column:found_count"`
	NewCount       int             `json:"new_count" gorm:"column:new_count"`
	DuplicateCount int             `json:"duplicate_count" gorm:"column:duplicate_count"`
	HighMatchCount int             `json:"high_match_count" gorm:"column:high_match_count"`
	SourceResults  JSONMap         `json:"source_results" gorm:"column:source_results;type:jsonb;serializer:json"`
	Warnings       JSONStringArray `json:"warnings" gorm:"column:warnings;type:jsonb"`
	StartedAt      *time.Time      `json:"started_at" gorm:"column:started_at"`
	FinishedAt     *time.Time      `json:"finished_at" gorm:"column:finished_at"`
	ErrorMessage   string          `json:"error_message" gorm:"column:error_message"`
	Timestamps
}

// TableName 指定表名。
func (SearchTask) TableName() string { return "search_tasks" }

// IsTerminal 报告任务是否已结束。
func (t SearchTask) IsTerminal() bool {
	switch t.Status {
	case SearchTaskCompleted, SearchTaskCompletedWarn, SearchTaskFailed, SearchTaskTerminated:
		return true
	}
	return false
}

// JobCart 是岗位车条目。
type JobCart struct {
	ID        ID        `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    ID        `json:"user_id" gorm:"column:user_id;type:uuid"`
	JobID     ID        `json:"job_id" gorm:"column:job_id;type:uuid"`
	Note      string    `json:"note" gorm:"column:note"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名。
func (JobCart) TableName() string { return "job_carts" }
