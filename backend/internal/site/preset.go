package site

import (
	"context"
	"log/slog"

	"gorm.io/gorm/clause"
)

// PresetRecipes 是系统内置的站点 Recipe。
//
// 预置浏览器脚本已退役。招聘站点应通过首次 Explorer 探索产生
// browser_observed Recipe，而不是由代码为公司预填采集路径。
func PresetRecipes() []Recipe {
	return nil
}

// SeedPresets 把内置预置 Recipe 写入数据库（仅当 site_key 不存在时）。
//
// 在系统启动时调用。幂等：重复调用不会产生重复记录，
// 也不会覆盖用户之后修改过的配置。
func (r *Repository) SeedPresets(ctx context.Context) error {
	for _, preset := range PresetRecipes() {
		existing, err := r.GetBySiteKey(ctx, preset.SiteKey)
		if err != nil {
			return err
		}
		if existing != nil {
			// 已存在（可能是用户改过的），保持原样。
			continue
		}
		toCreate := preset
		if err := r.Create(ctx, &toCreate); err != nil {
			return err
		}
		slog.Info("已写入内置站点 Recipe", "site_key", preset.SiteKey, "company", preset.CompanyName)
	}
	return nil
}

// SeedPlaybook 写入内置探索经验。只新增缺失项，不覆盖用户调整过的排序和描述。
func (r *Repository) SeedPlaybook(ctx context.Context) error {
	for _, tactic := range PresetPlaybook() {
		toCreate := tactic
		if err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tactic_key"}}, DoNothing: true}).
			Create(&toCreate).Error; err != nil {
			return err
		}
	}
	slog.Info("探索 Playbook 初始化完成", "count", len(PresetPlaybook()))
	return nil
}
