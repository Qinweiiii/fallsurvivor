package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("repository: 记录不存在")

// UserRepo 负责用户、求职画像与申请信息。
type UserRepo struct{ db *gorm.DB }

// GetDefaultUser 返回系统中唯一的用户（第一版单用户）。
func (r *UserRepo) GetDefaultUser(ctx context.Context) (*model.UserProfile, error) {
	var u model.UserProfile
	err := r.db.WithContext(ctx).Order("created_at ASC").First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser 新建用户。
func (r *UserRepo) CreateUser(ctx context.Context, u *model.UserProfile) error {
	return r.db.WithContext(ctx).Create(u).Error
}

// UpdateUser 更新用户基础信息。
func (r *UserRepo) UpdateUser(ctx context.Context, u *model.UserProfile) error {
	return r.db.WithContext(ctx).Model(&model.UserProfile{}).
		Where("id = ?", u.ID).
		Updates(map[string]any{"name": u.Name, "email": u.Email, "phone": u.Phone}).Error
}

// GetProfile 获取求职画像。
func (r *UserRepo) GetProfile(ctx context.Context, userID model.ID) (*model.JobProfile, error) {
	var p model.JobProfile
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertProfile 创建或更新求职画像。
func (r *UserRepo) UpsertProfile(ctx context.Context, p *model.JobProfile) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"target_roles", "preferred_languages", "preferred_locations",
			"company_preferences", "target_industries", "graduation_year", "updated_at",
		}),
	}).Create(p).Error
}

// GetApplicationProfile 获取可复用申请信息。
func (r *UserRepo) GetApplicationProfile(ctx context.Context, userID model.ID) (*model.ApplicationProfile, error) {
	var p model.ApplicationProfile
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// UpsertApplicationProfile 创建或更新可复用申请信息。
func (r *UserRepo) UpsertApplicationProfile(ctx context.Context, p *model.ApplicationProfile) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"profile_data", "updated_at"}),
	}).Create(p).Error
}
