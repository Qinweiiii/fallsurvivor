package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/google/uuid"
)

// EnrichmentService 为「JD 不完整」的岗位补全正文。
//
// 背景与动机：
//
//	BOSS 直聘等站点有反爬，本项目不做登录态爬取、不绕过验证码，
//	第一版只能通过搜索引擎发现岗位 URL，拿到的只是摘要片段（snippet）。
//	残缺的 JD 会让 ParseJD 提取不出职责/要求/技术栈，
//	MatchJob 算出的匹配分自然也不可信。
//
// 补全手段：复用用户在 Worker 持久会话中**已登录**的浏览器，
// 打开岗位原网页读取正文。这是安全的——用的是用户自己的登录态，
// 不破解任何反爬机制，与用户手动点击查看完全等价。
//
// 补全完成后会：更新 description + desc_quality → 重新解析 → 重新评分 → 回写。
type EnrichmentService struct {
	browser  *browser.Client
	llm      *ai.Client
	store    *repository.Store
	pipeline *Pipeline
	// maxEnrichPerRun 单次补全的岗位数上限，控制浏览器会话时长。
	maxEnrichPerRun int
	// minAcceptRunes 低于该长度视为补全失败，不覆盖原有内容。
	minAcceptRunes int
}

// NewEnrichmentService 构造 JD 补全服务。
func NewEnrichmentService(bc *browser.Client, llm *ai.Client, store *repository.Store, pipeline *Pipeline) *EnrichmentService {
	return &EnrichmentService{
		browser:         bc,
		llm:             llm,
		store:           store,
		pipeline:        pipeline,
		maxEnrichPerRun: defaultMaxEnrichPerRun,
		minAcceptRunes:  defaultMinAcceptRunes,
	}
}

// RunEnrichment 适配 task.EnrichmentRunner。
func (s *EnrichmentService) RunEnrichment(ctx context.Context, userID uuid.UUID, jobIDs []uuid.UUID, maxJobs int, keyword string) error {
	ids := make([]model.ID, 0, len(jobIDs))
	for _, id := range jobIDs {
		ids = append(ids, model.ID(id))
	}
	_, err := s.EnrichIncomplete(ctx, EnrichRequest{
		UserID:  model.ID(userID),
		JobIDs:  ids,
		MaxJobs: maxJobs,
		Keyword: keyword,
	})
	return err
}

// 补全流程的默认值。
const (
	// defaultMaxEnrichPerRun 单次补全最多处理多少个岗位。
	defaultMaxEnrichPerRun = 10
	// defaultMinAcceptRunes 抓到的正文至少要有多少字符才视为有效。
	// 低于此值多半是登录墙/验证页，不应覆盖原有内容。
	defaultMinAcceptRunes = 300
	// enrichWaitSeconds 打开岗位页后等待渲染的秒数（BOSS 是 SPA）。
	enrichWaitSeconds = 3
)

// EnrichRequest 补全请求。
type EnrichRequest struct {
	// JobIDs 指定要补全的岗位；为空时自动挑选所有 JD 不完整的岗位。
	JobIDs []model.ID
	// UserID 操作者，用于读取画像重新评分。
	UserID model.ID
	// MaxJobs 本次最多补全多少条，0 表示用默认值。
	MaxJobs int
	// Keyword 是触发列表采集时使用的原始搜索词，用于回放同一个 job-list。
	Keyword string
	// CityScores 城市偏好打分表，由 BuildCityScores 生成；为空时按画像现场构造。
	CityScores map[string]int
}

// EnrichResult 一条岗位的补全结果。
type EnrichResult struct {
	JobID       string `json:"job_id"`
	Title       string `json:"title"`
	Status      string `json:"status"`    // enriched / skipped / failed
	Reason      string `json:"reason"`    // 跳过或失败的原因
	Runes       int    `json:"runes"`     // 补全后的正文字符数
	NewScore    int    `json:"new_score"` // 重新评分后的分数
	OldScore    int    `json:"old_score"` // 补全前的分数
	DescQuality string `json:"desc_quality"`
}

// EnrichSummary 补全汇总。
type EnrichSummary struct {
	Total      int            `json:"total"`
	Enriched   int            `json:"enriched"`
	Skipped    int            `json:"skipped"`
	Failed     int            `json:"failed"`
	Results    []EnrichResult `json:"results"`
	DurationMS int64          `json:"duration_ms"`
}

