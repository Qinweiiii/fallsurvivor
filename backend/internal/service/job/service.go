// Package job 负责岗位查询与岗位车相关业务逻辑。
package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
)

// Service 是岗位服务。
type Service struct {
	store *repository.Store
}

// NewService 创建服务。
func NewService(store *repository.Store) *Service {
	return &Service{store: store}
}

// ListItem 是岗位列表项，附带岗位车状态。
type ListItem struct {
	model.Job
	InCart bool `json:"in_cart"`
}

// List 分页查询岗位。
func (s *Service) List(ctx context.Context, userID model.ID, f repository.JobFilter, p pagination.Query) ([]ListItem, int64, error) {
	jobs, total, err := s.store.Job.List(ctx, f, p)
	if err != nil {
		return nil, 0, err
	}

	cartIDs, err := s.store.Cart.ListJobIDs(ctx, userID)
	if err != nil {
		return nil, 0, err
	}
	inCart := make(map[model.ID]bool, len(cartIDs))
	for _, id := range cartIDs {
		inCart[id] = true
	}

	items := make([]ListItem, 0, len(jobs))
	for _, j := range jobs {
		sanitizeJobOutput(&j)
		items = append(items, ListItem{Job: j, InCart: inCart[j.ID]})
	}
	return items, total, nil
}

// Detail 是岗位详情。
type Detail struct {
	model.Job
	Sources []model.JobSource `json:"sources"`
	InCart  bool              `json:"in_cart"`
	// ApplicationID 若该岗位已创建投递任务则返回其 ID。
	ApplicationID *model.ID `json:"application_id"`
}

// GetDetail 返回岗位详情。
func (s *Service) GetDetail(ctx context.Context, userID, jobID model.ID) (*Detail, error) {
	j, err := s.store.Job.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	sanitizeJobOutput(j)

	sources, err := s.store.Job.ListSources(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if sources == nil {
		sources = []model.JobSource{}
	}

	inCart, err := s.store.Cart.Exists(ctx, userID, jobID)
	if err != nil {
		return nil, err
	}

	d := &Detail{Job: *j, Sources: sources, InCart: inCart}
	if app, err := s.store.Application.FindByJobID(ctx, userID, jobID); err == nil {
		d.ApplicationID = &app.ID
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	return d, nil
}

func sanitizeJobOutput(j *model.Job) {
	if j == nil {
		return
	}
	j.CompanyName = cleanOutputScalar(j.CompanyName)
	j.Department = cleanOutputScalar(j.Department)
	j.Business = cleanOutputScalar(j.Business)
	j.Location = cleanOutputScalar(j.Location)
	j.JobType = cleanOutputScalar(j.JobType)
	j.MatchAnalysis = sanitizeJSONMap(j.MatchAnalysis)
}

func cleanOutputScalar(v string) string {
	t := strings.TrimSpace(v)
	switch strings.ToLower(t) {
	case "", "<nil>", "nil", "null", "undefined":
		return ""
	default:
		return t
	}
}

func sanitizeJSONMap(in model.JSONMap) model.JSONMap {
	if len(in) == 0 {
		return in
	}
	out := make(model.JSONMap, len(in))
	for k, v := range in {
		cleaned, ok := sanitizeJSONValue(v)
		if ok {
			out[k] = cleaned
		}
	}
	return out
}

func sanitizeJSONValue(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		cleaned := cleanAnalysisText(x)
		return cleaned, cleaned != ""
	case []any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			cleaned, ok := sanitizeJSONValue(item)
			if ok {
				out = append(out, cleaned)
			}
		}
		return out, true
	case []string:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if cleaned := cleanAnalysisText(item); cleaned != "" {
				out = append(out, cleaned)
			}
		}
		return out, true
	case map[string]any:
		return sanitizeJSONMap(model.JSONMap(x)), true
	case model.JSONMap:
		return sanitizeJSONMap(x), true
	default:
		return v, true
	}
}

func cleanAnalysisText(v string) string {
	t := cleanOutputScalar(v)
	if t == "" {
		return ""
	}
	if endsWithPlaceholderValue(t) {
		return ""
	}
	replacer := strings.NewReplacer(
		"<nil>", "",
		"<Nil>", "",
		"<NULL>", "",
		" nil ", " ",
		" null ", " ",
		" undefined ", " ",
	)
	t = strings.TrimSpace(replacer.Replace(t))
	t = strings.TrimRight(t, "：:，,、；;。 .")
	return strings.TrimSpace(t)
}

