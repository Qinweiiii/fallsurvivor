package site

import (
	"context"
	"encoding/json"
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

// RecordExplorationRun 保存探索轨迹；失败记录与成功记录同等重要。
func (r *Repository) RecordExplorationRun(ctx context.Context, run *ExplorationRun) error {
	if len(run.Trace) == 0 {
		run.Trace = json.RawMessage("[]")
	}
	run.CreatedAt = time.Now().UTC()
	return r.db.WithContext(ctx).Create(run).Error
}

/**
 * MarkSuccess 标记 Recipe 一次执行成功。
 *
 * 成功即把连续失败计数归零并清空上次错误——
 * 失效判定看的是「连续」失败，中间成功过就说明配置仍然有效
 * （之前的失败多半是网络抖动或站点临时限流）。
 *
 * 同时把状态提升为 verified：能真实采到岗位就是最强的验证。
 */
func (r *Repository) MarkSuccess(ctx context.Context, siteKey string, jobsFound int) error {
	if strings.TrimSpace(siteKey) == "" {
		return nil
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&Recipe{}).
		Where("site_key = ?", siteKey).
		Updates(map[string]any{
			"verify_status":        VerifyVerified,
			"verified_at":          now,
			"verified_jobs":        jobsFound,
			"consecutive_failures": 0,
			"last_error":           "",
			"updated_at":           now,
		}).Error
}

/**
 * MarkFailure 标记 Recipe 一次执行失败，并在连续失败达阈值时判定为失效。
 *
 * 这是 Reflection（自愈）的触发点：一旦状态变成 invalid，
 * 下次命中该站点时 Fast Path 会主动跳过它并重新探索，
 * 而不是抱着一条已经跑不通的配置反复失败、等人来发现。
 *
 * 阈值判定放在 SQL 里用 CASE 完成（而非先读再写），
 * 避免并发执行时两个请求各读到 2 都写成 3 的竞态。
 */
func (r *Repository) MarkFailure(ctx context.Context, siteKey, errMsg string) error {
	if strings.TrimSpace(siteKey) == "" {
		return nil
	}
	now := time.Now().UTC()
	// 截断错误信息：它会被展示在配置页并喂回模型，过长无益。
	if len(errMsg) > 1000 {
		errMsg = errMsg[:1000]
	}
	return r.db.WithContext(ctx).
		Model(&Recipe{}).
		Where("site_key = ?", siteKey).
		Updates(map[string]any{
			"consecutive_failures": gorm.Expr("consecutive_failures + 1"),
			"verify_status": gorm.Expr(
				"CASE WHEN consecutive_failures + 1 >= ? THEN ? ELSE verify_status END",
				MaxConsecutiveFailures, VerifyInvalid),
			"last_error": errMsg,
			"updated_at": now,
		}).Error
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

// ListPlaybook 返回启用的探索经验，按人工优先级与历史命中数排序。
func (r *Repository) ListPlaybook(ctx context.Context, limit int) ([]PlaybookTactic, error) {
	if limit <= 0 {
		limit = 8
	}
	var out []PlaybookTactic
	err := r.db.WithContext(ctx).
		Where("enabled = ?", true).
		Order("priority ASC").
		Order("hit_count DESC").
		Order("title ASC").
		Limit(limit).
		Find(&out).Error
	return out, err
}

// RecordPlaybookHits 记录一次成功探索命中的通用经验。
func (r *Repository) RecordPlaybookHits(ctx context.Context, keys []string) error {
	now := time.Now().UTC()
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if err := r.db.WithContext(ctx).
			Model(&PlaybookTactic{}).
			Where("tactic_key = ?", key).
			Updates(map[string]any{
				"hit_count":    gorm.Expr("hit_count + 1"),
				"last_used_at": now,
				"updated_at":   now,
			}).Error; err != nil {
			return err
		}
	}
	return nil
}
