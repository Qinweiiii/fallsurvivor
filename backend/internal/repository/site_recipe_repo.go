package repository

import (
	"context"

	"gorm.io/gorm"

	"github.com/eddiel/fallsurvivor/backend/internal/site"
)

// SiteRecipeRepo 是站点 Recipe 的仓储。
//
// 实体定义与查询逻辑放在 internal/site 包（与领域概念保持一致），
// 这里只做一层薄封装，让 repository.Store 能统一注入。
type SiteRecipeRepo struct {
	db    *gorm.DB
	inner *site.Repository
}

// NewSiteRecipeRepo 由 Store 构造时调用。
func newSiteRecipeRepo(db *gorm.DB) *SiteRecipeRepo {
	return &SiteRecipeRepo{db: db, inner: site.NewRepository(db)}
}

// Inner 暴露底层 site.Repository，供 service 层直接使用其方法集。
func (r *SiteRecipeRepo) Inner() *site.Repository { return r.inner }

// ListEnabled 返回全部启用的 Recipe。
func (r *SiteRecipeRepo) ListEnabled(ctx context.Context) ([]site.Recipe, error) {
	return r.inner.ListEnabled(ctx)
}

// ListAll 返回全部 Recipe（含禁用）。
func (r *SiteRecipeRepo) ListAll(ctx context.Context) ([]site.Recipe, error) {
	return r.inner.ListAll(ctx)
}

// GetBySiteKey 按站点标识查询。
func (r *SiteRecipeRepo) GetBySiteKey(ctx context.Context, siteKey string) (*site.Recipe, error) {
	return r.inner.GetBySiteKey(ctx, siteKey)
}

// GetByID 按主键查询。
func (r *SiteRecipeRepo) GetByID(ctx context.Context, id string) (*site.Recipe, error) {
	return r.inner.GetByID(ctx, id)
}

// Create 新建 Recipe。
func (r *SiteRecipeRepo) Create(ctx context.Context, rc *site.Recipe) error {
	return r.inner.Create(ctx, rc)
}

// Update 更新 Recipe。
func (r *SiteRecipeRepo) Update(ctx context.Context, rc *site.Recipe) error {
	return r.inner.Update(ctx, rc)
}

// Delete 删除 Recipe。
func (r *SiteRecipeRepo) Delete(ctx context.Context, id string) error {
	return r.inner.Delete(ctx, id)
}

// RecordRun 记录一次执行结果。
func (r *SiteRecipeRepo) RecordRun(ctx context.Context, run *site.Run) error {
	return r.inner.RecordRun(ctx, run)
}

// ListRuns 返回最近的执行记录。
func (r *SiteRecipeRepo) ListRuns(ctx context.Context, siteKey string, limit int) ([]site.Run, error) {
	return r.inner.ListRuns(ctx, siteKey, limit)
}

// SeedPresets 写入内置预置 Recipe（仅当 site_key 不存在时）。
func (r *SiteRecipeRepo) SeedPresets(ctx context.Context) error {
	return r.inner.SeedPresets(ctx)
}