// EnrichIncomplete 补全 JD 不完整的岗位。
//
// 流程：
//  1. 选出待补全岗位（指定 ID 或全部 desc_quality != full）；
//  2. 打开一个共享的浏览器会话（复用用户登录态）；
//  3. 逐个导航到岗位原网页，等待渲染后抓取正文；
//  4. 正文有效则用 LLM 重新解析 + 重新评分，回写到数据库。
//
// 会话在所有岗位处理完后统一关闭，避免反复开关浏览器。
func (s *EnrichmentService) EnrichIncomplete(ctx context.Context, req EnrichRequest) (*EnrichSummary, error) {
	start := time.Now()
	summary := &EnrichSummary{Results: make([]EnrichResult, 0)}

	jobs, err := s.pickJobs(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		summary.DurationMS = time.Since(start).Milliseconds()
		return summary, nil
	}
	summary.Total = len(jobs)

	// 读取求职画像与简历摘要，用于重新评分。
	profile, err := s.store.User.GetProfile(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("读取求职画像失败: %w", err)
	}
	resumeBrief := s.loadResumeBrief(ctx, req.UserID)

	entryURL, siteKey, err := s.entryForJobs(ctx, jobs)
	if err != nil {
		return nil, err
	}

	// 打开共享浏览器会话。是否需要登录只接受 Worker 的显式 needs_login，
	// 不能因为页面上有“登录”按钮就中断公开页面补全。
	taskID := uuid.NewString()
	session, err := s.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID:  taskID,
		SiteKey: siteKey,
		URL:     entryURL,
	})
	if err != nil {
		return nil, fmt.Errorf("打开浏览器会话失败: %w", err)
	}
	defer func() {
		if closeErr := s.browser.CloseSession(context.Background(), taskID); closeErr != nil {
			slog.Warn("关闭补全会话失败", "task_id", taskID, "error", closeErr.Error())
		}
	}()

	// 未登录时给出明确指引：补全依赖用户自己的登录态。
	if session.NeedsLogin {
		return nil, fmt.Errorf("需要登录：请在 Worker 弹出的浏览器中完成 %s 登录后再试", siteKey)
	}

	// 城市偏好打分表：与 Pipeline 主流程口径一致。
	cityScores := req.CityScores
	if cityScores == nil {
		cityScores = config.BuildCityScores(profile.PreferredLocations)
	}

	listState := &enrichmentListState{}
	for i := range jobs {
		res := s.enrichOne(ctx, taskID, &jobs[i], req.Keyword, listState, profile, resumeBrief, cityScores)
		summary.Results = append(summary.Results, res)
		switch res.Status {
		case "enriched":
			summary.Enriched++
		case "skipped":
			summary.Skipped++
		default:
			summary.Failed++
		}
	}

	summary.DurationMS = time.Since(start).Milliseconds()
	slog.Info("JD 补全完成",
		"total", summary.Total,
		"enriched", summary.Enriched,
		"skipped", summary.Skipped,
		"failed", summary.Failed,
		"duration_ms", summary.DurationMS)
	return summary, nil
}

