// Package site 承载 Site Recipe Registry：把「已知招聘站点怎么采集」固化成数据。
//
// 设计原则（刻意避免过度设计）：
//   - Recipe 只描述「用什么策略 + 用哪个适配器 + 有哪些限制」，
//     不设计成通用采集 DSL。真正的执行逻辑仍写在代码里（executor / Worker adapter）；
//   - 这样新增一家公司 = 新增一条 Recipe 记录，而不是新增一个 Go 分支；
//   - 腾讯 / 字节已验证的逻辑不推倒重写，以 adapter_key 挂到 Recipe 上复用。
//
// 安全边界与 crawler 一致：不接管登录、不绕过反爬，浏览器采集依赖用户
// 在 Worker 持久会话中自行完成登录。
package site

import (
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// Strategy 采集策略类型。
type Strategy string

const (
	// StrategyBrowser 浏览器抽取：复用 Worker 的站点 adapter（腾讯、字节）。
	// 适用于 SPA / 需登录 / 列表走异步接口 的站点。
	StrategyBrowser Strategy = "browser"
	// StrategyAPI 直接调用公开 HTTP 接口。当前支持 GET 列表接口。
	StrategyAPI Strategy = "api"
	// StrategyBrowserObserved 通过保存的页面动作触发站点自身请求，并直接解析
	// 本次浏览器观测到的响应。它不重放脱离页面生命周期的 HTTP 请求。
	StrategyBrowserObserved Strategy = "browser_observed"
	// StrategyURLTemplate 按岗位 ID 模板拼详情页 URL（当前保留扩展位）。
	StrategyURLTemplate Strategy = "url_template"
)

// Source 标记 Recipe 的来源，用于区分内置预置与人工/探索产物。
type Source string

const (
	// SourcePreset 内置预置（代码内 seed，随版本提供）。
	SourcePreset Source = "preset"
	// SourceManual 人工在配置页录入。
	SourceManual Source = "manual"
	// SourceExploration 由 Exploration Agent 产出（后续阶段填充）。
	SourceExploration Source = "exploration"
)

// VerifyStatus 标记 Recipe 的验证状态，是 Agent 自愈闭环的状态机。
//
// 流转规则：
//
//	unverified --(探索后独立试跑成功)--> verified
//	verified   --(连续失败达阈值)-----> invalid
//	invalid    --(重新探索并验证通过)--> verified
//
// 只有 verified 的 Recipe 才值得信赖；invalid 会在下次命中时触发重新探索，
// 而不是一直用一条已经失效的配置反复失败。
type VerifyStatus string

const (
	// VerifyUnverified 尚未经过独立试跑验证。
	VerifyUnverified VerifyStatus = "unverified"
	// VerifyVerified 已用真实请求验证能采到岗位。
	VerifyVerified VerifyStatus = "verified"
	// VerifyInvalid 连续失败，判定已失效，需重新探索。
	VerifyInvalid VerifyStatus = "invalid"
)

// MaxConsecutiveFailures 连续失败达到该次数即把 Recipe 标记为失效。
//
// 取 3 而不是 1：单次失败常见于网络抖动或站点临时限流，
// 立刻判失效会导致频繁重探（探索成本远高于重试）。
const MaxConsecutiveFailures = 3

// Recipe 描述「某个招聘站点怎么采集」。
type Recipe struct {
	ID          string `json:"id"        gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	SiteKey     string `json:"site_key"  gorm:"column:site_key;uniqueIndex;type:text;not null"`
	CompanyName string `json:"company_name" gorm:"column:company_name;type:text;not null"`
	Domain      string `json:"domain"    gorm:"column:domain;type:text;not null"`
	CampusURL   string `json:"campus_url" gorm:"column:campus_url;type:text;not null;default:''"`

	StrategyType Strategy `json:"strategy_type" gorm:"column:strategy_type;type:varchar(30);not null;default:'browser'"`
	// AdapterKey 仅 browser 策略使用，对应 Worker 侧 site adapter 的 key
	// （见 worker-browser/src/sites/adapters.ts 的 adapter 表）。
	AdapterKey string `json:"adapter_key" gorm:"column:adapter_key;type:text;not null;default:''"`

	// API / url_template 策略使用。由 Exploration Agent 从网络请求中沉淀，
	// 使已探索过的站点下次可以直接复用接口与字段映射。
	ListAPI           string        `json:"list_api" gorm:"column:list_api;type:text;not null;default:''"`
	DetailAPI         string        `json:"detail_api" gorm:"column:detail_api;type:text;not null;default:''"`
	DetailURLTemplate string        `json:"detail_url_template" gorm:"column:detail_url_template;type:text;not null;default:''"`
	Method            string        `json:"method" gorm:"column:method;type:varchar(10);not null;default:'GET'"`
	IDField           string        `json:"id_field" gorm:"column:id_field;type:text;not null;default:''"`
	TitleField        string        `json:"title_field" gorm:"column:title_field;type:text;not null;default:''"`
	ListPath          string        `json:"list_path" gorm:"column:list_path;type:text;not null;default:''"`
	KeywordParam      string        `json:"keyword_param" gorm:"column:keyword_param;type:text;not null;default:''"`
	FieldMap          model.JSONMap `json:"field_map" gorm:"column:field_map;type:jsonb;serializer:json"`

	// ---- 请求侧配置：让「怎么发这个请求」成为数据，而不是 Go 里的站点分支 ----
	//
	// 这三个字段由 Exploration Agent 从**真实观测到的请求**中沉淀。
	// 有了它们，执行器只需原样复现，不必再猜「这个站点是要 JSON 还是 form」。
	//
	// RequestBody 仅 POST 时使用，内容是探索时观测到的请求体原文。
	RequestBody string `json:"request_body" gorm:"column:request_body;type:text;not null;default:''"`
	// RequestContentType 请求体类型（application/json 或
	// application/x-www-form-urlencoded）。留空时按 JSON 处理。
	RequestContentType string `json:"request_content_type" gorm:"column:request_content_type;type:text;not null;default:''"`
	// RequestHeaders 复现接口所需的非凭证请求头（渠道 / 语言等）。
	// 写入前经应用层白名单过滤，绝不含 Cookie / Authorization。
	RequestHeaders model.JSONMap `json:"request_headers" gorm:"column:request_headers;type:jsonb;serializer:json"`
	// BrowserPlan 是浏览器观测策略的可复用动作计划。
	// 只保存跨会话稳定的语义动作（例如 navigate / search / 文案 click），
	// 不保存一次会话内的 DOM ref。
	BrowserPlan model.JSONMap `json:"browser_plan" gorm:"column:browser_plan;type:jsonb;serializer:json"`

	// ---- 健康度：驱动「验证通过才保存」与「失效自动重探」----
	//
	// VerifyStatus 与 ConsecutiveFailures 同样【刻意不写】default tag，
	// 原因与下方 Enabled 一致：零值（0 次失败）有业务含义，必须能写进去。
	VerifyStatus VerifyStatus `json:"verify_status" gorm:"column:verify_status;type:varchar(20);not null"`
	// VerifiedAt 最近一次验证通过的时间。
	VerifiedAt *time.Time `json:"verified_at" gorm:"column:verified_at"`
	// VerifiedJobs 验证时实际采到的岗位数，用于判断配置质量。
	VerifiedJobs int `json:"verified_jobs" gorm:"column:verified_jobs;not null"`
	// ConsecutiveFailures 连续失败次数；成功一次即归零。
	ConsecutiveFailures int `json:"consecutive_failures" gorm:"column:consecutive_failures;not null"`
	// LastError 最近一次失败原因，供自修正时喂回模型。
	LastError string `json:"last_error" gorm:"column:last_error;type:text;not null;default:''"`

	// KeywordInBody 关键词是否放在请求体里（POST 搜索接口常见）。
	// 同样不带 default tag：false 是合法业务值。
	KeywordInBody bool `json:"keyword_in_body" gorm:"column:keyword_in_body;not null"`

	// 注意：以下三个字段【刻意不写】 gorm 的 default tag。
	//
	// GORM 对带有 default tag 的字段，在值为零值（false / 0）时会跳过该列，
	// 让数据库套用 DEFAULT。这会导致业务上合法的零值写不进去：
	//   - enabled=false（禁用站点）会被存成 true；
	//   - max_detail_fetches=0（不抓详情页，用抽取阶段的 JD）会被存成 8。
	// 这两个零值都有实际业务含义，因此去掉 default tag，改由应用层保证默认值
	// （见 handler.validateRecipe 与 SiteCrawler.CrawlWithRecipe 的兜底逻辑）。
	Enabled bool `json:"enabled" gorm:"column:enabled;not null"`

	// MaxJobsPerSearch 单次搜索最多返回的岗位条数。
	MaxJobsPerSearch int `json:"max_jobs_per_search" gorm:"column:max_jobs_per_search;not null"`
	// MaxDetailFetches 最多对多少条岗位抓取详情正文。
	// 0 表示完全跳过详情页抓取（适用于 Worker 侧已拿到完整 JD 的站点，如腾讯）。
	MaxDetailFetches int `json:"max_detail_fetches" gorm:"column:max_detail_fetches;not null"`

	Notes  string `json:"notes"  gorm:"column:notes;type:text;not null;default:''"`
	Source Source `json:"source" gorm:"column:source;type:varchar(30);not null;default:'manual'"`
	// Version 人工修订或探索重新生成时递增，便于追溯。
	// 同样不带 default tag，避免出现零值写不进去的问题。
	Version int `json:"version" gorm:"column:version;not null"`

	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;not null;default:now()"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;not null;default:now()"`
}

// TableName 指定表名。
func (Recipe) TableName() string { return "site_recipes" }

// RunStatus Recipe 一次执行的结果状态。
type RunStatus string

const (
	// RunSuccess 成功采集到岗位。
	RunSuccess RunStatus = "success"
	// RunEmpty 执行成功但没有采集到任何岗位（可能是筛选条件过窄或页面结构变化）。
	RunEmpty RunStatus = "empty"
	// RunFailed 执行过程中出错（与配置相关的真实失败，计入连续失败）。
	RunFailed RunStatus = "failed"
	// RunEnvironment 执行因环境故障（Worker 未启动 / 登录过期）而失败，
	// 与 Recipe 配置无关，只记录执行历史、不计入连续失败、不触发失效。
	RunEnvironment RunStatus = "environment"
)

// Run 记录 Recipe 的一次执行结果，用于观测与后续 Reflection。
type Run struct {
	ID           string    `json:"id"          gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	RecipeID     *string   `json:"recipe_id"   gorm:"column:recipe_id;type:uuid"`
	SiteKey      string    `json:"site_key"    gorm:"column:site_key;type:text;not null"`
	Keyword      string    `json:"keyword"     gorm:"column:keyword;type:text;not null;default:''"`
	Status       RunStatus `json:"status"      gorm:"column:status;type:varchar(20);not null"`
	JobsFound    int       `json:"jobs_found"  gorm:"column:jobs_found;not null;default:0"`
	JobsIngested int       `json:"jobs_ingested" gorm:"column:jobs_ingested;not null;default:0"`
	ErrorMessage string    `json:"error_message" gorm:"column:error_message;type:text;not null;default:''"`
	DurationMS   int64     `json:"duration_ms" gorm:"column:duration_ms;not null;default:0"`
	CreatedAt    time.Time `json:"created_at"  gorm:"column:created_at;not null;default:now()"`
}

// TableName 指定表名。
func (Run) TableName() string { return "recipe_runs" }