func endsWithPlaceholderValue(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, placeholder := range []string{"<nil>", "nil", "null", "undefined"} {
		for _, sep := range []string{"：", ":"} {
			if strings.HasSuffix(lower, sep+placeholder) {
				return true
			}
		}
	}
	return false
}

// FilterOptions 是筛选器的可选值。
type FilterOptions struct {
	Companies []string `json:"companies"`
	Locations []string `json:"locations"`
	Statuses  []string `json:"statuses"`
	Sources   []string `json:"sources"`
	Languages []string `json:"languages"`
}

// commonLanguages 是筛选器提供的语言选项。
var commonLanguages = []string{"Go", "Python", "Java", "C++", "Rust", "TypeScript"}

// GetFilterOptions 返回筛选器可选值。
func (s *Service) GetFilterOptions(ctx context.Context) (*FilterOptions, error) {
	companies, err := s.store.Job.DistinctCompanies(ctx, 200)
	if err != nil {
		return nil, err
	}
	locations, err := s.store.Job.DistinctLocations(ctx, 200)
	if err != nil {
		return nil, err
	}
	return &FilterOptions{
		Companies: companies,
		Locations: locations,
		Statuses:  model.AllJobStatuses,
		Sources:   model.AllSourceTypes,
		Languages: commonLanguages,
	}, nil
}

// CartResult 是岗位车批量操作的结果。
type CartResult struct {
	Added    int `json:"added"`
	Existing int `json:"existing"`
	Skipped  int `json:"skipped"`
}

// AddToCart 批量加入岗位车。
func (s *Service) AddToCart(ctx context.Context, userID model.ID, jobIDs []model.ID) (*CartResult, error) {
	if len(jobIDs) == 0 {
		return &CartResult{}, nil
	}

	jobs, err := s.store.Job.GetManyByIDs(ctx, jobIDs)
	if err != nil {
		return nil, err
	}
	valid := make(map[model.ID]*model.Job, len(jobs))
	for i := range jobs {
		valid[jobs[i].ID] = &jobs[i]
	}

	out := &CartResult{}
	added := make([]model.ID, 0, len(jobIDs))

	for _, id := range jobIDs {
		j, ok := valid[id]
		if !ok {
			out.Skipped++
			continue
		}
		created, err := s.store.Cart.Add(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		if !created {
			out.Existing++
			continue
		}
		out.Added++
		added = append(added, id)

		jid := id
		_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
			JobID:       &jid,
			EventType:   model.EventJobAddedToCart,
			Description: fmt.Sprintf("加入岗位车：%s - %s", j.CompanyName, j.Title),
		})
	}

	// 只把仍处于 NEW 的岗位改为 IN_CART，避免覆盖更靠后的状态。
	if len(added) > 0 {
		for _, id := range added {
			if j := valid[id]; j != nil && j.Status == model.JobStatusNew {
				_ = s.store.Job.UpdateStatus(ctx, []model.ID{id}, model.JobStatusInCart)
			}
		}
	}
	return out, nil
}

// RemoveFromCart 批量移出岗位车。
func (s *Service) RemoveFromCart(ctx context.Context, userID model.ID, jobIDs []model.ID) (int64, error) {
	n, err := s.store.Cart.Remove(ctx, userID, jobIDs)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}

	jobs, err := s.store.Job.GetManyByIDs(ctx, jobIDs)
	if err != nil {
		return n, nil
	}
	for i := range jobs {
		j := jobs[i]
		// 只回退尚未进入投递流程的岗位。
		if j.Status == model.JobStatusInCart {
			_ = s.store.Job.UpdateStatus(ctx, []model.ID{j.ID}, model.JobStatusNew)
		}
		jid := j.ID
		_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
			JobID:       &jid,
			EventType:   model.EventJobRemovedFromCart,
			Description: fmt.Sprintf("移出岗位车：%s - %s", j.CompanyName, j.Title),
		})
	}
	return n, nil
}

