// Package pagination 提供统一的分页参数与响应结构。
package pagination

// 分页边界。
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// AllowedPageSizes 是前端允许选择的每页条数。
var AllowedPageSizes = []int{20, 50, 100}

// Query 是规范化后的分页请求。
type Query struct {
	Page     int `form:"page" json:"page"`
	PageSize int `form:"page_size" json:"page_size"`
}

// Normalize 修正非法的分页参数，避免出现负偏移或超大查询。
func (q *Query) Normalize() {
	if q.Page < 1 {
		q.Page = DefaultPage
	}
	if q.PageSize <= 0 {
		q.PageSize = DefaultPageSize
	}
	if q.PageSize > MaxPageSize {
		q.PageSize = MaxPageSize
	}
}

// Offset 返回 SQL OFFSET。
func (q Query) Offset() int { return (q.Page - 1) * q.PageSize }

// Limit 返回 SQL LIMIT。
func (q Query) Limit() int { return q.PageSize }

// Meta 是响应中的分页元信息。
type Meta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// NewMeta 计算分页元信息。
func NewMeta(q Query, total int64) Meta {
	totalPages := 0
	if q.PageSize > 0 {
		totalPages = int((total + int64(q.PageSize) - 1) / int64(q.PageSize))
	}
	return Meta{Page: q.Page, PageSize: q.PageSize, Total: total, TotalPages: totalPages}
}

// Page 是统一的分页响应体。
type Page[T any] struct {
	Items      []T  `json:"items"`
	Pagination Meta `json:"pagination"`
}

// NewPage 构造分页响应，保证 items 永不为 null。
func NewPage[T any](items []T, q Query, total int64) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Pagination: NewMeta(q, total)}
}
