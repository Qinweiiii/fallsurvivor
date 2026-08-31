// Package dto 定义 HTTP 层的请求与响应结构。
//
// 所有外部输入都必须先绑定到本包中的具体结构体，
// 严禁使用 map[string]any 接收请求体。
package dto

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
)

// 校验错误。
var (
	ErrInvalidID     = errors.New("ID 格式不合法")
	ErrTooManyIDs    = errors.New("单次操作的数量超出上限")
	ErrInvalidStatus = errors.New("状态值不合法")
)

// maxBatchSize 限制批量操作的规模，防止资源耗尽。
const maxBatchSize = 200

// ParseUUID 解析并校验 UUID。
func ParseUUID(s string) (model.ID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return model.ID{}, ErrInvalidID
	}
	return id, nil
}

// ParseUUIDs 批量解析 UUID，并限制数量。
func ParseUUIDs(list []string) ([]model.ID, error) {
	if len(list) > maxBatchSize {
		return nil, ErrTooManyIDs
	}
	out := make([]model.ID, 0, len(list))
	seen := make(map[model.ID]bool, len(list))
	for _, s := range list {
		id, err := ParseUUID(s)
		if err != nil {
			return nil, err
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// ---------------- 求职画像 ----------------

// UpdateProfileRequest 是更新求职画像的请求。
type UpdateProfileRequest struct {
	TargetRoles        []string `json:"target_roles"`
	PreferredLanguages []string `json:"preferred_languages"`
	PreferredLocations []string `json:"preferred_locations"`
	CompanyPreferences []string `json:"company_preferences"`
	TargetIndustries   []string `json:"target_industries"`
	GraduationYear     int      `json:"graduation_year"`
}

// maxTagLen 限制单个标签长度。
const maxTagLen = 50

// maxTagCount 限制标签数量。
const maxTagCount = 30

// Validate 校验并清洗输入。
func (r *UpdateProfileRequest) Validate() error {
	r.TargetRoles = sanitizeTags(r.TargetRoles)
	r.PreferredLanguages = sanitizeTags(r.PreferredLanguages)
	r.PreferredLocations = sanitizeTags(r.PreferredLocations)
	r.CompanyPreferences = sanitizeTags(r.CompanyPreferences)
	r.TargetIndustries = sanitizeTags(r.TargetIndustries)

	if r.GraduationYear != 0 && (r.GraduationYear < 2000 || r.GraduationYear > 2100) {
		return errors.New("毕业届次不合法")
	}
	if r.GraduationYear == 0 {
		r.GraduationYear = 2027
	}
	return nil
}

// sanitizeTags 清洗标签数组：去空、去重、限长、限量。
func sanitizeTags(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		if r := []rune(t); len(r) > maxTagLen {
			t = string(r[:maxTagLen])
		}
		k := strings.ToLower(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
		if len(out) >= maxTagCount {
			break
		}
	}
	return out
}

// ---------------- 岗位列表 ----------------

// JobListQuery 是岗位列表的查询参数。
type JobListQuery struct {
	pagination.Query
	Keyword     string   `form:"keyword"`
	Company     string   `form:"company"`
	Title       string   `form:"title"`
	Locations   []string `form:"locations"`
	Languages   []string `form:"languages"`
	Statuses    []string `form:"statuses"`
	Sources     []string `form:"sources"`
	MinScore    int      `form:"min_score"`
	HasDeadline string   `form:"has_deadline"`
	SortBy      string   `form:"sort_by"`
	SortOrder   string   `form:"sort_order"`
}

// maxKeywordLen 限制关键词长度。
const maxKeywordLen = 100

// Validate 清洗查询参数。
func (q *JobListQuery) Validate() error {
	q.Query.Normalize()

	q.Keyword = clampText(q.Keyword, maxKeywordLen)
	q.Company = clampText(q.Company, maxKeywordLen)
	q.Title = clampText(q.Title, maxKeywordLen)

	q.Locations = sanitizeTags(q.Locations)
	q.Languages = sanitizeTags(q.Languages)
	q.Sources = filterAllowed(q.Sources, model.AllSourceTypes)
	q.Statuses = filterAllowed(q.Statuses, model.AllJobStatuses)

	if q.MinScore < 0 {
		q.MinScore = 0
	}
	if q.MinScore > 100 {
		q.MinScore = 100
	}
	return nil
}

// HasDeadlineFilter 返回截止日期筛选条件。
func (q *JobListQuery) HasDeadlineFilter() *bool {
	switch strings.ToLower(strings.TrimSpace(q.HasDeadline)) {
	case "true", "1", "yes":
		v := true
		return &v
	case "false", "0", "no":
		v := false
		return &v
	default:
		return nil
	}
}

// clampText 裁剪文本长度。
func clampText(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// filterAllowed 只保留位于白名单中的值。
func filterAllowed(in, allowed []string) []string {
	if len(in) == 0 {
		return nil
	}
	set := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		set[a] = true
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		v := strings.ToUpper(strings.TrimSpace(s))
		if set[v] {
			out = append(out, v)
		}
	}
	return out
}

// ---------------- 搜索任务 ----------------

// SearchRequest 是触发搜索的请求。
type SearchRequest struct {
	Force bool `json:"force"`
}

// ---------------- 岗位车 ----------------

// CartRequest 是单个岗位加入岗位车的请求。
type CartRequest struct {
	JobID string `json:"job_id"`
}

// CartBatchRequest 是批量操作岗位车的请求。
type CartBatchRequest struct {
	JobIDs []string `json:"job_ids"`
}

// ---------------- 投递任务 ----------------

// ApplicationCreateRequest 是创建单个投递任务的请求。
type ApplicationCreateRequest struct {
	JobID string `json:"job_id"`
}

// ApplicationBatchRequest 是批量创建投递任务的请求。
type ApplicationBatchRequest struct {
	JobIDs []string `json:"job_ids"`
}

// ApplicationListQuery 是投递列表查询参数。
type ApplicationListQuery struct {
	pagination.Query
	Statuses []string `form:"statuses"`
	Keyword  string   `form:"keyword"`
	Company  string   `form:"company"`
	Scope    string   `form:"scope"` // all | active | finished
}

// Validate 清洗查询参数。
func (q *ApplicationListQuery) Validate() error {
	q.Query.Normalize()
	q.Keyword = clampText(q.Keyword, maxKeywordLen)
	q.Company = clampText(q.Company, maxKeywordLen)
	q.Statuses = filterAllowed(q.Statuses, model.AllApplicationStatuses)
	switch strings.ToLower(strings.TrimSpace(q.Scope)) {
	case "active", "finished", "all", "":
	default:
		q.Scope = "all"
	}
	return nil
}

// UpdateStatusRequest 是更新投递状态的请求。
type UpdateStatusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// Validate 校验状态值。
func (r *UpdateStatusRequest) Validate() error {
	r.Status = strings.ToUpper(strings.TrimSpace(r.Status))
	for _, s := range model.AllApplicationStatuses {
		if s == r.Status {
			r.Note = clampText(r.Note, 500)
			return nil
		}
	}
	return ErrInvalidStatus
}

// ---------------- 简历 ----------------

// ResumeTextRequest 是提交简历文本的请求。
type ResumeTextRequest struct {
	Text string `json:"text"`
}

// maxResumeTextLen 限制简历文本长度。
const maxResumeTextLen = 50000

// Validate 校验简历文本。
func (r *ResumeTextRequest) Validate() error {
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		return errors.New("简历文本不能为空")
	}
	if len([]rune(r.Text)) > maxResumeTextLen {
		return errors.New("简历文本过长")
	}
	return nil
}
