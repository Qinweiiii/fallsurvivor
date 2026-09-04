package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
)

// JobRepo 负责岗位与岗位来源。
type JobRepo struct{ db *gorm.DB }

// JobFilter 是岗位列表的筛选条件。所有值都以参数绑定方式进入 SQL。
type JobFilter struct {
	Keyword     string   // 模糊匹配公司/岗位/部门
	Company     string   // 公司模糊匹配
	Companies   []string // 公司精确多选
	Title       string   // 岗位关键词
	Locations   []string // 城市多选
	Languages   []string // 技术语言多选
	Statuses    []string // 状态多选
	Sources     []string // 来源多选
	MinScore    int      // 最低匹配度
	HasDeadline *bool    // 是否仅看有截止日期的
	SortBy      string   // 排序字段（白名单）
	SortOrder   string   // asc | desc（白名单）
}

// jobSortWhitelist 是允许出现在 ORDER BY 中的字段。
// 绝不允许把用户输入直接拼进 SQL，只能通过本映射转换。
var jobSortWhitelist = map[string]string{
	"match_score":  "match_score",
	"created_at":   "created_at",
	"published_at": "published_at",
	"deadline":     "deadline",
	"company_name": "company_name",
}

// resolveJobSort 把用户输入映射为安全的 ORDER BY 片段。
func resolveJobSort(sortBy, order string) string {
	col, ok := jobSortWhitelist[strings.ToLower(strings.TrimSpace(sortBy))]
	if !ok {
		col = "match_score"
	}
	dir := "DESC"
	if strings.EqualFold(strings.TrimSpace(order), "asc") {
		dir = "ASC"
	}
	// deadline 可能为 NULL，排序时统一放在最后。
	if col == "deadline" || col == "published_at" {
		return col + " " + dir + " NULLS LAST, created_at DESC"
	}
	return col + " " + dir + ", created_at DESC"
}

// applyJobFilter 把筛选条件转成参数化的查询。
func applyJobFilter(q *gorm.DB, f JobFilter) *gorm.DB {
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where(
			"(company_name ILIKE ? OR title ILIKE ? OR department ILIKE ? OR business ILIKE ?)",
			like, like, like, like,
		)
	}
	if c := strings.TrimSpace(f.Company); c != "" {
		q = q.Where("company_name ILIKE ?", "%"+c+"%")
	}
	if len(f.Companies) > 0 {
		q = q.Where("company_name IN ?", f.Companies)
	}
	if t := strings.TrimSpace(f.Title); t != "" {
		q = q.Where("title ILIKE ?", "%"+t+"%")
	}
	if len(f.Locations) > 0 {
		// 地点可能是「深圳市南山区」，用 ILIKE 逐个 OR。
		clauses := make([]string, 0, len(f.Locations))
		args := make([]any, 0, len(f.Locations)*2)
		for _, loc := range f.Locations {
			loc = strings.TrimSpace(loc)
			if loc == "" {
				continue
			}
			clauses = append(clauses, "(location ILIKE ? OR locations::text ILIKE ?)")
			args = append(args, "%"+loc+"%", "%"+loc+"%")
		}
		if len(clauses) > 0 {
			q = q.Where("("+strings.Join(clauses, " OR ")+")", args...)
		}
	}
	if len(f.Languages) > 0 {
		clauses := make([]string, 0, len(f.Languages))
		args := make([]any, 0, len(f.Languages)*3)
		for _, lang := range f.Languages {
			lang = strings.TrimSpace(lang)
			if lang == "" {
				continue
			}
			clauses = append(clauses,
				"(technical_stack::text ILIKE ? OR language_requirements::text ILIKE ? OR description ILIKE ?)")
			args = append(args, "%"+lang+"%", "%"+lang+"%", "%"+lang+"%")
		}
		if len(clauses) > 0 {
			q = q.Where("("+strings.Join(clauses, " OR ")+")", args...)
		}
	}
	if len(f.Statuses) > 0 {
		q = q.Where("status IN ?", f.Statuses)
	}
	if len(f.Sources) > 0 {
		q = q.Where("source_type IN ?", f.Sources)
	}
	if f.MinScore > 0 {
		q = q.Where("match_score >= ?", f.MinScore)
	}
	if f.HasDeadline != nil {
		if *f.HasDeadline {
			q = q.Where("deadline IS NOT NULL")
		} else {
			q = q.Where("deadline IS NULL")
		}
	}
	return q
}

// List 分页查询岗位。分页与筛选全部由数据库完成。
func (r *JobRepo) List(ctx context.Context, f JobFilter, p pagination.Query) ([]model.Job, int64, error) {
	base := applyJobFilter(r.db.WithContext(ctx).Model(&model.Job{}), f)

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []model.Job{}, 0, nil
	}

	var items []model.Job
	err := applyJobFilter(r.db.WithContext(ctx).Model(&model.Job{}), f).
		Order(resolveJobSort(f.SortBy, f.SortOrder)).
		Limit(p.Limit()).
		Offset(p.Offset()).
		Find(&items).Error
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetByID 查询单个岗位。
func (r *JobRepo) GetByID(ctx context.Context, id model.ID) (*model.Job, error) {
	var j model.Job
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListIncompleteJD 查询 JD 不完整的岗位（desc_quality 不是 'full'），供 JD 补全流程使用。
//
// BOSS 等站点受反爬限制，只能经搜索引擎拿到摘要（snippet/empty），
// 需要先标记为不完整，再由浏览器会话补全正文。
// 按 created_at DESC 排序，优先补全最新发现的岗位。
//
// 注意：jobs 表是全局岗位池，**没有 user_id 列**——
// 用户与岗位的关联（岗位车、投递记录）在各自关联表中。
// 因此这里不做用户维度过滤，补全对所有人的可见岗位生效。
//
// sourceTypes 为空时不过滤来源；否则只处理指定来源（如只看 BOSS）。
func (r *JobRepo) ListIncompleteJD(ctx context.Context, limit int, sourceTypes ...string) ([]model.Job, error) {
	if limit <= 0 {
		limit = 10
	}
	q := r.db.WithContext(ctx).
		Where("desc_quality IS NULL OR desc_quality <> ?", "full").
		Where("source_url IS NOT NULL AND source_url <> ''")
	if len(sourceTypes) > 0 {
		q = q.Where("source_type IN ?", sourceTypes)
	}
	var items []model.Job
	err := q.Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}

// GetManyByIDs 批量查询岗位。
func (r *JobRepo) GetManyByIDs(ctx context.Context, ids []model.ID) ([]model.Job, error) {
	if len(ids) == 0 {
		return []model.Job{}, nil
	}
	var items []model.Job
	err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&items).Error
	return items, err
}

