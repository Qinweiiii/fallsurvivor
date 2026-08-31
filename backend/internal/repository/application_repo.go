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

// ApplicationRepo 负责投递任务、字段与事件。
type ApplicationRepo struct{ db *gorm.DB }

// ApplicationFilter 是投递列表筛选条件。
type ApplicationFilter struct {
	Statuses     []string
	Keyword      string
	Company      string
	OnlyActive   bool // 只看未结束
	OnlyFinished bool // 只看已结束
}

func applyAppFilter(q *gorm.DB, f ApplicationFilter) *gorm.DB {
	if len(f.Statuses) > 0 {
		q = q.Where("applications.status IN ?", f.Statuses)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(jobs.company_name ILIKE ? OR jobs.title ILIKE ?)", like, like)
	}
	if c := strings.TrimSpace(f.Company); c != "" {
		q = q.Where("jobs.company_name ILIKE ?", "%"+c+"%")
	}
	terminal := []string{model.AppStatusOffer, model.AppStatusRejected, model.AppStatusWithdrawn}
	if f.OnlyActive {
		q = q.Where("applications.status NOT IN ?", terminal)
	}
	if f.OnlyFinished {
		q = q.Where("applications.status IN ?", terminal)
	}
	return q
}

// List 分页查询投递任务，未结束的排在前面。
func (r *ApplicationRepo) List(ctx context.Context, userID model.ID, f ApplicationFilter, p pagination.Query) ([]model.Application, int64, error) {
	countQ := applyAppFilter(
		r.db.WithContext(ctx).Model(&model.Application{}).
			Joins("JOIN jobs ON jobs.id = applications.job_id").
			Where("applications.user_id = ?", userID),
		f,
	)
	var total int64
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []model.Application{}, 0, nil
	}

	// 结束状态排到最后，其余按更新时间倒序。
	orderExpr := `CASE WHEN applications.status IN ('OFFER','REJECTED','WITHDRAWN') THEN 1 ELSE 0 END ASC,
	              applications.updated_at DESC`

	var items []model.Application
	err := applyAppFilter(
		r.db.WithContext(ctx).Model(&model.Application{}).
			Joins("JOIN jobs ON jobs.id = applications.job_id").
			Where("applications.user_id = ?", userID),
		f,
	).Preload("Job").
		Order(orderExpr).
		Limit(p.Limit()).Offset(p.Offset()).
		Find(&items).Error
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetByID 查询投递任务（含岗位）。
func (r *ApplicationRepo) GetByID(ctx context.Context, userID, id model.ID) (*model.Application, error) {
	var a model.Application
	err := r.db.WithContext(ctx).Preload("Job").
		Where("id = ? AND user_id = ?", id, userID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// FindByJobID 按岗位查询投递任务。
func (r *ApplicationRepo) FindByJobID(ctx context.Context, userID, jobID model.ID) (*model.Application, error) {
	var a model.Application
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND job_id = ?", userID, jobID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// Create 新建投递任务，若已存在则返回已有记录。
func (r *ApplicationRepo) Create(ctx context.Context, a *model.Application) (created bool, err error) {
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "job_id"}},
		DoNothing: true,
	}).Create(a)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// UpdateFields 更新投递任务字段（调用方必须已通过状态机校验）。
func (r *ApplicationRepo) UpdateFields(ctx context.Context, id model.ID, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Application{}).
		Where("id = ?", id).Updates(fields).Error
}

// CountByStatus 返回各状态的任务数量。
func (r *ApplicationRepo) CountByStatus(ctx context.Context, userID model.ID) (map[string]int64, error) {
	type row struct {
		Status string
		N      int64
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&model.Application{}).
		Select("status, COUNT(*) AS n").
		Where("user_id = ?", userID).
		Group("status").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, x := range rows {
		out[x.Status] = x.N
	}
	return out, nil
}

// ---------------- 表单字段 ----------------

// ReplaceFields 覆盖式写入表单字段分析结果。
func (r *ApplicationRepo) ReplaceFields(ctx context.Context, appID model.ID, fields []model.ApplicationField) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("application_id = ?", appID).Delete(&model.ApplicationField{}).Error; err != nil {
			return err
		}
		if len(fields) == 0 {
			return nil
		}
		return tx.Create(&fields).Error
	})
}

// ListFields 查询表单字段。
func (r *ApplicationRepo) ListFields(ctx context.Context, appID model.ID) ([]model.ApplicationField, error) {
	// 空切片兜底，避免序列化为 null。
	items := make([]model.ApplicationField, 0, 32)
	err := r.db.WithContext(ctx).Where("application_id = ?", appID).
		Order("is_sensitive DESC, created_at ASC").Find(&items).Error
	return items, err
}

// ---------------- 事件 ----------------

