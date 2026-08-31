package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/security"
	appsvc "github.com/eddiel/fallsurvivor/backend/internal/service/application"
	"github.com/eddiel/fallsurvivor/backend/internal/service/profile"
)

// Service 编排浏览器辅助填写流程。
type Service struct {
	store      *repository.Store
	client     *Client
	llm        *ai.Client
	profileSvc *profile.Service
	appSvc     *appsvc.Service
}

// NewService 创建服务。
func NewService(
	store *repository.Store,
	client *Client,
	llm *ai.Client,
	profileSvc *profile.Service,
	appSvc *appsvc.Service,
) *Service {
	return &Service{store: store, client: client, llm: llm, profileSvc: profileSvc, appSvc: appSvc}
}

// StartResult 是启动结果。
type StartResult struct {
	BrowserTask *model.BrowserTask `json:"browser_task"`
	NeedsLogin  bool               `json:"needs_login"`
	Message     string             `json:"message"`
}

// Start 启动浏览器辅助任务。
//
// 流程严格按照设计文档第 2 节：
// 打开页面 → 检查登录 → （需要则暂停等用户登录）→ 识别表单 →
// 字段映射 → 填写普通字段 → 敏感字段留空 → 暂停等用户检查。
func (s *Service) Start(ctx context.Context, userID, appID model.ID) (*StartResult, error) {
	app, err := s.store.Application.GetByID(ctx, userID, appID)
	if err != nil {
		return nil, err
	}
	if model.IsSubmittedOrBeyond(app.Status) {
		return nil, errors.New("该任务已完成投递，无需再次辅助填写")
	}
	if app.ApplicationURL == "" {
		return nil, errors.New("该岗位缺少可用的申请页面地址，请手动补充")
	}
	if !s.client.Health(ctx) {
		return nil, ErrWorkerUnavailable
	}

	// 复用未结束的任务，实现「继续投递准备」。
	task, err := s.store.Browser.FindActiveByApplication(ctx, appID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if task == nil {
		task = &model.BrowserTask{
			ApplicationID: appID,
			Status:        model.BrowserTaskPending,
			SiteKey:       detectSiteKey(app.ApplicationURL),
			CurrentURL:    app.ApplicationURL,
			Step:          "created",
			PendingFields: model.JSONStringArray{},
		}
		if err := s.store.Browser.Create(ctx, task); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	_ = s.store.Browser.Update(ctx, task.ID, map[string]any{
		"status":     model.BrowserTaskRunning,
		"step":       "opening",
		"started_at": now,
	})

	jid := app.JobID
	_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
		ApplicationID: &appID,
		JobID:         &jid,
		EventType:     model.EventBrowserStarted,
		Description:   "启动浏览器辅助填写",
	})

	// ---- 打开页面并检查登录态 ----
	sess, err := s.client.OpenSession(ctx, SessionRequest{
		TaskID:  task.ID.String(),
		SiteKey: task.SiteKey,
		URL:     app.ApplicationURL,
	})
	if err != nil {
		s.failTask(ctx, userID, app, task.ID, "打开页面失败")
		return nil, err
	}

	if sess.NeedsLogin {
		_ = s.store.Browser.Update(ctx, task.ID, map[string]any{
			"status":      model.BrowserTaskWaitingUser,
			"step":        "login_required",
			"current_url": sess.CurrentURL,
		})
		_, _ = s.appSvc.UpdateStatus(ctx, userID, appID, model.AppStatusLoginRequired,
			"请在浏览器中完成登录与验证码，然后点击继续")

		updated, _ := s.store.Browser.GetByID(ctx, task.ID)
		return &StartResult{
			BrowserTask: updated,
			NeedsLogin:  true,
			Message:     "请在已打开的浏览器中完成登录（含验证码 / 二次验证），完成后点击「继续」",
		}, nil
	}

	// ---- 已登录：直接进入表单分析 ----
	if err := s.analyzeAndFill(ctx, userID, app, task.ID); err != nil {
		return nil, err
	}

	updated, _ := s.store.Browser.GetByID(ctx, task.ID)
	return &StartResult{
		BrowserTask: updated,
		Message:     buildUserMessage(updated),
	}, nil
}

