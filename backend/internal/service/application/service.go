package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
)

// ErrNotFound 表示投递任务不存在。
var ErrNotFound = repository.ErrNotFound

// Service 是投递任务服务。
type Service struct {
	store *repository.Store
}

// NewService 创建服务。
func NewService(store *repository.Store) *Service {
	return &Service{store: store}
}

// CreateResult 是批量创建的结果。
type CreateResult struct {
	Created  int        `json:"created"`
	Existing int        `json:"existing"`
	Skipped  int        `json:"skipped"`
	IDs      []model.ID `json:"ids"`
}

// CreateFromJobs 为指定岗位创建投递任务。
//
// 说明：只允许为岗位车中的岗位创建投递任务，
// 这是产品文档「岗位车 = 用户明确表示要投」这一约定的强制落地。
func (s *Service) CreateFromJobs(ctx context.Context, userID model.ID, jobIDs []model.ID) (*CreateResult, error) {
	if len(jobIDs) == 0 {
		return &CreateResult{IDs: []model.ID{}}, nil
	}

	cartIDs, err := s.store.Cart.ListJobIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	inCart := make(map[model.ID]bool, len(cartIDs))
	for _, id := range cartIDs {
		inCart[id] = true
	}

	jobs, err := s.store.Job.GetManyByIDs(ctx, jobIDs)
	if err != nil {
		return nil, err
	}
	jobByID := make(map[model.ID]*model.Job, len(jobs))
	for i := range jobs {
		jobByID[jobs[i].ID] = &jobs[i]
	}

	out := &CreateResult{IDs: make([]model.ID, 0, len(jobIDs))}
	now := time.Now().UTC()

	for _, jobID := range jobIDs {
		job, ok := jobByID[jobID]
		if !ok || !inCart[jobID] {
			out.Skipped++
			continue
		}

		app := &model.Application{
			UserID:         userID,
			JobID:          jobID,
			Status:         model.AppStatusPreparing,
			Progress:       progressOf(model.AppStatusPreparing),
			ApplicationURL: firstNonEmpty(job.OfficialURL, job.SourceURL),
			StartedAt:      &now,
			LastActionAt:   &now,
		}

		created, err := s.store.Application.Create(ctx, app)
		if err != nil {
			return nil, err
		}
		if !created {
			existing, err := s.store.Application.FindByJobID(ctx, userID, jobID)
			if err != nil {
				return nil, err
			}
			out.Existing++
			out.IDs = append(out.IDs, existing.ID)
			continue
		}

		out.Created++
		out.IDs = append(out.IDs, app.ID)

		jid := jobID
		_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
			ApplicationID: &app.ID,
			JobID:         &jid,
			EventType:     model.EventApplicationCreated,
			Description:   fmt.Sprintf("创建投递任务：%s - %s", job.CompanyName, job.Title),
			ToStatus:      model.AppStatusPreparing,
		})
		// 岗位状态同步为「已准备投递」。
		_ = s.store.Job.UpdateStatus(ctx, []model.ID{jobID}, model.JobStatusPreparing)
	}

	return out, nil
}

// List 分页查询投递任务。
func (s *Service) List(ctx context.Context, userID model.ID, f repository.ApplicationFilter, p pagination.Query) ([]model.Application, int64, error) {
	return s.store.Application.List(ctx, userID, f, p)
}

// Detail 是投递任务详情。
type Detail struct {
	Application  *model.Application       `json:"application"`
	Job          *model.Job               `json:"job"`
	BrowserTask  *model.BrowserTask       `json:"browser_task"`
	Fields       []model.ApplicationField `json:"fields"`
	Events       []model.ApplicationEvent `json:"events"`
	AllowedNext  []string                 `json:"allowed_next"`
	FieldSummary FieldSummary             `json:"field_summary"`
}

// FieldSummary 是表单完成度统计。
type FieldSummary struct {
	Total         int      `json:"total"`
	Filled        int      `json:"filled"`
	Skipped       int      `json:"skipped"`
	Sensitive     int      `json:"sensitive"`
	PendingLabels []string `json:"pending_labels"`
}