// enrichOne 补全单个岗位。
func (s *EnrichmentService) enrichOne(
	ctx context.Context,
	taskID string,
	job *model.Job,
	listKeyword string,
	listState *enrichmentListState,
	profile *model.JobProfile,
	resumeBrief string,
	cityScores map[string]int,
) EnrichResult {
	res := EnrichResult{
		JobID:    job.ID.String(),
		Title:    job.Title,
		OldScore: job.MatchScore,
	}
	s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusEnriching, "")

	detailURL, fromList, err := s.openDetailPage(ctx, taskID, job, listKeyword, listState)
	if err != nil {
		res.Status = "skipped"
		res.Reason = err.Error()
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}
	if fromList {
		defer s.returnToList(ctx, taskID, listState)
	}

	scrape, err := s.browser.ScrapePage(ctx, taskID)
	if err != nil {
		res.Status = "failed"
		res.Reason = "抓取正文失败: " + err.Error()
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}

	pageText := strings.TrimSpace(scrape.PageText)
	runes := len([]rune(pageText))
	res.Runes = runes

	if looksLikeJobListPage(pageText) && !looksLikeJDDetailPage(pageText) {
		res.Status = "skipped"
		res.Reason = "仍停留在岗位列表页，未进入具体岗位详情"
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}

	// 正文太短：多半是登录墙/验证页，不覆盖原有内容。
	if runes < s.minAcceptRunes {
		res.Status = "skipped"
		res.Reason = fmt.Sprintf("抓到的正文过短（%d 字符），疑似需要登录或遇到验证页", runes)
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}

	// 重新判定质量：仍不是 full 说明这个页面本身就没有完整 JD。
	cleaned, quality := DetectDescriptionQuality(pageText)
	if quality != DescQualityFull {
		res.Status = "skipped"
		res.Reason = "页面正文仍不构成完整 JD（quality=" + string(quality) + "）"
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}
	if !looksLikeJDDetailPage(cleaned) {
		res.Status = "skipped"
		res.Reason = "页面没有职责/要求类正文块，暂不作为完整 JD"
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}

	if detailURL != "" && job.SourceURL == "" {
		job.SourceURL = detailURL
		if job.SourceType == model.SourceOfficial {
			job.OfficialURL = detailURL
		}
	}
	if err := s.applyEnrichment(ctx, job, cleaned, profile, resumeBrief, cityScores); err != nil {
		res.Status = "failed"
		res.Reason = "回填失败: " + err.Error()
		s.markEnrichmentStatus(ctx, job.ID, model.EnrichmentStatusFailed, res.Reason)
		return res
	}

	res.Status = "enriched"
	res.DescQuality = string(DescQualityFull)
	res.NewScore = job.MatchScore
	return res
}

func (s *EnrichmentService) markEnrichmentStatus(ctx context.Context, jobID model.ID, status, reason string) {
	fields := map[string]any{
		"enrichment_status":     status,
		"enrichment_error":      reason,
		"enrichment_updated_at": time.Now().UTC(),
	}
	if err := s.store.Job.UpdateEnrichment(ctx, jobID, fields); err != nil {
		slog.Warn("更新 JD 补全状态失败", "job_id", jobID.String(), "status", status, "error", err.Error())
	}
}

type enrichmentListState struct {
	key     string
	listURL string
}

func (s *EnrichmentService) openDetailPage(ctx context.Context, taskID string, job *model.Job, listKeyword string, listState *enrichmentListState) (string, bool, error) {
	if url := s.firstUsableDetailURL(ctx, job); url != "" {
		if err := s.navigateAndWait(ctx, taskID, url); err != nil {
			return "", false, fmt.Errorf("导航失败: %w", err)
		}
		return url, false, nil
	}
	rc, err := s.recipeForJob(ctx, job)
	if err != nil {
		return "", false, fmt.Errorf("读取站点 Recipe 失败: %w", err)
	}
	if rc == nil || strings.TrimSpace(rc.CampusURL) == "" {
		return "", false, fmt.Errorf("无来源 URL，且未找到可回放的站点 Recipe")
	}
	entry := strings.TrimSpace(rc.CampusURL)
	key := rc.SiteKey + "\n" + entry + "\n" + strings.TrimSpace(listKeyword)
	if listState == nil || listState.key != key {
		if err := s.navigateAndWait(ctx, taskID, entry); err != nil {
			return "", false, fmt.Errorf("回到列表页失败: %w", err)
		}
		if err := runBrowserPlan(ctx, s.browser, taskID, rc.BrowserPlan, listKeyword, "enrichment"); err != nil {
			return "", false, err
		}
		if listState != nil {
			listState.key = key
			listState.listURL = s.currentURL(ctx, taskID)
		}
	}
	if strings.TrimSpace(job.Title) == "" {
		return "", true, fmt.Errorf("岗位标题为空，无法从列表页定位岗位卡片")
	}
	resp, err := s.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavClick, Text: job.Title})
	if err != nil {
		return "", true, fmt.Errorf("点击岗位卡片失败: %w", err)
	}
	if resp != nil && !resp.OK {
		return "", true, fmt.Errorf("点击岗位卡片未完成: %s", resp.Message)
	}
	_, _ = s.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: enrichWaitSeconds})
	if resp != nil && isNavigableWebURL(resp.CurrentURL) && !sameAPIPath(entry, resp.CurrentURL) {
		return resp.CurrentURL, true, nil
	}
	return "", true, nil
}