// Resume 用户完成登录 / 手工填写后继续。
func (s *Service) Resume(ctx context.Context, userID, taskID model.ID) (*model.BrowserTask, error) {
	task, err := s.store.Browser.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	app, err := s.store.Application.GetByID(ctx, userID, task.ApplicationID)
	if err != nil {
		return nil, err
	}
	if !task.IsResumable() {
		return nil, fmt.Errorf("当前任务状态（%s）不支持继续", task.Status)
	}
	if !s.client.Health(ctx) {
		return nil, ErrWorkerUnavailable
	}

	// 登录后重新检查会话，再进入分析。
	if _, err := s.client.GetStatus(ctx, taskID.String()); err != nil {
		// 会话已丢失，重新打开。
		if _, err := s.client.OpenSession(ctx, SessionRequest{
			TaskID:  taskID.String(),
			SiteKey: task.SiteKey,
			URL:     firstNonEmpty(task.CurrentURL, app.ApplicationURL),
		}); err != nil {
			s.failTask(ctx, userID, app, taskID, "浏览器会话已丢失且无法重新打开")
			return nil, err
		}
	}

	if err := s.analyzeAndFill(ctx, userID, app, taskID); err != nil {
		return nil, err
	}
	return s.store.Browser.GetByID(ctx, taskID)
}

// Pause 暂停任务。
func (s *Service) Pause(ctx context.Context, userID, taskID model.ID) (*model.BrowserTask, error) {
	task, err := s.store.Browser.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.Application.GetByID(ctx, userID, task.ApplicationID); err != nil {
		return nil, err
	}

	if err := s.store.Browser.Update(ctx, taskID, map[string]any{
		"status": model.BrowserTaskPaused,
		"step":   "paused",
	}); err != nil {
		return nil, err
	}
	return s.store.Browser.GetByID(ctx, taskID)
}

// Get 查询浏览器任务，强制校验归属。
func (s *Service) Get(ctx context.Context, userID, taskID model.ID) (*model.BrowserTask, error) {
	task, err := s.store.Browser.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.Application.GetByID(ctx, userID, task.ApplicationID); err != nil {
		return nil, err
	}
	return task, nil
}

// ScrapeResult 是用已登录浏览器抓取真实 JD 的结果。
type ScrapeResult struct {
	// NeedsLogin 为 true 时，浏览器停在登录墙后，需用户登录后调用 ScrapeResume。
	NeedsLogin bool       `json:"needs_login"`
	TaskID     string     `json:"task_id"`
	Message    string     `json:"message"`
	Job        *model.Job `json:"job,omitempty"`
}

// Scrape 打开岗位来源页，用已登录浏览器抓取真实 JD。
//
// 流程：打开页面 → 检查登录 → （需登录则返回 task_id 等用户登录）→
// 读取页面正文 → AI 解析为结构化字段 → 写回岗位 enrichment 字段。
// 即使没有 AI Key，也会把原始正文存为 description，保证 P0 可用。
func (s *Service) Scrape(ctx context.Context, userID, jobID model.ID) (*ScrapeResult, error) {
	_ = userID // 仅用于审计，岗位本身属于当前用户
	job, err := s.store.Job.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	url := firstNonEmpty(job.SourceURL, job.OfficialURL)
	if url == "" {
		return nil, errors.New("该岗位没有可抓取的来源链接，无法用浏览器抓取真实 JD")
	}
	if !s.client.Health(ctx) {
		return nil, ErrWorkerUnavailable
	}

	taskID := model.NewID().String()
	siteKey := detectSiteKey(url)
	sess, err := s.client.OpenSession(ctx, SessionRequest{TaskID: taskID, SiteKey: siteKey, URL: url})
	if err != nil {
		return nil, err
	}
	if sess.NeedsLogin {
		return &ScrapeResult{NeedsLogin: true, TaskID: taskID, Message: sess.Message}, nil
	}
	return s.scrapeAndStore(ctx, jobID, taskID)
}

