package model

import "time"

// 投递任务状态。取 03 与 06 两份设计文档的并集。
const (
	AppStatusPreparing     = "PREPARING"       // 待准备
	AppStatusLoginRequired = "LOGIN_REQUIRED"  // 需要用户登录招聘网站
	AppStatusFormAnalyzing = "FORM_ANALYZING"  // 正在识别申请表
	AppStatusFormFilling   = "FORM_FILLING"    // 填写中
	AppStatusWaitingUser   = "WAITING_USER"    // 等待人工操作
	AppStatusReadyToSubmit = "READY_TO_SUBMIT" // 待最终审核（由用户自己提交）
	AppStatusSubmitted     = "SUBMITTED"       // 已投递
	AppStatusWrittenTest   = "WRITTEN_TEST"    // 笔试
	AppStatusInterview1    = "INTERVIEW_1"     // 一面
	AppStatusInterview2    = "INTERVIEW_2"     // 二面
	AppStatusHRInterview   = "HR_INTERVIEW"    // HR 面
	AppStatusOffer         = "OFFER"           // Offer
	AppStatusRejected      = "REJECTED"        // 被拒
	AppStatusWithdrawn     = "WITHDRAWN"       // 主动撤回
	AppStatusFailed        = "FAILED"          // 自动化异常
	AppStatusBlocked       = "BLOCKED"         // 被网站阻止，需人工
)

// AllApplicationStatuses 是全部合法状态，用于参数校验。
var AllApplicationStatuses = []string{
	AppStatusPreparing, AppStatusLoginRequired, AppStatusFormAnalyzing, AppStatusFormFilling,
	AppStatusWaitingUser, AppStatusReadyToSubmit, AppStatusSubmitted,
	AppStatusWrittenTest, AppStatusInterview1, AppStatusInterview2, AppStatusHRInterview,
	AppStatusOffer, AppStatusRejected, AppStatusWithdrawn, AppStatusFailed, AppStatusBlocked,
}

// StatusLabels 是状态的中文展示名。
var StatusLabels = map[string]string{
	AppStatusPreparing:     "待准备",
	AppStatusLoginRequired: "需要登录",
	AppStatusFormAnalyzing: "识别表单中",
	AppStatusFormFilling:   "填写中",
	AppStatusWaitingUser:   "等待人工操作",
	AppStatusReadyToSubmit: "待最终审核",
	AppStatusSubmitted:     "已投递",
	AppStatusWrittenTest:   "笔试",
	AppStatusInterview1:    "一面",
	AppStatusInterview2:    "二面",
	AppStatusHRInterview:   "HR 面",
	AppStatusOffer:         "Offer",
	AppStatusRejected:      "已拒绝",
	AppStatusWithdrawn:     "已撤回",
	AppStatusFailed:        "异常",
	AppStatusBlocked:       "被阻止",
}

// terminalStatuses 是流程结束状态，界面上灰化并折叠到底部。
var terminalStatuses = map[string]bool{
	AppStatusOffer:     true,
	AppStatusRejected:  true,
	AppStatusWithdrawn: true,
}

// IsTerminalStatus 报告状态是否已结束。
func IsTerminalStatus(s string) bool { return terminalStatuses[s] }

// submittedOrBeyond 是「已经真实投出去」的状态集合，用于统计。
var submittedOrBeyond = map[string]bool{
	AppStatusSubmitted:   true,
	AppStatusWrittenTest: true,
	AppStatusInterview1:  true,
	AppStatusInterview2:  true,
	AppStatusHRInterview: true,
	AppStatusOffer:       true,
	AppStatusRejected:    true,
}

// IsSubmittedOrBeyond 报告该状态是否意味着已完成投递。
func IsSubmittedOrBeyond(s string) bool { return submittedOrBeyond[s] }

// interviewStatuses 是面试阶段集合。
var interviewStatuses = map[string]bool{
	AppStatusInterview1:  true,
	AppStatusInterview2:  true,
	AppStatusHRInterview: true,
}

// IsInterviewStatus 报告该状态是否处于面试阶段。
func IsInterviewStatus(s string) bool { return interviewStatuses[s] }