// FindDuplicate 按去重三要素查找已存在的岗位。
// 依次尝试 official_url、normalized_url、company+title+location 指纹。
func (r *JobRepo) FindDuplicate(ctx context.Context, officialURL, normalizedURL, fingerprint string) (*model.Job, error) {
	q := r.db.WithContext(ctx).Model(&model.Job{})

	conds := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if officialURL != "" {
		conds = append(conds, "official_url = ?")
		args = append(args, officialURL)
	}
	if normalizedURL != "" {
		conds = append(conds, "normalized_url = ?")
		args = append(args, normalizedURL)
	}
	if fingerprint != "" {
		conds = append(conds, "dedup_fingerprint = ?")
		args = append(args, fingerprint)
	}
	if len(conds) == 0 {
		return nil, ErrNotFound
	}

	var j model.Job
	err := q.Where(strings.Join(conds, " OR "), args...).First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Create 新建岗位。
func (r *JobRepo) Create(ctx context.Context, j *model.Job) error {
	return r.db.WithContext(ctx).Create(j).Error
}

// UpdateEnrichment 只更新可被搜索管线覆盖的字段，避免误改用户状态。
func (r *JobRepo) UpdateEnrichment(ctx context.Context, id model.ID, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	// 白名单：搜索管线只允许更新这些列。
	// desc_quality 供 JD 补全流程使用：补到完整 JD 后需把质量标记从
	// snippet/empty 改为 full，否则前端会一直提示「JD 不完整」。
	allowed := map[string]bool{
		"department": true, "business": true, "location": true, "locations": true,
		"job_type": true, "graduation_year": true, "description": true,
		"responsibilities": true, "requirements": true, "language_requirements": true,
		"technical_stack": true, "source_url": true, "official_url": true, "published_at": true,
		"deadline": true, "crawled_at": true, "match_score": true, "match_analysis": true,
		"desc_quality": true,
	}
	safe := make(map[string]any, len(fields))
	for k, v := range fields {
		if allowed[k] {
			safe[k] = v
		}
	}
	if len(safe) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Job{}).Where("id = ?", id).Updates(safe).Error
}

// UpdateStatus 更新岗位状态。
func (r *JobRepo) UpdateStatus(ctx context.Context, ids []model.ID, status string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Job{}).
		Where("id IN ?", ids).Update("status", status).Error
}

// UpsertSource 记录岗位来源，冲突时刷新 last_seen_at。
func (r *JobRepo) UpsertSource(ctx context.Context, s *model.JobSource) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "url"}},
		DoUpdates: clause.Assignments(map[string]any{"last_seen_at": time.Now().UTC(), "is_valid": true}),
	}).Create(s).Error
}

// ListSources 查询某岗位的全部来源。
func (r *JobRepo) ListSources(ctx context.Context, jobID model.ID) ([]model.JobSource, error) {
	// 空切片兜底，避免序列化为 null。
	items := make([]model.JobSource, 0, 4)
	err := r.db.WithContext(ctx).Where("job_id = ?", jobID).
		Order("first_seen_at ASC").Find(&items).Error
	return items, err
}

// DistinctCompanies 返回全部公司名，用于筛选下拉。
func (r *JobRepo) DistinctCompanies(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var out []string
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Distinct().Pluck("company_name", &out).Error
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DistinctLocations 返回全部地点，用于筛选下拉。
func (r *JobRepo) DistinctLocations(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var out []string
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("location <> ''").Distinct().Pluck("location", &out).Error
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CountAll 返回岗位总数。
func (r *JobRepo) CountAll(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Job{}).Count(&n).Error
	return n, err
}

// CountCreatedAfter 返回指定时间后新增的岗位数。
func (r *JobRepo) CountCreatedAfter(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("created_at >= ?", since).Count(&n).Error
	return n, err
}

// ListUpcomingDeadlines 返回即将截止的岗位。
func (r *JobRepo) ListUpcomingDeadlines(ctx context.Context, within time.Duration, limit int) ([]model.Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	now := time.Now().UTC()
	// 空切片兜底，避免序列化为 null。
	items := make([]model.Job, 0, limit)
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("deadline IS NOT NULL AND deadline >= ? AND deadline <= ?", now, now.Add(within)).
		Order("deadline ASC").Limit(limit).Find(&items).Error
	return items, err
}

// ListRecentHighMatch 返回最近发现的高匹配岗位。
func (r *JobRepo) ListRecentHighMatch(ctx context.Context, minScore, limit int) ([]model.Job, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	// 空切片兜底，避免序列化为 null。
	items := make([]model.Job, 0, limit)
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("match_score >= ?", minScore).
		Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}