// GetDetail 返回投递任务详情。
func (s *Service) GetDetail(ctx context.Context, userID, id model.ID) (*Detail, error) {
	app, err := s.store.Application.GetByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}

	d := &Detail{
		Application: app,
		Job:         app.Job,
		AllowedNext: AllowedNextStatuses(app.Status),
	}

	if bt, err := s.store.Browser.GetLatestByApplication(ctx, id); err == nil {
		d.BrowserTask = bt
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	fields, err := s.store.Application.ListFields(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Fields = fields
	d.FieldSummary = summarizeFields(fields)

	events, err := s.store.Application.ListEvents(ctx, id, 50)
	if err != nil {
		return nil, err
	}
	d.Events = events

	return d, nil
}

// summarizeFields 统计表单完成度。
func summarizeFields(fields []model.ApplicationField) FieldSummary {
	s := FieldSummary{Total: len(fields), PendingLabels: []string{}}
	for _, f := range fields {
		switch {
		case f.IsFilled:
			s.Filled++
		default:
			s.Skipped++
			if f.IsSensitive {
				s.Sensitive++
			}
			if f.FieldLabel != "" {
				s.PendingLabels = append(s.PendingLabels, f.FieldLabel)
			}
		}
	}
	return s
}

// UpdateStatus 变更投递状态，全部跃迁必须经过状态机校验。
func (s *Service) UpdateStatus(ctx context.Context, userID, id model.ID, to, note string) (*model.Application, error) {
	app, err := s.store.Application.GetByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if err := ValidateTransition(app.Status, to); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	fields := map[string]any{
		"status":         to,
		"progress":       progressOf(to),
		"last_action_at": now,
	}
	if to == model.AppStatusSubmitted && app.SubmittedAt == nil {
		fields["submitted_at"] = now
	}
	if note != "" {
		fields["note"] = note
	}

	if err := s.store.Application.UpdateFields(ctx, id, fields); err != nil {
		return nil, err
	}

	desc := fmt.Sprintf("状态变更：%s → %s", label(app.Status), label(to))
	if note != "" {
		desc += "（" + note + "）"
	}
	jid := app.JobID
	_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
		ApplicationID: &id,
		JobID:         &jid,
		EventType:     eventTypeFor(to),
		Description:   desc,
		FromStatus:    app.Status,
		ToStatus:      to,
	})

	// 同步岗位状态。
	s.syncJobStatus(ctx, app.JobID, to)

	app.Status = to
	app.Progress = progressOf(to)
	app.LastActionAt = &now
	return app, nil
}

// MarkSubmitted 把任务标记为已投递。
//
// 语义（依据 API 文档第 11 节）：
// 用户已经在招聘网站上真实完成了提交，此接口只更新本系统的记录状态。
// 系统绝不代替用户点击招聘网站的提交按钮。
func (s *Service) MarkSubmitted(ctx context.Context, userID, id model.ID) (*model.Application, error) {
	app, err := s.store.Application.GetByID(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if app.Status == model.AppStatusSubmitted {
		return app, nil
	}
	if err := ValidateTransition(app.Status, model.AppStatusSubmitted); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := s.store.Application.UpdateFields(ctx, id, map[string]any{
		"status":         model.AppStatusSubmitted,
		"progress":       100,
		"submitted_at":   now,
		"last_action_at": now,
	}); err != nil {
		return nil, err
	}

	jid := app.JobID
	_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
		ApplicationID: &id,
		JobID:         &jid,
		EventType:     model.EventUserSubmitted,
		Description:   "用户已在招聘网站完成提交，系统标记为已投递",
		FromStatus:    app.Status,
		ToStatus:      model.AppStatusSubmitted,
	})

	// 已投递的岗位从岗位车移出，保持岗位车只放待处理岗位。
	_, _ = s.store.Cart.Remove(ctx, userID, []model.ID{app.JobID})
	_ = s.store.Job.UpdateStatus(ctx, []model.ID{app.JobID}, model.JobStatusSubmitted)

	app.Status = model.AppStatusSubmitted
	app.Progress = 100
	app.SubmittedAt = &now
	return app, nil
}

// syncJobStatus 依据投递状态同步岗位状态。
func (s *Service) syncJobStatus(ctx context.Context, jobID model.ID, appStatus string) {
	var jobStatus string
	switch {
	case model.IsSubmittedOrBeyond(appStatus):
		jobStatus = model.JobStatusSubmitted
	case appStatus == model.AppStatusWithdrawn:
		jobStatus = model.JobStatusClosed
	default:
		jobStatus = model.JobStatusPreparing
	}
	_ = s.store.Job.UpdateStatus(ctx, []model.ID{jobID}, jobStatus)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