// ScrapeResume 用户登录后继续抓取真实 JD。
func (s *Service) ScrapeResume(ctx context.Context, userID, jobID model.ID, taskID string) (*ScrapeResult, error) {
	_ = userID
	if !s.client.Health(ctx) {
		return nil, ErrWorkerUnavailable
	}
	return s.scrapeAndStore(ctx, jobID, taskID)
}

// scrapeAndStore 读取页面正文、解析并写回岗位字段。
func (s *Service) scrapeAndStore(ctx context.Context, jobID model.ID, taskID string) (*ScrapeResult, error) {
	resp, err := s.client.ScrapePage(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if resp.NeedsLogin {
		return &ScrapeResult{NeedsLogin: true, TaskID: taskID, Message: resp.Message}, nil
	}

	job, err := s.store.Job.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}

	// AI 解析（无 Key 时返回空结构，降级为仅保存原文）。
	hint := strings.TrimSpace(job.Title + " " + job.CompanyName)
	parsed, _ := s.llm.ParseJD(ctx, resp.PageText, job.SourceURL, hint)

	fields := map[string]any{}
	if parsed.Title != "" {
		fields["title"] = parsed.Title
	}
	if parsed.CompanyName != "" {
		fields["company_name"] = parsed.CompanyName
	}
	if parsed.Department != "" {
		fields["department"] = parsed.Department
	}
	if parsed.Business != "" {
		fields["business"] = parsed.Business
	}
	if len(parsed.Locations) > 0 {
		fields["location"] = parsed.Locations[0]
		fields["locations"] = parsed.Locations
	}
	if parsed.JobType != "" {
		fields["job_type"] = parsed.JobType
	}
	if parsed.GraduationYear > 0 {
		fields["graduation_year"] = parsed.GraduationYear
	}
	// 真实 JD 正文：保存未改写的网页原文（可核对），无 AI 时也能用。
	fields["description"] = resp.PageText
	if len(parsed.Responsibilities) > 0 {
		fields["responsibilities"] = parsed.Responsibilities
	}
	if len(parsed.Requirements) > 0 {
		fields["requirements"] = parsed.Requirements
	}
	if len(parsed.Languages) > 0 {
		fields["language_requirements"] = parsed.Languages
	}
	if len(parsed.TechnicalStack) > 0 {
		fields["technical_stack"] = parsed.TechnicalStack
	}
	if parsed.Deadline != "" {
		if t, perr := time.Parse(time.DateOnly, parsed.Deadline); perr == nil {
			fields["deadline"] = t
		}
	}
	fields["crawled_at"] = time.Now().UTC()
	fields["desc_quality"] = "full" // 浏览器抓取的 JD 质量为 full

	// 仅更新白名单字段，避免误改用户状态 / 投递进度。
	if err := s.store.Job.UpdateEnrichment(ctx, jobID, fields); err != nil {
		return nil, err
	}

	_ = s.store.Job.UpsertSource(ctx, &model.JobSource{
		JobID:      jobID,
		SourceType: model.SourceBrowser,
		SourceName: "浏览器抓取",
		URL:        job.SourceURL,
	})

	_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
		JobID:       &jobID,
		EventType:   model.EventJobScraped,
		Description: fmt.Sprintf("已用浏览器抓取真实 JD：%s", firstNonEmpty(resp.Title, job.Title)),
	})

	updated, err := s.store.Job.GetByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return &ScrapeResult{Job: updated, Message: "已抓取并解析 JD"}, nil
}

