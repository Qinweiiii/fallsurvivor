// Package source 定义岗位来源的统一抽象。
//
// 新增来源只需实现 JobSource 并注册到 Registry，无需修改搜索管线。
package source

import (
	"context"
	"time"
)

// SearchQuery 是传给来源的检索请求。
type SearchQuery struct {
	// Queries 是检索式列表。
	Queries []string
	// Locations 是城市偏好，来源可用于附加过滤。
	Locations []string
	// CompanyPreferences 是用户画像中的公司偏好，可包含具体公司或“大厂/外企”等宽泛类别。
	CompanyPreferences []string
	// GraduationYear 是目标毕业届次。
	GraduationYear int
	// MaxResultsPerQuery 限制单条检索式返回条数。
	MaxResultsPerQuery int
}

// RawJob 是来源返回的原始岗位线索，尚未标准化。
type RawJob struct {
	// SourceType 取 model.SourceOfficial / SourceBoss / SourceTavily。
	SourceType string
	// SourceName 是具体来源名，例如 "字节跳动招聘官网"。
	SourceName string
	// URL 是用户可访问的岗位页地址。没有真实详情页时必须为空，不能填列表 API。
	URL string
	// IdentityURL 是仅供去重的稳定岗位身份，不向用户展示为外链。
	// API 型站点没有详情页 URL 时可使用列表接口加岗位 ID 作为身份。
	IdentityURL string
	// Title 是原始标题。
	Title string
	// Snippet 是摘要文本，可能就是全部可用信息。
	Snippet string
	// Content 是更完整的页面文本（若来源能提供）。
	Content string
	// CompanyHint 是来源已知的公司名线索。
	CompanyHint string
	// LocationHint 是来源已知的地点线索。
	LocationHint string
	// Score 是来源给出的相关度（0~1），无则为 0。
	Score float64
	// PublishedAt 是发布时间，未知则为 nil。
	PublishedAt *time.Time
	// Query 记录该结果由哪条检索式命中。
	Query string
	// Meta 携带来源已知的结构化字段（如官方接口直接返回的事业群/业务/城市）。
	//
	// 这些值由来源保证其权威性，pipeline 会优先采用并覆盖模型解析结果，
	// 避免模型在长文本中漏提取或猜错。仅接受 Department / Business /
	// Location / Company 四个键，其余键会被忽略。
	Meta map[string]string
}

// Result 是单个来源的执行结果。
type Result struct {
	SourceType string   `json:"source_type"`
	SourceName string   `json:"source_name"`
	Success    bool     `json:"success"`
	Count      int      `json:"count"`
	Error      string   `json:"error,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	DurationMS int64    `json:"duration_ms"`
}

// JobSource 是岗位来源接口。
type JobSource interface {
	// Type 返回来源类型常量。
	Type() string
	// Name 返回可读名称。
	Name() string
	// Available 报告该来源当前是否可用（例如是否配置了 API Key）。
	Available() bool
	// Search 执行检索。返回错误时不应导致整个搜索任务失败。
	Search(ctx context.Context, q SearchQuery) ([]RawJob, error)
}

// Registry 是来源注册表。
type Registry struct {
	sources []JobSource
}

// NewRegistry 创建注册表。
func NewRegistry(sources ...JobSource) *Registry {
	return &Registry{sources: sources}
}

// Register 追加来源。
func (r *Registry) Register(s JobSource) { r.sources = append(r.sources, s) }

// Available 返回当前可用的来源。
func (r *Registry) Available() []JobSource {
	out := make([]JobSource, 0, len(r.sources))
	for _, s := range r.sources {
		if s.Available() {
			out = append(out, s)
		}
	}
	return out
}

// All 返回全部已注册来源。
func (r *Registry) All() []JobSource { return r.sources }