func (s *EnrichmentService) returnToList(ctx context.Context, taskID string, listState *enrichmentListState) {
	if listState == nil || strings.TrimSpace(listState.listURL) == "" {
		return
	}
	current := s.currentURL(ctx, taskID)
	if current == "" || current == listState.listURL {
		return
	}
	if _, err := s.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavBack}); err != nil {
		slog.Warn("补全后返回列表页失败", "error", err.Error())
		return
	}
	_, _ = s.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: enrichWaitSeconds})
}

func (s *EnrichmentService) currentURL(ctx context.Context, taskID string) string {
	status, err := s.browser.GetStatus(ctx, taskID)
	if err != nil || status == nil {
		return ""
	}
	return strings.TrimSpace(status.CurrentURL)
}

func (s *EnrichmentService) firstUsableDetailURL(ctx context.Context, job *model.Job) string {
	for _, raw := range []string{job.SourceURL, job.OfficialURL} {
		if isNavigableWebURL(raw) {
			return strings.TrimSpace(raw)
		}
	}
	rc, err := s.recipeForJob(ctx, job)
	if err != nil || rc == nil || strings.TrimSpace(rc.DetailURLTemplate) == "" {
		return ""
	}
	id := firstNonEmpty(
		ExtractURLJobID(job.NormalizedURL),
		ExtractURLJobID(job.SourceURL),
		ExtractURLJobID(job.OfficialURL),
	)
	if id == "" {
		return ""
	}
	return strings.ReplaceAll(rc.DetailURLTemplate, "{id}", url.QueryEscape(id))
}

func (s *EnrichmentService) navigateAndWait(ctx context.Context, taskID, rawURL string) error {
	resp, err := s.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavNavigate, URL: rawURL})
	if err != nil {
		return err
	}
	if resp != nil && !resp.OK {
		return errors.New(resp.Message)
	}
	if _, err := s.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: enrichWaitSeconds}); err != nil {
		slog.Warn("补全等待渲染失败", "url", rawURL, "error", err.Error())
	}
	return nil
}

func (s *EnrichmentService) entryForJobs(ctx context.Context, jobs []model.Job) (string, string, error) {
	for i := range jobs {
		if url := s.firstUsableDetailURL(ctx, &jobs[i]); url != "" {
			return url, s.detectSiteKeyFromJob(ctx, &jobs[i]), nil
		}
	}
	for i := range jobs {
		rc, err := s.recipeForJob(ctx, &jobs[i])
		if err != nil {
			return "", "", err
		}
		if rc != nil && strings.TrimSpace(rc.CampusURL) != "" {
			return rc.CampusURL, firstNonEmpty(rc.SiteKey, s.detectSiteKey(rc.CampusURL)), nil
		}
	}
	return "", "", fmt.Errorf("没有可打开的岗位详情 URL 或站点入口")
}

func (s *EnrichmentService) detectSiteKeyFromJob(ctx context.Context, job *model.Job) string {
	if rc, err := s.recipeForJob(ctx, job); err == nil && rc != nil && strings.TrimSpace(rc.SiteKey) != "" {
		return rc.SiteKey
	}
	return s.detectSiteKey(firstNonEmpty(job.SourceURL, job.OfficialURL, job.NormalizedURL))
}

func (s *EnrichmentService) recipeForJob(ctx context.Context, job *model.Job) (*site.Recipe, error) {
	if job == nil || s.store == nil || s.store.Site == nil {
		return nil, nil
	}
	siteKey := s.detectSiteKey(firstNonEmpty(job.SourceURL, job.OfficialURL, job.NormalizedURL))
	if siteKey != "" && siteKey != "generic" {
		rc, err := s.store.Site.GetBySiteKey(ctx, siteKey)
		if err != nil || rc != nil {
			return rc, err
		}
	}
	all, err := s.store.Site.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	jobCompany := strings.TrimSpace(job.CompanyName)
	jobHost := hostOf(firstNonEmpty(job.SourceURL, job.OfficialURL, job.NormalizedURL))
	for i := range all {
		rc := &all[i]
		if jobCompany != "" && strings.EqualFold(strings.TrimSpace(rc.CompanyName), jobCompany) {
			return rc, nil
		}
		if jobHost != "" && strings.EqualFold(strings.TrimSpace(rc.Domain), jobHost) {
			return rc, nil
		}
	}
	return nil, nil
}

func isNavigableWebURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func looksLikeJobListPage(text string) bool {
	text = CleanText(text)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	if strings.Contains(text, "搜索词-") || strings.Contains(lower, "search keyword") {
		return true
	}
	hasCount := strings.Contains(text, "共") && (strings.Contains(text, "岗位") || strings.Contains(text, "职位"))
	repeatedLocation := strings.Count(text, "工作地点") >= 2 || strings.Count(text, "工作城市") >= 2
	return hasCount && repeatedLocation
}

func looksLikeJDDetailPage(text string) bool {
	text = CleanText(text)
	if text == "" {
		return false
	}
	markers := []string{
		"岗位职责", "工作职责", "职位职责", "工作内容",
		"岗位要求", "任职要求", "职位要求", "资格要求",
		"岗位描述", "职位描述", "职责描述",
	}
	hits := 0
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			hits++
		}
	}
	return hits >= 2
}

// applyEnrichment 用补全后的正文重新解析、重新评分并回写数据库。
func (s *EnrichmentService) applyEnrichment(
	ctx context.Context,
	job *model.Job,
	pageText string,
	profile *model.JobProfile,
	resumeBrief string,
	cityScores map[string]int,
) error {
	// 1. 用完整 JD 重新解析结构化字段。
	parsed, err := s.llm.ParseJD(ctx, pageText, job.SourceURL, job.Title)
	if err != nil {
		// 解析失败不阻断：至少把正文与质量标记写回，评分保持原值。
		slog.Warn("补全后重新解析失败，仅回写正文", "job_id", job.ID, "error", err.Error())
		parsed = nil
	}

	// 先把解析出的结构化字段应用到内存中的 job，供评分使用。
	if parsed != nil {
		if len(parsed.Responsibilities) > 0 {
			job.Responsibilities = parsed.Responsibilities
		}
		if len(parsed.Requirements) > 0 {
			job.Requirements = parsed.Requirements
		}
		if len(parsed.TechnicalStack) > 0 {
			job.TechnicalStack = parsed.TechnicalStack
		}
		if len(parsed.Locations) > 0 {
			job.Locations = parsed.Locations
		}
	}

	fields := map[string]any{
		"description":           pageText,
		"desc_quality":          string(DescQualityFull),
		"crawled_at":            time.Now().UTC(),
		"enrichment_status":     model.EnrichmentStatusEnriched,
		"enrichment_error":      "",
		"enrichment_updated_at": time.Now().UTC(),
	}
	if isNavigableWebURL(job.SourceURL) {
		fields["source_url"] = job.SourceURL
	}
	if isNavigableWebURL(job.OfficialURL) {
		fields["official_url"] = job.OfficialURL
	}
	if parsed != nil {
		if len(parsed.Responsibilities) > 0 {
			fields["responsibilities"] = mustJSON(parsed.Responsibilities)
		}
		if len(parsed.Requirements) > 0 {
			fields["requirements"] = mustJSON(parsed.Requirements)
		}
		if len(parsed.Languages) > 0 {
			fields["language_requirements"] = mustJSON(parsed.Languages)
		}
		if len(parsed.TechnicalStack) > 0 {
			fields["technical_stack"] = mustJSON(parsed.TechnicalStack)
		}
	}

	// 用完整信息重新评分：规则分 + LLM 分融合，与主流程口径一致。
	score, analysis, scoreErr := s.rescore(ctx, job, profile, resumeBrief, cityScores)
	if scoreErr != nil {
		// 评分失败不阻断：正文与质量标记仍然写回，分数保持原值。
		slog.Warn("补全后重新评分失败，保留原分数", "job_id", job.ID, "error", scoreErr.Error())
	} else {
		fields["match_score"] = score
		if analysis != "" {
			fields["match_analysis"] = analysis
		}
		job.MatchScore = score
	}

	if err := s.store.Job.UpdateEnrichment(ctx, job.ID, fields); err != nil {
		return err
	}
	return nil
}