// analyzeAndFill 识别表单、映射字段、填写非敏感项，然后暂停等待用户。
func (s *Service) analyzeAndFill(ctx context.Context, userID model.ID, app *model.Application, taskID model.ID) error {
	_ = s.store.Browser.Update(ctx, taskID, map[string]any{
		"status": model.BrowserTaskRunning,
		"step":   "analyzing",
	})
	_, _ = s.appSvc.UpdateStatus(ctx, userID, app.ID, model.AppStatusFormAnalyzing, "")

	extracted, err := s.client.ExtractForm(ctx, taskID.String())
	if err != nil {
		s.failTask(ctx, userID, app, taskID, "读取申请表失败")
		return err
	}

	// 网站明确阻止自动化时，停止并交给用户。
	if extracted.Blocked {
		_ = s.store.Browser.Update(ctx, taskID, map[string]any{
			"status":        model.BrowserTaskBlocked,
			"step":          "blocked",
			"current_url":   extracted.CurrentURL,
			"error_message": "页面存在自动化限制，已停止并交由用户处理",
		})
		_, _ = s.appSvc.UpdateStatus(ctx, userID, app.ID, model.AppStatusBlocked,
			"页面存在自动化限制，请手动完成填写")
		return nil
	}

	if len(extracted.Fields) == 0 {
		_ = s.store.Browser.Update(ctx, taskID, map[string]any{
			"status":      model.BrowserTaskWaitingUser,
			"step":        "no_form_found",
			"current_url": extracted.CurrentURL,
		})
		_, _ = s.appSvc.UpdateStatus(ctx, userID, app.ID, model.AppStatusWaitingUser,
			"未识别到申请表单，请手动确认页面")
		return nil
	}

	// ---- 字段映射：规则优先，LLM 兜底 ----
	profileData, err := s.profileSvc.GetApplicationProfile(ctx, userID)
	if err != nil {
		return err
	}
	values := profileData.Flatten()

	resolved, remaining := ai.MapFieldsByRule(extracted.Fields, values)
	if len(remaining) > 0 {
		llmMappings, err := s.llm.MapFieldsByLLM(ctx, remaining, values)
		if err != nil {
			slog.Warn("字段语义映射失败，剩余字段交由用户处理", "error", err.Error())
			for _, f := range remaining {
				resolved = append(resolved, ai.FieldMapping{
					Ref: f.Ref, Action: ai.ActionSkip, Reason: ai.ReasonNoMapping,
					Label: f.Label, Required: f.Required,
					IsSensitive: security.IsSensitiveField(f.Label, f.Name, f.Placeholder),
				})
			}
		} else {
			resolved = append(resolved, llmMappings...)
		}
	}

	// ---- 执行填写：只提交 action 为 FILL 的字段 ----
	fillItems := make([]FillItem, 0, len(resolved))
	for _, m := range resolved {
		if m.Action != ai.ActionFill {
			continue
		}
		// 最后一道防线：敏感字段绝不进入填写列表。
		if m.IsSensitive || security.IsSensitiveField(m.Label) {
			continue
		}
		fillItems = append(fillItems, FillItem{Ref: m.Ref, Value: m.Value})
	}

	_ = s.store.Browser.Update(ctx, taskID, map[string]any{
		"status": model.BrowserTaskRunning,
		"step":   "filling",
	})
	_, _ = s.appSvc.UpdateStatus(ctx, userID, app.ID, model.AppStatusFormFilling, "")

	var fillResp *FillResponse
	if len(fillItems) > 0 {
		fillResp, err = s.client.FillForm(ctx, FillRequest{TaskID: taskID.String(), Items: fillItems})
		if err != nil {
			s.failTask(ctx, userID, app, taskID, "填写表单失败")
			return err
		}
	} else {
		fillResp = &FillResponse{CurrentURL: extracted.CurrentURL}
	}

	failedRefs := make(map[string]bool, len(fillResp.FailedRefs))
	for _, r := range fillResp.FailedRefs {
		failedRefs[r] = true
	}

	// ---- 持久化字段分析结果（只存元信息，不存值） ----
	records := make([]model.ApplicationField, 0, len(resolved))
	pending := make([]string, 0, 8)
	byRef := make(map[string]ai.FormField, len(extracted.Fields))
	for _, f := range extracted.Fields {
		byRef[f.Ref] = f
	}

	filled, skipped := 0, 0
	for _, m := range resolved {
		f := byRef[m.Ref]
		isFilled := m.Action == ai.ActionFill && !failedRefs[m.Ref] && !m.IsSensitive
		if isFilled {
			filled++
		} else {
			skipped++
			label := firstNonEmpty(m.Label, f.Name, "未命名字段")
			pending = append(pending, label)
		}

		rec := model.ApplicationField{
			ApplicationID: app.ID,
			FieldName:     f.Name,
			FieldLabel:    m.Label,
			FieldType:     f.Type,
			Confidence:    m.Confidence,
			IsSensitive:   m.IsSensitive,
			IsFilled:      isFilled,
			IsRequired:    m.Required,
			SkipReason:    m.Reason,
		}
		// 数据库约束要求敏感字段不得有映射来源。
		if !m.IsSensitive && m.Source != "" {
			src := m.Source
			rec.MappedSource = &src
		}
		records = append(records, rec)
	}

	if err := s.store.Application.ReplaceFields(ctx, app.ID, records); err != nil {
		return err
	}

	if len(pending) > 12 {
		pending = pending[:12]
	}
	_ = s.store.Browser.Update(ctx, taskID, map[string]any{
		"status":         model.BrowserTaskWaitingUser,
		"step":           "waiting_user_review",
		"current_url":    fillResp.CurrentURL,
		"field_total":    len(resolved),
		"field_filled":   filled,
		"field_skipped":  skipped,
		"pending_fields": model.JSONStringArray(pending),
	})

	// 滚动到提交按钮提醒用户检查（不点击）。
	if err := s.client.Highlight(ctx, taskID.String()); err != nil {
		slog.Debug("高亮提交按钮失败", "error", err.Error())
	}

	// 状态推进：有待办则等待用户，否则待最终审核。
	nextStatus := model.AppStatusReadyToSubmit
	note := "已完成自动填写，请检查后自行在招聘网站点击提交"
	if skipped > 0 {
		nextStatus = model.AppStatusWaitingUser
		note = fmt.Sprintf("已自动填写 %d 项，另有 %d 项需你手动填写", filled, skipped)
	}
	_, _ = s.appSvc.UpdateStatus(ctx, userID, app.ID, nextStatus, note)

	jid := app.JobID
	_ = s.store.Application.AddEvent(ctx, &model.ApplicationEvent{
		ApplicationID: &app.ID,
		JobID:         &jid,
		EventType:     model.EventFormFilled,
		Description:   fmt.Sprintf("自动填写 %d 项，跳过 %d 项（含敏感字段）", filled, skipped),
	})
	return nil
}

