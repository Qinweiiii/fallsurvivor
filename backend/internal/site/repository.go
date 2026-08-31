package site

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Repository 提供 site_recipes 与 recipe_runs 的数据访问。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造仓储。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// ListEnabled 返回全部启用的 Recipe，按公司名排序。
// 用于 Registry 启动时装载，以及配置页展示。
func (r *Repository) ListEnabled(ctx context.Context) ([]Recipe, error) {
	var out []Recipe
	err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("company_name ASC").
		Find(&out).Error
	return out, err
}

// ListAll 返回全部 Recipe（含禁用），按公司名排序。供配置管理页使用。
func (r *Repository) ListAll(ctx context.Context) ([]Recipe, error) {
	var out []Recipe
	err := r.db.WithContext(ctx).
		Order("company_name ASC").
		Find(&out).Error
	return out, err
}

// GetBySiteKey 按站点标识查询 Recipe。未找到返回 nil, nil。
func (r *Repository) GetBySiteKey(ctx context.Context, siteKey string) (*Recipe, error) {
	var out Recipe
	err := r.db.WithContext(ctx).
		Where("site_key = ?", siteKey).
		First(&out).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetByID 按主键查询 Recipe。未找到返回 nil, nil。
func (r *Repository) GetByID(ctx context.Context, id string) (*Recipe, error) {
	var out Recipe
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&out).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Create 新建 Recipe。site_key 唯一冲突时返回 error。
func (r *Repository) Create(ctx context.Context, rc *Recipe) error {
	now := time.Now().UTC()
	rc.CreatedAt = now
	rc.UpdatedAt = now
	return r.db.WithContext(ctx).Create(rc).Error
}

// Update 全量更新 Recipe（不含 created_at）。
func (r *Repository) Update(ctx context.Context, rc *Recipe) error {
	rc.UpdatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).
		Select("*").Omit("id", "created_at").
		Where("id = ?", rc.ID).
		Updates(rc).Error
}

// Delete 删除 Recipe。
func (r *Repository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&Recipe{}).Error
}

// RecordRun 记录一次 Recipe 执行结果。
func (r *Repository) RecordRun(ctx context.Context, run *Run) error {
	run.CreatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Create(run).Error
}

// ListRuns 返回最近 limit 条执行记录。limit <= 0 时用默认 50。
func (r *Repository) ListRuns(ctx context.Context, siteKey string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Model(&Run{})
	if sk := strings.TrimSpace(siteKey); sk != "" {
		q = q.Where("site_key = ?", sk)
	}
	var out []Run
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}