// rescore 基于补全后的完整信息重新计算匹配分，返回分数与分析 JSON。
// 口径与 Pipeline 主流程一致：规则分打底，LLM 分融合。
func (s *EnrichmentService) rescore(
	ctx context.Context,
	job *model.Job,
	profile *model.JobProfile,
	resumeBrief string,
	cityScores map[string]int,
) (int, string, error) {
	// 规则分：确定性逻辑，LLM 不可用时也能给出可用排序。
	rule := ComputeRuleScore(ScoreInput{Job: job, Profile: profile, CityScores: cityScores})

	var llmResult *ai.MatchResult
	if s.llm != nil && s.llm.Enabled() {
		in := ai.MatchInput{
			Job: ai.MatchJobBrief{
				CompanyName:    job.CompanyName,
				Department:     job.Department,
				Title:          job.Title,
				Locations:      job.Locations,
				JobType:        job.JobType,
				Requirements:   job.Requirements,
				TechnicalStack: job.TechnicalStack,
				Description:    job.Description,
			},
			Profile: ai.MatchProfile{
				TargetRoles:        profile.TargetRoles,
				PreferredLanguages: profile.PreferredLanguages,
				PreferredLocations: profile.PreferredLocations,
				CompanyPreferences: profile.CompanyPreferences,
				GraduationYear:     profile.GraduationYear,
			},
			ResumeSummary: resumeBrief,
			RuleScore:     rule.Total,
		}
		if job.GraduationYear != nil {
			in.Job.GraduationYear = *job.GraduationYear
		}
		if r, err := s.llm.MatchJob(ctx, in); err != nil {
			// 只警告不失败：还有规则分可用。
			slog.Warn("补全后 LLM 匹配失败，使用规则分", "job_id", job.ID, "error", err.Error())
		} else {
			llmResult = r
		}
	}

	analysis := FuseScores(rule, llmResult)
	analysis.AnalyzedAt = time.Now().UTC().Format(time.RFC3339)
	analysisJSON := ""
	if m, err := toJSONMap(analysis); err == nil {
		if b, mErr := json.Marshal(m); mErr == nil {
			analysisJSON = string(b)
		}
	}
	return analysis.Score, analysisJSON, nil
}

// pickJobs 挑选待补全的岗位。
func (s *EnrichmentService) pickJobs(ctx context.Context, req EnrichRequest) ([]model.Job, error) {
	limit := req.MaxJobs
	if limit <= 0 {
		limit = s.maxEnrichPerRun
	}

	// 指定了 ID 就只处理这些。
	if len(req.JobIDs) > 0 {
		jobs := make([]model.Job, 0, len(req.JobIDs))
		for _, id := range req.JobIDs {
			job, err := s.store.Job.GetByID(ctx, id)
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					continue
				}
				return nil, err
			}
			jobs = append(jobs, *job)
			if len(jobs) >= limit {
				break
			}
		}
		return jobs, nil
	}

	// 否则挑所有 JD 不完整的（desc_quality 不是 full）。
	// 优先处理 BOSS 来源——它受反爬限制最严重，只能拿到摘要。
	// 若 BOSS 无待补全项，则退化到全部来源。
	if jobs, err := s.store.Job.ListIncompleteJD(ctx, limit, string(model.SourceBoss)); err == nil && len(jobs) > 0 {
		return jobs, nil
	}
	return s.store.Job.ListIncompleteJD(ctx, limit)
}

// loadResumeBrief 读取简历摘要，失败时返回空串（不阻断补全）。
func (s *EnrichmentService) loadResumeBrief(ctx context.Context, userID model.ID) string {
	r, err := s.store.Resume.GetCurrent(ctx, userID)
	if err != nil || r == nil {
		return ""
	}
	raw, err := json.Marshal(r.StructuredData)
	if err != nil {
		return ""
	}
	rp, err := ai.ParseResumeProfile(raw)
	if err != nil {
		return ""
	}
	return rp.Brief()
}

// detectSiteKey 根据 URL 推断站点标识，用于选择浏览器登录态目录。
func (s *EnrichmentService) detectSiteKey(rawURL string) string {
	lower := strings.ToLower(rawURL)
	switch {
	case strings.Contains(lower, "zhipin.com"):
		return "boss"
	case strings.Contains(lower, "join.qq.com"):
		return "tencent"
	case strings.Contains(lower, "bytedance.com"):
		return "bytedance"
	default:
		return "generic"
	}
}

// mustJSON 序列化为 JSON 字符串，失败时回退为空数组。
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil || v == nil {
		return "[]"
	}
	return string(b)
}