// Application 是一次投递任务。
type Application struct {
	ID             ID         `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID         ID         `json:"user_id" gorm:"column:user_id;type:uuid"`
	JobID          ID         `json:"job_id" gorm:"column:job_id;type:uuid"`
	Status         string     `json:"status" gorm:"column:status"`
	Progress       int        `json:"progress" gorm:"column:progress"`
	ApplicationURL string     `json:"application_url" gorm:"column:application_url"`
	Note           string     `json:"note" gorm:"column:note"`
	StartedAt      *time.Time `json:"started_at" gorm:"column:started_at"`
	LastActionAt   *time.Time `json:"last_action_at" gorm:"column:last_action_at"`
	SubmittedAt    *time.Time `json:"submitted_at" gorm:"column:submitted_at"`
	Timestamps

	// Job 是可选的预加载关联。
	Job *Job `json:"job,omitempty" gorm:"foreignKey:JobID;references:ID"`
}

// TableName 指定表名。
func (Application) TableName() string { return "applications" }

// 浏览器任务状态。
const (
	BrowserTaskPending     = "PENDING"
	BrowserTaskRunning     = "RUNNING"
	BrowserTaskWaitingUser = "WAITING_USER"
	BrowserTaskPaused      = "PAUSED"
	BrowserTaskCompleted   = "COMPLETED"
	BrowserTaskFailed      = "FAILED"
	BrowserTaskBlocked     = "BLOCKED"
)

// BrowserTask 是一次浏览器辅助填写任务。
type BrowserTask struct {
	ID            ID              `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	ApplicationID ID              `json:"application_id" gorm:"column:application_id;type:uuid"`
	Status        string          `json:"status" gorm:"column:status"`
	SiteKey       string          `json:"site_key" gorm:"column:site_key"`
	CurrentURL    string          `json:"current_url" gorm:"column:current_url"`
	Step          string          `json:"step" gorm:"column:step"`
	FieldTotal    int             `json:"field_total" gorm:"column:field_total"`
	FieldFilled   int             `json:"field_filled" gorm:"column:field_filled"`
	FieldSkipped  int             `json:"field_skipped" gorm:"column:field_skipped"`
	PendingFields JSONStringArray `json:"pending_fields" gorm:"column:pending_fields;type:jsonb"`
	ErrorMessage  string          `json:"error_message" gorm:"column:error_message"`
	StartedAt     *time.Time      `json:"started_at" gorm:"column:started_at"`
	FinishedAt    *time.Time      `json:"finished_at" gorm:"column:finished_at"`
	Timestamps
}

// TableName 指定表名。
func (BrowserTask) TableName() string { return "browser_tasks" }

// IsResumable 报告任务是否可以继续。
func (t BrowserTask) IsResumable() bool {
	return t.Status == BrowserTaskWaitingUser || t.Status == BrowserTaskPaused
}

// ApplicationField 是表单字段的分析结果，只存元信息，绝不存字段值。
type ApplicationField struct {
	ID            ID      `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	ApplicationID ID      `json:"application_id" gorm:"column:application_id;type:uuid"`
	FieldName     string  `json:"field_name" gorm:"column:field_name"`
	FieldLabel    string  `json:"field_label" gorm:"column:field_label"`
	FieldType     string  `json:"field_type" gorm:"column:field_type"`
	MappedSource  *string `json:"mapped_source" gorm:"column:mapped_source"`
	Confidence    float64 `json:"confidence" gorm:"column:confidence"`
	IsSensitive   bool    `json:"is_sensitive" gorm:"column:is_sensitive"`
	IsFilled      bool    `json:"is_filled" gorm:"column:is_filled"`
	IsRequired    bool    `json:"is_required" gorm:"column:is_required"`
	SkipReason    string  `json:"skip_reason" gorm:"column:skip_reason"`
	Timestamps
}

// TableName 指定表名。
func (ApplicationField) TableName() string { return "application_fields" }

// 事件类型。
const (
	EventJobAddedToCart     = "JOB_ADDED_TO_CART"
	EventJobRemovedFromCart = "JOB_REMOVED_FROM_CART"
	EventApplicationCreated = "APPLICATION_CREATED"
	EventBrowserStarted     = "BROWSER_STARTED"
	EventUserRequired       = "USER_REQUIRED"
	EventFormFilled         = "FORM_FILLED"
	EventReadyToSubmit      = "READY_TO_SUBMIT"
	EventUserSubmitted      = "USER_SUBMITTED"
	EventStatusChanged      = "STATUS_CHANGED"
	EventBrowserFailed      = "BROWSER_FAILED"
	EventJobScraped         = "JOB_SCRAPED"
)

// ApplicationEvent 记录投递流程中的一次事件。
type ApplicationEvent struct {
	ID            ID        `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	ApplicationID *ID       `json:"application_id" gorm:"column:application_id;type:uuid"`
	JobID         *ID       `json:"job_id" gorm:"column:job_id;type:uuid"`
	EventType     string    `json:"event_type" gorm:"column:event_type"`
	Description   string    `json:"description" gorm:"column:description"`
	FromStatus    string    `json:"from_status" gorm:"column:from_status"`
	ToStatus      string    `json:"to_status" gorm:"column:to_status"`
	CreatedAt     time.Time `json:"created_at" gorm:"column:created_at;autoCreateTime"`
}

// TableName 指定表名。
func (ApplicationEvent) TableName() string { return "application_events" }