// failTask 把任务标记为失败。
func (s *Service) failTask(ctx context.Context, userID model.ID, app *model.Application, taskID model.ID, reason string) {
	_ = s.store.Browser.Update(ctx, taskID, map[string]any{
		"status":        model.BrowserTaskFailed,
		"error_message": reason,
		"finished_at":   time.Now().UTC(),
	})
	if app != nil {
		_, _ = s.appSvc.UpdateStatus(ctx, userID, app.ID, model.AppStatusFailed, reason)
	}
}

// buildUserMessage 生成给用户看的提示文案。
func buildUserMessage(t *model.BrowserTask) string {
	if t == nil {
		return ""
	}
	switch t.Status {
	case model.BrowserTaskBlocked:
		return "该页面限制自动化操作，已停止，请手动完成填写"
	case model.BrowserTaskWaitingUser:
		if t.FieldSkipped > 0 {
			return fmt.Sprintf("已自动填写 %d 项信息，有 %d 项需要你手动填写（含身份证、验证码等敏感字段）",
				t.FieldFilled, t.FieldSkipped)
		}
		return "请在浏览器中确认页面内容"
	default:
		return fmt.Sprintf("已自动填写 %d 项信息，请检查后自行点击招聘网站的提交按钮", t.FieldFilled)
	}
}

// siteKeys 是已适配的站点标识。
var siteKeys = map[string]string{
	"zhipin.com":          "boss",
	"careers.tencent.com": "tencent",
	"join.qq.com":         "tencent",
	"jobs.bytedance.com":  "bytedance",
}

// detectSiteKey 依据 URL 判断使用哪个站点适配器。
func detectSiteKey(rawURL string) string {
	lower := rawURL
	for domain, key := range siteKeys {
		if contains(lower, domain) {
			return key
		}
	}
	return "generic"
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
