package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
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

	// 打开共享浏览器会话：BOSS 需要登录态才能看到完整 JD。
	// 用一次会话处理全部岗位，避免反复启动浏览器。
	taskID := uuid.NewString()
	siteKey := s.detectSiteKey(jobs[0].SourceURL)
	session, err := s.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID:  taskID,
		SiteKey: siteKey,
		URL:     jobs[0].SourceURL,
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
	if session.NeedsLogin || !session.LoggedIn {
		return nil, fmt.Errorf("需要登录：请在 Worker 弹出的浏览器中完成 %s 登录后再试", siteKey)
	}

	// 城市偏好打分表：与 Pipeline 主流程口径一致。
	cityScores := req.CityScores
	if cityScores == nil {
		cityScores = config.BuildCityScores(profile.PreferredLocations)
	}

	for i := range jobs {
		res := s.enrichOne(ctx, taskID, &jobs[i], profile, resumeBrief, cityScores)
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
	profile *model.JobProfile,
	resumeBrief string,
	cityScores map[string]int,
) EnrichResult {
	res := EnrichResult{
		JobID:    job.ID.String(),
		Title:    job.Title,
		OldScore: job.MatchScore,
	}

	url := strings.TrimSpace(job.SourceURL)
	if url == "" {
		res.Status = "skipped"
		res.Reason = "无来源 URL"
		return res
	}

	// 导航到岗位原网页。
	if _, err := s.browser.NavAct(ctx, taskID, browser.NavAction{
		Type: browser.NavNavigate,
		URL:  url,
	}); err != nil {
		res.Status = "failed"
		res.Reason = "导航失败: " + err.Error()
		return res
	}
	// SPA 需要等待渲染。
	if _, err := s.browser.NavAct(ctx, taskID, browser.NavAction{
		Type: browser.NavWait, Seconds: enrichWaitSeconds,
	}); err != nil {
		slog.Warn("补全等待渲染失败", "url", url, "error", err.Error())
	}

	scrape, err := s.browser.ScrapePage(ctx, taskID)
	if err != nil {
		res.Status = "failed"
		res.Reason = "抓取正文失败: " + err.Error()
		return res
	}

	pageText := strings.TrimSpace(scrape.PageText)
	runes := len([]rune(pageText))
	res.Runes = runes

	// 正文太短：多半是登录墙/验证页，不覆盖原有内容。
	if runes < s.minAcceptRunes {
		res.Status = "skipped"
		res.Reason = fmt.Sprintf("抓到的正文过短（%d 字符），疑似需要登录或遇到验证页", runes)
		return res
	}

	// 重新判定质量：仍不是 full 说明这个页面本身就没有完整 JD。
	cleaned, quality := DetectDescriptionQuality(pageText)
	if quality != DescQualityFull {
		res.Status = "skipped"
		res.Reason = "页面正文仍不构成完整 JD（quality=" + string(quality) + "）"
		return res
	}

	if err := s.applyEnrichment(ctx, job, cleaned, profile, resumeBrief, cityScores); err != nil {
		res.Status = "failed"
		res.Reason = "回填失败: " + err.Error()
		return res
	}

	res.Status = "enriched"
	res.DescQuality = string(DescQualityFull)
	res.NewScore = job.MatchScore
	return res
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
		"description":  pageText,
		"desc_quality": string(DescQualityFull),
		"crawled_at":   time.Now().UTC(),
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

// 占位：确保 source 包被引用（RawJob 用于未来扩展补全结果回灌）。
var _ = source.RawJob{}
