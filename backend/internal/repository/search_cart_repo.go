package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
)

// SearchTaskRepo 负责搜索任务。
type SearchTaskRepo struct{ db *gorm.DB }

// Create 新建搜索任务。
func (r *SearchTaskRepo) Create(ctx context.Context, t *model.SearchTask) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetByID 查询搜索任务。
func (r *SearchTaskRepo) GetByID(ctx context.Context, id model.ID) (*model.SearchTask, error) {
	var t model.SearchTask
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Update 更新搜索任务的进度与结果字段。
func (r *SearchTaskRepo) Update(ctx context.Context, id model.ID, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.SearchTask{}).
		Where("id = ?", id).Updates(fields).Error
}

// FindActive 查询用户是否已有进行中的搜索任务，避免重复触发。
func (r *SearchTaskRepo) FindActive(ctx context.Context, userID model.ID) (*model.SearchTask, error) {
	var t model.SearchTask
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID,
			[]string{model.SearchTaskPending, model.SearchTaskRunning}).
		Order("created_at DESC").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetLatest 返回最近一次搜索任务。
func (r *SearchTaskRepo) GetLatest(ctx context.Context, userID model.ID) (*model.SearchTask, error) {
	var t model.SearchTask
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ReclaimStale 把超时仍在运行的任务标记为失败，用于进程重启后的清理。
func (r *SearchTaskRepo) ReclaimStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	res := r.db.WithContext(ctx).Model(&model.SearchTask{}).
		Where("status IN ? AND created_at < ?",
			[]string{model.SearchTaskPending, model.SearchTaskRunning}, cutoff).
		Updates(map[string]any{
			"status":        model.SearchTaskFailed,
			"error_message": "任务超时未完成，已被系统回收",
			"finished_at":   time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// ReclaimToTerminated 把所有未结束（PENDING/RUNNING）的任务标记为 TERMINATED。
// 用于服务关闭/重启时清理残留任务，避免任务永远停在「进行中」。
func (r *SearchTaskRepo) ReclaimToTerminated(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.SearchTask{}).
		Where("status IN ?",
			[]string{model.SearchTaskPending, model.SearchTaskRunning}).
		Updates(map[string]any{
			"status":        model.SearchTaskTerminated,
			"error_message": "服务关闭或重启，任务已被终止",
			"finished_at":   time.Now().UTC(),
		})
	return res.RowsAffected, res.Error
}

// ---------------------------------------------------------------------------

// CartRepo 负责岗位车。
type CartRepo struct{ db *gorm.DB }

// Add 加入岗位车，重复加入视为幂等成功。
// 返回值表示是否新增。
func (r *CartRepo) Add(ctx context.Context, userID, jobID model.ID) (bool, error) {
	item := &model.JobCart{UserID: userID, JobID: jobID}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "job_id"}},
		DoNothing: true,
	}).Create(item)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// Remove 移出岗位车。
func (r *CartRepo) Remove(ctx context.Context, userID model.ID, jobIDs []model.ID) (int64, error) {
	if len(jobIDs) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("user_id = ? AND job_id IN ?", userID, jobIDs).
		Delete(&model.JobCart{})
	return res.RowsAffected, res.Error
}

// CartItem 是岗位车条目与岗位的组合视图。
type CartItem struct {
	model.Job
	AddedAt time.Time `json:"added_at"`
}

// List 分页查询岗位车。
func (r *CartRepo) List(ctx context.Context, userID model.ID, p pagination.Query) ([]CartItem, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.JobCart{}).
		Where("user_id = ?", userID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []CartItem{}, 0, nil
	}

	var items []CartItem
	err := r.db.WithContext(ctx).
		Table("job_carts AS c").
		Select("j.*, c.created_at AS added_at").
		Joins("JOIN jobs AS j ON j.id = c.job_id").
		Where("c.user_id = ?", userID).
		Order("c.created_at DESC").
		Limit(p.Limit()).Offset(p.Offset()).
		Scan(&items).Error
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListJobIDs 返回岗位车中的全部岗位 ID。
func (r *CartRepo) ListJobIDs(ctx context.Context, userID model.ID) ([]model.ID, error) {
	var ids []model.ID
	err := r.db.WithContext(ctx).Model(&model.JobCart{}).
		Where("user_id = ?", userID).Pluck("job_id", &ids).Error
	return ids, err
}

// Count 返回岗位车数量。
func (r *CartRepo) Count(ctx context.Context, userID model.ID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.JobCart{}).
		Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

// Exists 判断岗位是否已在车中。
func (r *CartRepo) Exists(ctx context.Context, userID, jobID model.ID) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.JobCart{}).
		Where("user_id = ? AND job_id = ?", userID, jobID).Count(&n).Error
	return n > 0, err
}