// ListCart 分页查询岗位车。
func (s *Service) ListCart(ctx context.Context, userID model.ID, p pagination.Query) ([]repository.CartItem, int64, error) {
	return s.store.Cart.List(ctx, userID, p)
}

// ---------------- Dashboard ----------------

// DashboardStats 是仪表盘统计数据。
type DashboardStats struct {
	Jobs struct {
		Total int64 `json:"total"`
		New   int64 `json:"new"`
	} `json:"jobs"`
	Cart         int64 `json:"cart"`
	Applications int64 `json:"applications"`
	Pending      int64 `json:"pending"`
	Submitted    int64 `json:"submitted"`
	WrittenTest  int64 `json:"written_test"`
	Interview    int64 `json:"interview"`
	Offer        int64 `json:"offer"`
	Rejected     int64 `json:"rejected"`
}

// DashboardData 是仪表盘完整响应。
type DashboardData struct {
	Stats             DashboardStats           `json:"stats"`
	RecentHighMatch   []model.Job              `json:"recent_high_match"`
	UpcomingDeadlines []DeadlineItem           `json:"upcoming_deadlines"`
	RecentEvents      []repository.RecentEvent `json:"recent_events"`
	LatestSearchTask  *model.SearchTask        `json:"latest_search_task"`
}

// DeadlineItem 是即将截止的岗位。
type DeadlineItem struct {
	JobID       model.ID  `json:"job_id"`
	CompanyName string    `json:"company_name"`
	Title       string    `json:"title"`
	Deadline    time.Time `json:"deadline"`
	// DaysLeft 是剩余天数，由服务端统一计算避免前端时区偏差。
	DaysLeft int `json:"days_left"`
}

// GetDashboard 汇总仪表盘数据。
func (s *Service) GetDashboard(ctx context.Context, userID model.ID) (*DashboardData, error) {
	out := &DashboardData{
		RecentHighMatch:   []model.Job{},
		UpcomingDeadlines: []DeadlineItem{},
		RecentEvents:      []repository.RecentEvent{},
	}

	total, err := s.store.Job.CountAll(ctx)
	if err != nil {
		return nil, err
	}
	out.Stats.Jobs.Total = total

	newJobs, err := s.store.Job.CountCreatedAfter(ctx, time.Now().UTC().Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	out.Stats.Jobs.New = newJobs

	cartCount, err := s.store.Cart.Count(ctx, userID)
	if err != nil {
		return nil, err
	}
	out.Stats.Cart = cartCount

	byStatus, err := s.store.Application.CountByStatus(ctx, userID)
	if err != nil {
		return nil, err
	}
	for status, n := range byStatus {
		out.Stats.Applications += n
		switch {
		case model.IsInterviewStatus(status):
			out.Stats.Interview += n
		case status == model.AppStatusWrittenTest:
			out.Stats.WrittenTest += n
		case status == model.AppStatusOffer:
			out.Stats.Offer += n
		case status == model.AppStatusRejected:
			out.Stats.Rejected += n
		}
		if model.IsSubmittedOrBeyond(status) {
			out.Stats.Submitted += n
		} else if status != model.AppStatusWithdrawn {
			out.Stats.Pending += n
		}
	}

	if jobs, err := s.store.Job.ListRecentHighMatch(ctx, 85, 5); err == nil {
		out.RecentHighMatch = jobs
	}

	if jobs, err := s.store.Job.ListUpcomingDeadlines(ctx, 30*24*time.Hour, 8); err == nil {
		now := time.Now().UTC()
		for _, j := range jobs {
			if j.Deadline == nil {
				continue
			}
			out.UpcomingDeadlines = append(out.UpcomingDeadlines, DeadlineItem{
				JobID:       j.ID,
				CompanyName: j.CompanyName,
				Title:       j.Title,
				Deadline:    *j.Deadline,
				DaysLeft:    int(j.Deadline.Sub(now).Hours() / 24),
			})
		}
	}

	if events, err := s.store.Application.ListRecentEvents(ctx, 12); err == nil {
		out.RecentEvents = events
	}

	if t, err := s.store.SearchTask.GetLatest(ctx, userID); err == nil {
		out.LatestSearchTask = t
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	return out, nil
}