// AddEvent 追加一条事件。
func (r *ApplicationRepo) AddEvent(ctx context.Context, e *model.ApplicationEvent) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// ListEvents 查询某任务的事件流。
func (r *ApplicationRepo) ListEvents(ctx context.Context, appID model.ID, limit int) ([]model.ApplicationEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// 空切片兜底，避免序列化为 null。
	items := make([]model.ApplicationEvent, 0, limit)
	err := r.db.WithContext(ctx).Where("application_id = ?", appID).
		Order("created_at DESC").Limit(limit).Find(&items).Error
	return items, err
}

// RecentEvent 是仪表盘最近动态的展示结构。
type RecentEvent struct {
	ID          model.ID  `json:"id"`
	EventType   string    `json:"event_type"`
	Description string    `json:"description"`
	CompanyName string    `json:"company_name"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListRecentEvents 查询全局最近事件。
func (r *ApplicationRepo) ListRecentEvents(ctx context.Context, limit int) ([]RecentEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	// 初始化为空切片而非 nil：nil 会被序列化为 null，前端遍历时崩溃。
	items := make([]RecentEvent, 0, limit)
	err := r.db.WithContext(ctx).
		Table("application_events AS e").
		Select("e.id, e.event_type, e.description, COALESCE(j.company_name,'') AS company_name, COALESCE(j.title,'') AS title, e.created_at").
		Joins("LEFT JOIN applications AS a ON a.id = e.application_id").
		Joins("LEFT JOIN jobs AS j ON j.id = COALESCE(e.job_id, a.job_id)").
		Order("e.created_at DESC").Limit(limit).Scan(&items).Error
	return items, err
}

// ---------------------------------------------------------------------------

// BrowserRepo 负责浏览器任务。
type BrowserRepo struct{ db *gorm.DB }

// Create 新建浏览器任务。
func (r *BrowserRepo) Create(ctx context.Context, t *model.BrowserTask) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// GetByID 查询浏览器任务。
func (r *BrowserRepo) GetByID(ctx context.Context, id model.ID) (*model.BrowserTask, error) {
	var t model.BrowserTask
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetLatestByApplication 查询某投递任务最近的浏览器任务。
func (r *BrowserRepo) GetLatestByApplication(ctx context.Context, appID model.ID) (*model.BrowserTask, error) {
	var t model.BrowserTask
	err := r.db.WithContext(ctx).Where("application_id = ?", appID).
		Order("created_at DESC").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// FindActiveByApplication 查询是否存在未结束的浏览器任务。
func (r *BrowserRepo) FindActiveByApplication(ctx context.Context, appID model.ID) (*model.BrowserTask, error) {
	var t model.BrowserTask
	err := r.db.WithContext(ctx).
		Where("application_id = ? AND status IN ?", appID, []string{
			model.BrowserTaskPending, model.BrowserTaskRunning,
			model.BrowserTaskWaitingUser, model.BrowserTaskPaused,
		}).Order("created_at DESC").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Update 更新浏览器任务字段。
func (r *BrowserRepo) Update(ctx context.Context, id model.ID, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.BrowserTask{}).
		Where("id = ?", id).Updates(fields).Error
}

// ---------------------------------------------------------------------------

// ResumeRepo 负责简历。
type ResumeRepo struct{ db *gorm.DB }

// Create 新建简历记录。
func (r *ResumeRepo) Create(ctx context.Context, m *model.Resume) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// List 查询用户的全部简历。
func (r *ResumeRepo) List(ctx context.Context, userID model.ID) ([]model.Resume, error) {
	var items []model.Resume
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC").Find(&items).Error
	return items, err
}

// GetByID 查询简历，强制校验归属。
func (r *ResumeRepo) GetByID(ctx context.Context, userID, id model.ID) (*model.Resume, error) {
	var m model.Resume
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetCurrent 查询当前生效的简历。
func (r *ResumeRepo) GetCurrent(ctx context.Context, userID model.ID) (*model.Resume, error) {
	var m model.Resume
	err := r.db.WithContext(ctx).Where("user_id = ? AND is_current = true", userID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// SetCurrent 设置当前简历，保证同一用户只有一份。
func (r *ResumeRepo) SetCurrent(ctx context.Context, userID, id model.ID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Resume{}).
			Where("user_id = ? AND is_current = true", userID).
			Update("is_current", false).Error; err != nil {
			return err
		}
		res := tx.Model(&model.Resume{}).
			Where("id = ? AND user_id = ?", id, userID).
			Update("is_current", true)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Update 更新简历解析结果。
func (r *ResumeRepo) Update(ctx context.Context, id model.ID, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Resume{}).
		Where("id = ?", id).Updates(fields).Error
}

// Delete 删除简历记录。
func (r *ResumeRepo) Delete(ctx context.Context, userID, id model.ID) error {
	res := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&model.Resume{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
