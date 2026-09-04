package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
	"github.com/eddiel/fallsurvivor/backend/pkg/safefetch"
)

// 管线参数。
const (
	// maxCandidatesPerRun 限制单次任务处理的候选数量，控制时长与 LLM 成本。
	maxCandidatesPerRun = 60
	// maxLLMConcurrency 限制并发调用模型的数量。
	maxLLMConcurrency = 4
	// highMatchThreshold 是「高匹配」的分数线。
	highMatchThreshold = 85
	// taskTimeout 是单次搜索任务的整体超时。
	taskTimeout = 10 * time.Minute
)

// Pipeline 是岗位搜索管线。
type Pipeline struct {
	store    *repository.Store
	registry *source.Registry
	llm      *ai.Client
	fetcher  *safefetch.Client
}

// NewPipeline 创建管线。
func NewPipeline(store *repository.Store, registry *source.Registry, llm *ai.Client, fetcher *safefetch.Client) *Pipeline {
	return &Pipeline{store: store, registry: registry, llm: llm, fetcher: fetcher}
}

// Run 执行一次完整的搜索任务。
//
// 关键约定：
//   - 单个来源失败不导致任务失败，任务状态记为 COMPLETED_WITH_WARNING；
//   - JD 解析失败时保留原始文本，不丢弃岗位；
//   - LLM 不可用时降级为纯规则评分，任务仍可完成。
func (p *Pipeline) Run(ctx context.Context, taskID model.ID) error {
	ctx, cancel := context.WithTimeout(ctx, taskTimeout)
	defer cancel()

	task, err := p.store.SearchTask.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("加载搜索任务失败: %w", err)
	}
	if task.IsTerminal() {
		slog.Info("搜索任务已结束，跳过", "task_id", taskID.String())
		return nil
	}

	now := time.Now().UTC()
	if err := p.store.SearchTask.Update(ctx, taskID, map[string]any{
		"status":     model.SearchTaskRunning,
		"started_at": now,
	}); err != nil {
		return err
	}

	result, runErr := p.run(ctx, task)

	fields := map[string]any{"finished_at": time.Now().UTC()}
	if runErr != nil {
		// 任务被服务关闭/重启取消（context 取消）时，标记为 TERMINATED 而非失败。
		// 同时检查 ctx.Err()，因为 asynq 关闭会取消父 ctx，而 run 内部错误未必 wrap context.Canceled。
		canceled := ctx.Err() == context.Canceled || errors.Is(runErr, context.Canceled)
		if canceled {
			fields["status"] = model.SearchTaskTerminated
			fields["error_message"] = "任务被服务关闭或重启取消"
			slog.Info("搜索任务被取消", "task_id", taskID.String())
		} else {
			fields["status"] = model.SearchTaskFailed
			// 只写通用错误描述，细节进日志。
			fields["error_message"] = sanitizeErr(runErr)
			slog.Error("搜索任务失败", "task_id", taskID.String(), "error", runErr.Error())
		}
	} else {
		status := model.SearchTaskCompleted
		if len(result.Warnings) > 0 {
			status = model.SearchTaskCompletedWarn
		}
		fields["status"] = status
		fields["queries"] = model.JSONStringArray(result.Queries)
		fields["query_count"] = len(result.Queries)
		fields["found_count"] = result.FoundCount
		fields["new_count"] = result.NewCount
		fields["duplicate_count"] = result.DuplicateCount
		fields["high_match_count"] = result.HighMatchCount
		fields["warnings"] = model.JSONStringArray(result.Warnings)
		if b, err := json.Marshal(map[string]any{"sources": result.SourceResults}); err == nil {
			fields["source_results"] = b
		}
	}

	// 用脱离取消的 context 写最终状态：worker 关闭时 asynq 会取消任务 ctx，
	// 若仍用原 ctx，Update 会因 context canceled 失败，导致任务永久卡在 RUNNING。
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer writeCancel()
	if err := p.store.SearchTask.Update(writeCtx, taskID, fields); err != nil {
		return err
	}
	return runErr
}

// RunResult 是一次任务的执行结果。
type RunResult struct {
	Queries        []string
	FoundCount     int
	NewCount       int
	DuplicateCount int
	HighMatchCount int
	Warnings       []string
	SourceResults  []source.Result
}

func (p *Pipeline) run(ctx context.Context, task *model.SearchTask) (*RunResult, error) {
	out := &RunResult{}

	// ---------- 1. 读取画像与简历 ----------
	profile, err := p.store.User.GetProfile(ctx, task.UserID)
	if err != nil {
		return nil, fmt.Errorf("读取求职画像失败: %w", err)
	}

	var resumeProfile *ai.ResumeProfile
	if r, err := p.store.Resume.GetCurrent(ctx, task.UserID); err == nil && r != nil {
		if raw, mErr := json.Marshal(r.StructuredData); mErr == nil {
			if rp, pErr := ai.ParseResumeProfile(raw); pErr == nil {
				resumeProfile = rp
			}
		}
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		slog.Warn("读取当前简历失败", "error", err.Error())
	}

	// ---------- 2. 生成检索式 ----------
	queries, warn := p.buildQueries(ctx, profile, resumeProfile)
	if warn != "" {
		out.Warnings = append(out.Warnings, warn)
	}
	if len(queries) == 0 {
		return nil, errors.New("无法生成任何检索式，请先完善求职画像")
	}
	out.Queries = queries

	// ---------- 3. 并行调用各来源 ----------
	sq := source.SearchQuery{
		Queries:            queries,
		Locations:          profile.PreferredLocations,
		CompanyPreferences: profile.CompanyPreferences,
		GraduationYear:     profile.GraduationYear,
		MaxResultsPerQuery: 2,
	}

	raws, srcResults := p.searchAllSources(ctx, sq)
	out.SourceResults = srcResults
	for _, r := range srcResults {
		if !r.Success {
			out.Warnings = append(out.Warnings,
				fmt.Sprintf("%s 本次未返回结果：%s", r.SourceName, r.Error))
		}
	}
	if len(raws) == 0 {
		if len(out.Warnings) == len(srcResults) && len(srcResults) > 0 {
			return nil, errors.New("全部岗位来源均不可用")
		}
		return out, nil
	}
	out.FoundCount = len(raws)

	// ---------- 4. 批内去重 ----------
	candidates, extras, dedupStats := DedupInBatch(raws)
	slog.Info("批内去重完成",
		"input", dedupStats.InputCount,
		"noise", dedupStats.DroppedNoise,
		"invalid", dedupStats.DroppedInvalid,
		"merged", dedupStats.MergedInBatch,
		"unique", dedupStats.Unique)

	if len(candidates) > maxCandidatesPerRun {
		out.Warnings = append(out.Warnings,
			fmt.Sprintf("本次候选过多，仅处理前 %d 条，可再次点击获取岗位继续", maxCandidatesPerRun))
		candidates = candidates[:maxCandidatesPerRun]
	}

	// ---------- 5. 逐个丰富 + 评分 + 落库 ----------
	cityScores := config.BuildCityScores(profile.PreferredLocations)
	resumeBrief := resumeProfile.Brief()

	var (
		mu             sync.Mutex
		newCount       int
		duplicateCount = dedupStats.MergedInBatch
		highCount      int
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxLLMConcurrency)

	for i := range candidates {
		c := candidates[i]
		g.Go(func() error {
			created, score, _, err := p.processCandidate(gctx, task.UserID, c, profile, cityScores, resumeBrief, false)
			if err != nil {
				// 单条失败不影响整体。
				slog.Warn("处理岗位候选失败", "url", c.NormalizedURL, "error", err.Error())
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			if created {
				newCount++
			} else {
				duplicateCount++
			}
			if score >= highMatchThreshold {
				highCount++
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// ---------- 6. 附加来源记录 ----------
	p.recordExtraSources(ctx, extras)

	out.NewCount = newCount
	out.DuplicateCount = duplicateCount
	out.HighMatchCount = highCount
	return out, nil
}

// buildQueries 生成检索式。LLM 不可用时使用确定性模板兜底。
func (p *Pipeline) buildQueries(ctx context.Context, profile *model.JobProfile, resume *ai.ResumeProfile) ([]string, string) {
	if p.llm.Enabled() {
		in := ai.QueryGeneratorInput{
			TargetRoles:        profile.TargetRoles,
			PreferredLanguages: profile.PreferredLanguages,
			Locations:          profile.PreferredLocations,
			CompanyPreferences: profile.CompanyPreferences,
			GraduationYear:     profile.GraduationYear,
			ResumeHighlights:   resume.HighlightKeywords(8),
		}
		if plan, err := p.llm.GenerateQueries(ctx, in); err == nil && len(plan.Queries) > 0 {
			return plan.Queries, ""
		} else if err != nil {
			slog.Warn("检索式生成失败，改用模板兜底", "error", err.Error())
			return FallbackQueries(profile), "AI 检索式生成失败，已使用默认关键词组合"
		}
	}
	return FallbackQueries(profile), ""
}

// FallbackQueries 是纯确定性的检索式模板，用于 LLM 不可用时兜底。
func FallbackQueries(profile *model.JobProfile) []string {
	roles := profile.TargetRoles
	if len(roles) == 0 {
		roles = []string{"后端开发", "AI后端"}
	}
	cities := profile.PreferredLocations
	if len(cities) == 0 {
		cities = []string{"深圳", "广州"}
	}
	langs := profile.PreferredLanguages
	if len(langs) == 0 {
		langs = []string{"Go"}
	}
	year := profile.GraduationYear
	if year == 0 {
		year = 2027
	}

	out := make([]string, 0, 16)
	// 岗位方向 × 城市
	for _, role := range roles {
		for i, city := range cities {
			if i >= 3 {
				break
			}
			out = append(out, fmt.Sprintf("%d届 %s 校园招聘 %s", year, role, city))
		}
	}
	// 语言 × 首选城市
	for _, lang := range langs {
		out = append(out, fmt.Sprintf("%d届 %s %s 校招 后端", year, lang, cities[0]))
	}
	// 官方招聘站定向
	out = append(out, fmt.Sprintf("%d届 校园招聘 官网 %s 后端开发", year, cities[0]))

	// 去重限量
	seen := map[string]bool{}
	uniq := make([]string, 0, len(out))
	for _, q := range out {
		if seen[q] {
			continue
		}
		seen[q] = true
		uniq = append(uniq, q)
		if len(uniq) >= 1 {
			break
		}
	}
	return uniq
}

// searchAllSources 并行调用全部可用来源。
func (p *Pipeline) searchAllSources(ctx context.Context, sq source.SearchQuery) ([]source.RawJob, []source.Result) {
	sources := p.registry.Available()
	if len(sources) == 0 {
		return nil, nil
	}

	type outcome struct {
		jobs   []source.RawJob
		result source.Result
	}
	outcomes := make([]outcome, len(sources))

	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Add(1)
		go func(i int, s source.JobSource) {
			defer wg.Done()
			start := time.Now()
			jobs, err := s.Search(ctx, sq)
			res := source.Result{
				SourceType: s.Type(),
				SourceName: s.Name(),
				DurationMS: time.Since(start).Milliseconds(),
			}
			if err != nil {
				res.Success = false
				res.Error = sanitizeErr(err)
			} else {
				res.Success = true
				res.Count = len(jobs)
			}
			outcomes[i] = outcome{jobs: jobs, result: res}
		}(i, s)
	}
	wg.Wait()

	var (
		all     []source.RawJob
		results = make([]source.Result, 0, len(outcomes))
	)
	for _, o := range outcomes {
		all = append(all, o.jobs...)
		results = append(results, o.result)
	}
	return all, results
}

// IngestResult 汇总一次浏览器脚本入库的结果，用于把「异步 goroutine 静默吞错」
// 变成可见的诊断信息返回给调用方。
type IngestResult struct {
	// Total 进入管线的候选数。
	Total int `json:"total"`
	// NewCount 实际新建的岗位数。
	NewCount int `json:"new_count"`
	// DupCount 因已存在被判定为重复、未新建的岗位数。
	DupCount int `json:"duplicate_count"`
	// Skipped 因质量/类型过滤被跳过的岗位数（非重复、非错误）。
	Skipped int `json:"skipped"`
	// SkippedReasons 按跳过原因聚合计数，便于定位噪声来源。
	SkippedReasons map[string]int `json:"skipped_reasons"`
}

// IngestRawJobs 处理已由外部（如浏览器半固定脚本）结构化的原始岗位：
// 批内去重 → 逐条丰富 + 评分 + 落库。复用了 Run 的核心步骤 4~6，
// 让字节跳动这类需要浏览器的站点也能产出干净、字段准确的结构化岗位记录。
func (p *Pipeline) IngestRawJobs(ctx context.Context, userID model.ID, raws []source.RawJob) (*IngestResult, error) {
	res := &IngestResult{SkippedReasons: map[string]int{}}
	if len(raws) == 0 {
		return nil, nil
	}

	// 加载画像与简历（与 Run 一致）。
	profile, err := p.store.User.GetProfile(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("读取求职画像失败: %w", err)
	}
	var resumeProfile *ai.ResumeProfile
	if r, err := p.store.Resume.GetCurrent(ctx, userID); err == nil && r != nil {
		if raw, mErr := json.Marshal(r.StructuredData); mErr == nil {
			if rp, pErr := ai.ParseResumeProfile(raw); pErr == nil {
				resumeProfile = rp
			}
		}
	} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
		slog.Warn("读取当前简历失败", "error", err.Error())
	}

	// ---------- 批内去重 ----------
	candidates, extras, stats := DedupInBatch(raws)
	slog.Info("浏览器脚本批内去重完成",
		"input", stats.InputCount, "unique", stats.Unique, "noise", stats.DroppedNoise)

	if len(candidates) > maxCandidatesPerRun {
		slog.Warn("浏览器脚本候选过多，仅处理前 N 条", "limit", maxCandidatesPerRun)
		candidates = candidates[:maxCandidatesPerRun]
	}

	// ---------- 逐个丰富 + 评分 + 落库 ----------
	cityScores := config.BuildCityScores(profile.PreferredLocations)
	resumeBrief := resumeProfile.Brief()

	res.Total = len(candidates)
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxLLMConcurrency)
	for i := range candidates {
		c := candidates[i]
		g.Go(func() error {
			created, _, reason, err := p.processCandidate(gctx, userID, c, profile, cityScores, resumeBrief, true)
			if err != nil {
				slog.Warn("处理岗位候选失败", "url", c.NormalizedURL, "error", err.Error())
				return nil
			}
			mu.Lock()
			defer mu.Unlock()
			if created {
				res.NewCount++
			} else if reason == "duplicate" {
				res.DupCount++
			} else {
				res.Skipped++
				if reason != "" {
					res.SkippedReasons[reason]++
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return res, err
	}

	// 为批内被合并掉的来源补记 job_sources。
	p.recordExtraSources(ctx, extras)
	return res, nil
}

// processCandidate 处理单个候选：查库去重 → 丰富内容 → 评分 → 落库。
// 返回是否新建、最终匹配分、跳过原因。
//
// trustSource=true 表示候选来自可信来源（如浏览器半固定脚本从官方详情页抽取的
// 真实岗位链接）：此时跳过「AI 页面类型判定」这一闸，且 LLM 解析/匹配失败时降级
// 为规则分 + 原始正文落库，而不是整条丢弃。因为这类内容可信度远高于联网搜索的
// 脏文本，不应因 LLM 临时不可用（如 402 额度耗尽）而颗粒无收。
func (p *Pipeline) processCandidate(
	ctx context.Context,
	userID model.ID,
	c Candidate,
	profile *model.JobProfile,
	cityScores map[string]int,
	resumeBrief string,
	trustSource bool,
) (ok bool, score int, reason string, err error) {
	// 单个候选处理中的任何 panic 都不能拖垮整个 worker：捕获后降级为「跳过该候选」。
	defer func() {
		if r := recover(); r != nil {
			slog.Error("处理候选时发生 panic，已跳过该候选",
				"url", c.NormalizedURL, "panic", fmt.Sprintf("%v", r))
			ok, score, reason, err = false, 0, "panic", fmt.Errorf("候选处理异常（已跳过）: %v", r)
		}
	}()
	// ---- 跨批次去重：已按用户要求暂时停用 ----
	// 这里不做复杂的语义合并，只用确定性的 URL / 指纹判断「是否已经入库」。
	// 命中后刷新来源与来源侧元数据并短路，避免重复爬取时先调用 LLM、最后再撞唯一索引。
	if existing, found, err := p.findExistingCandidate(ctx, c); err != nil {
		return false, 0, "dedup_lookup_failed", err
	} else if found {
		p.refreshExistingJob(ctx, existing, c)
		return false, existing.MatchScore, "duplicate", nil
	}

	// ---- 获取更完整的页面文本 ----
	pageText := CleanText(c.Raw.Content)
	if pageText == "" {
		pageText = CleanText(c.Raw.Snippet)
	}
	if len([]rune(pageText)) < 300 && LooksLikeJobURL(c.Raw.URL) {
		if fetched := p.tryFetch(ctx, c.Raw.URL); fetched != "" {
			pageText = fetched
		}
	}
	// 清洗网页噪声并修复被换行切断的段落：
	// StripHTML 会把导航菜单、页脚等拆成独立行混入正文，
	// 导致 JD 断行且夹杂「了解更多」这类无关文字。
	pageText = CleanJDText(pageText)
	listOnly := isOfficialListOnlyCandidate(c, trustSource, pageText)
	if strings.TrimSpace(pageText) == "" && !listOnly {
		// 完全没有可用文本，不虚构内容，直接跳过。
		return false, 0, "empty_text", errors.New("候选缺少可用文本")
	}

	// ---- AI 页面类型判定：聚合页 / 具体岗位页 / 无关页 ----
	// 不能靠 URL 规则（聚合页形态千变万化，规则永远补不完），直接把真实页面内容
	// 交给模型判断。命中非岗位详情页则跳过，且能省掉后面更贵的 ParseJD + MatchJob 调用。
	//
	// 判定失败时同样跳过：模型不可用时放行会导致聚合页/无关页被当成岗位入库，
	// 这正是「搜出来根本不是具体岗位信息」的噪声来源。宁可漏，不可错。
	//
	// 例外：trustSource（浏览器脚本抽取的官方详情页）本身已通过 URL 形态校验，
	// 可信度足够，跳过此闸；即便 LLM 不可用也让岗位正常落库。
	if !trustSource && p.llm.Enabled() {
		pageType, perr := p.llm.ClassifyPage(ctx, c.Title, pageText, c.Raw.URL)
		if perr != nil {
			slog.Warn("页面类型判定失败，跳过该候选", "url", c.Raw.URL, "error", perr.Error())
			return false, 0, "classify_failed", nil
		}
		if pageType != ai.PageTypeJobDetail {
			slog.Info("跳过非岗位详情页", "page_type", pageType, "title", c.Title, "url", c.Raw.URL)
			return false, 0, "not_job_detail", nil
		}
	}

	// ---- 描述质量检测：过滤 Tavily 退化为网页标题的垃圾数据 ----
	descText, descQuality := DetectDescriptionQuality(pageText)
	if descQuality == DescQualityEmpty {
		slog.Warn("岗位描述质量检测未通过，疑似网页标题而非 JD 正文",
			"url", c.NormalizedURL, "raw_preview", TruncateRunes(strings.TrimSpace(pageText), 80))
	}

	// ---- 实习/校招岗位过滤 ----
	// 仅对不可信来源（联网搜索脏文本）启用：Tavily 常混入大量无关实习/校招噪声。
	// 浏览器半固定脚本来自官方详情页、且用户主动指定了校招入口，不应再被此过滤器丢弃。
	if !trustSource && LooksLikeInternship(c.Title, descText) {
		slog.Info("跳过实习/校招类岗位",
			"title", c.Title, "url", c.NormalizedURL)
		return false, 0, "internship", nil
	}

	// ---- LLM 结构化解析（失败则退化为原始文本） ----
	job := &model.Job{
		CompanyName:          firstNonEmpty(c.CompanyName, guessCompanyFromTitle(c.Title), "未知公司"),
		Title:                firstNonEmpty(c.Title, "未知岗位"),
		Location:             c.Location,
		SourceURL:            c.Raw.URL,
		NormalizedURL:        c.NormalizedURL,
		SourceType:           c.Raw.SourceType,
		Description:          TruncateRunes(descText, 20000),
		DescQuality:          string(descQuality),
		CrawledAt:            time.Now().UTC(),
		Status:               model.JobStatusNew,
		PublishedAt:          c.Raw.PublishedAt,
		Locations:            model.JSONStringArray{},
		Responsibilities:     model.JSONStringArray{},
		Requirements:         model.JSONStringArray{},
		LanguageRequirements: model.JSONStringArray{},
		TechnicalStack:       model.JSONStringArray{},
	}
	if c.Raw.SourceType == model.SourceOfficial {
		job.OfficialURL = c.Raw.URL
	}

	if p.llm.Enabled() && !listOnly && pageText != "" {
		parsed, err := p.llm.ParseJD(ctx, pageText, c.Raw.URL, c.Title)
		if err != nil {
			slog.Warn("JD 解析失败，保留原始文本", "url", c.NormalizedURL, "error", err.Error())
		} else {
			applyParsed(job, parsed)
		}
	}

	// 来源提供的权威结构化元数据优先级最高，覆盖模型解析结果。
	// 官方接口（如腾讯）直接返回事业群/业务线，比模型从长文本中提取更可靠，
	// 可修复 department / business 长期为空的问题。
	applySourceMeta(job, c.Raw.Meta)

	// 重新计算指纹（解析后公司名可能更准确）。
	// 纳入 URLID：让不同 postid 的同名岗位拥有不同指纹。
	job.DedupFingerprint = URLIDFingerprint(job.CompanyName, job.Title, job.Location, ExtractURLJobID(c.NormalizedURL))
	if existing, found, err := p.findExistingJob(ctx, job); err != nil {
		return false, 0, "dedup_lookup_failed", err
	} else if found {
		p.refreshExistingJob(ctx, existing, c)
		return false, existing.MatchScore, "duplicate", nil
	}

	// ---- 评分 ----
	rule := ComputeRuleScore(ScoreInput{Job: job, Profile: profile, CityScores: cityScores})

	var llmResult *ai.MatchResult
	if p.llm.Enabled() && !listOnly {
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
		if r, err := p.llm.MatchJob(ctx, in); err != nil {
			slog.Warn("岗位匹配分析失败，使用规则分", "url", c.NormalizedURL, "error", err.Error())
		} else {
			llmResult = r
		}
	}

	analysis := FuseScores(rule, llmResult)
	analysis.AnalyzedAt = time.Now().UTC().Format(time.RFC3339)
	job.MatchScore = analysis.Score
	if m, err := toJSONMap(analysis); err == nil {
		job.MatchAnalysis = m
	}

	// ---- 落库 ----
	// 注意：此处【绝不】静默吞掉错误。早期版本把任何 Create 失败都当成
	// 「并发撞唯一索引 → 视为重复」，并用 Debug 级别打印，
	// 导致真实的落库失败（唯一索引冲突、字段超长、约束违反等）完全不可见，
	// 表现为「明明爬到了、LLM 也分析了，但数据库里就是没有」。
	// 现在统一以 Error 级别打印真实错误，便于定位。
	if err := p.store.Job.Create(ctx, job); err != nil {
		if isUniqueViolation(err) {
			if existing, found, findErr := p.findExistingJob(ctx, job); findErr != nil {
				slog.Error("岗位唯一索引冲突后查重失败",
					"url", c.NormalizedURL,
					"title", job.Title,
					"company", job.CompanyName,
					"error", findErr.Error())
			} else if found {
				p.refreshExistingJob(ctx, existing, c)
				return false, existing.MatchScore, "duplicate", nil
			}
		}
		slog.Error("岗位落库失败",
			"url", c.NormalizedURL,
			"title", job.Title,
			"company", job.CompanyName,
			"source_type", job.SourceType,
			"fingerprint", job.DedupFingerprint,
			"error", err.Error())
		return false, 0, "insert_failed", err
	}
	p.upsertSource(ctx, job.ID, c.Raw)
	return true, job.MatchScore, "", nil
}

// isOfficialListOnlyCandidate 标记“已由站点列表接口确认存在，但详情尚未取得”的岗位。
// IdentityURL 是列表接口加岗位稳定 ID 的内部身份，不会展示为用户可点击原网页。
func isOfficialListOnlyCandidate(c Candidate, trustSource bool, pageText string) bool {
	return trustSource &&
		c.Raw.SourceType == model.SourceOfficial &&
		strings.TrimSpace(c.Raw.URL) == "" &&
		strings.TrimSpace(c.Raw.IdentityURL) != "" &&
		strings.TrimSpace(pageText) == ""
}

func (p *Pipeline) findExistingCandidate(ctx context.Context, c Candidate) (*model.Job, bool, error) {
	officialURL := ""
	if c.Raw.SourceType == model.SourceOfficial {
		officialURL = c.Raw.URL
	}
	existing, err := p.store.Job.FindDuplicate(ctx, officialURL, c.NormalizedURL, c.Fingerprint)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return existing, existing != nil, nil
}

func (p *Pipeline) findExistingJob(ctx context.Context, job *model.Job) (*model.Job, bool, error) {
	if job == nil {
		return nil, false, nil
	}
	existing, err := p.store.Job.FindDuplicate(ctx, job.OfficialURL, job.NormalizedURL, job.DedupFingerprint)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return existing, existing != nil, nil
}

func (p *Pipeline) refreshExistingJob(ctx context.Context, existing *model.Job, c Candidate) {
	if existing == nil {
		return
	}
	fields := existingJobRefreshFields(existing, c.Raw)
	if len(fields) > 0 {
		if err := p.store.Job.UpdateEnrichment(ctx, existing.ID, fields); err != nil {
			slog.Warn("刷新已存在岗位字段失败",
				"job_id", existing.ID.String(),
				"url", c.NormalizedURL,
				"error", err.Error())
		}
	}
	p.upsertSource(ctx, existing.ID, c.Raw)
}

func existingJobRefreshFields(existing *model.Job, raw source.RawJob) map[string]any {
	fields := map[string]any{"crawled_at": time.Now().UTC()}
	if existing == nil {
		return fields
	}
	if raw.SourceType == model.SourceOfficial && cleanJobScalar(raw.URL) != "" {
		if cleanJobScalar(existing.SourceURL) == "" {
			fields["source_url"] = raw.URL
		}
		if cleanJobScalar(existing.OfficialURL) == "" {
			fields["official_url"] = raw.URL
		}
	}
	if raw.PublishedAt != nil && existing.PublishedAt == nil {
		fields["published_at"] = raw.PublishedAt
	}
	if v := cleanJobScalar(raw.Meta["Department"]); v != "" {
		fields["department"] = v
	}
	if v := cleanJobScalar(raw.Meta["Business"]); v != "" {
		fields["business"] = v
	}
	if v := cleanJobScalar(raw.Meta["Location"]); v != "" {
		if city := NormalizeCity(v); city != "" {
			fields["location"] = city
			fields["locations"] = model.JSONStringArray{city}
		}
	}
	if text := CleanJDText(CleanText(raw.Content)); text != "" && shouldRefreshDescription(existing) {
		descText, descQuality := DetectDescriptionQuality(text)
		if descQuality != DescQualityEmpty {
			fields["description"] = TruncateRunes(descText, 20000)
			fields["desc_quality"] = string(descQuality)
		}
	}
	return fields
}

func shouldRefreshDescription(existing *model.Job) bool {
	if existing == nil {
		return false
	}
	return strings.TrimSpace(existing.Description) == "" || existing.DescQuality == "" || existing.DescQuality != string(DescQualityFull)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key value") || strings.Contains(msg, "unique constraint")
}

// tryFetch 抓取页面正文。任何失败都静默降级，不影响任务。
func (p *Pipeline) tryFetch(ctx context.Context, rawURL string) string {
	if p.fetcher == nil {
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	res, err := p.fetcher.Get(fetchCtx, rawURL, nil)
	if err != nil {
		slog.Debug("抓取页面失败", "error", err.Error())
		return ""
	}
	if res.StatusCode != 200 {
		return ""
	}
	if !strings.Contains(strings.ToLower(res.ContentType), "html") &&
		!strings.Contains(strings.ToLower(res.ContentType), "text") {
		return ""
	}
	return TruncateRunes(CleanText(StripHTML(string(res.Body))), 20000)
}

// upsertSource 记录岗位来源。
func (p *Pipeline) upsertSource(ctx context.Context, jobID model.ID, raw source.RawJob) {
	now := time.Now().UTC()
	err := p.store.Job.UpsertSource(ctx, &model.JobSource{
		JobID:       jobID,
		SourceType:  raw.SourceType,
		SourceName:  raw.SourceName,
		URL:         raw.URL,
		FirstSeenAt: now,
		LastSeenAt:  now,
		IsValid:     true,
	})
	if err != nil {
		slog.Debug("记录岗位来源失败", "error", err.Error())
	}
}

// recordExtraSources 为批内被合并掉的来源补记 job_sources。
func (p *Pipeline) recordExtraSources(ctx context.Context, extras []source.RawJob) {
	for _, raw := range extras {
		norm := NormalizeURL(raw.URL)
		if norm == "" {
			continue
		}
		job, err := p.store.Job.FindDuplicate(ctx, "", norm, "")
		if err != nil || job == nil {
			continue
		}
		p.upsertSource(ctx, job.ID, raw)
	}
}

// applyParsed 把 LLM 解析结果写入 Job，仅覆盖非空字段。
// applySourceMeta 用来源提供的权威结构化字段覆盖岗位字段。
//
// 只接受白名单内的键，且仅在该值非空时覆盖，
// 避免来源误传任意字段污染数据。
func applySourceMeta(job *model.Job, meta map[string]string) {
	if len(meta) == 0 || job == nil {
		return
	}
	if v := cleanJobScalar(meta["Company"]); v != "" {
		job.CompanyName = v
	}
	if v := cleanJobScalar(meta["Department"]); v != "" {
		job.Department = v
	}
	if v := cleanJobScalar(meta["Business"]); v != "" {
		job.Business = v
	}
	if v := cleanJobScalar(meta["Location"]); v != "" {
		if city := NormalizeCity(v); city != "" {
			job.Location = city
			job.Locations = model.JSONStringArray{city}
		}
	}
}

func applyParsed(job *model.Job, parsed *ai.ParsedJob) {
	if parsed == nil {
		return
	}
	if v := cleanJobScalar(parsed.CompanyName); v != "" {
		job.CompanyName = v
	}
	if v := cleanJobScalar(parsed.Title); v != "" {
		job.Title = v
	}
	if v := cleanJobScalar(parsed.Department); v != "" {
		job.Department = v
	}
	if v := cleanJobScalar(parsed.Business); v != "" {
		job.Business = v
	}
	if len(parsed.Locations) > 0 {
		cities := make([]string, 0, len(parsed.Locations))
		for _, l := range parsed.Locations {
			if c := NormalizeCity(l); c != "" {
				cities = append(cities, c)
			}
		}
		if len(cities) > 0 {
			job.Locations = cities
			job.Location = cities[0]
		}
	}
	if v := cleanJobScalar(parsed.JobType); v != "" {
		job.JobType = v
	}
	if parsed.GraduationYear > 0 {
		y := parsed.GraduationYear
		job.GraduationYear = &y
	}
	if len(parsed.Responsibilities) > 0 {
		job.Responsibilities = parsed.Responsibilities
	}
	if len(parsed.Requirements) > 0 {
		job.Requirements = parsed.Requirements
	}
	if len(parsed.Languages) > 0 {
		job.LanguageRequirements = parsed.Languages
	}
	if len(parsed.TechnicalStack) > 0 {
		job.TechnicalStack = parsed.TechnicalStack
	}
	// 截止日期只在能明确解析时才写入，绝不推测。
	if parsed.Deadline != "" {
		if t := parseISODate(parsed.Deadline); t != nil {
			job.Deadline = t
		}
	}
}

func cleanJobScalar(v string) string {
	s := strings.TrimSpace(v)
	switch strings.ToLower(s) {
	case "", "<nil>", "nil", "null", "undefined":
		return ""
	default:
		return s
	}
}

// parseISODate 解析 ISO 风格日期。
func parseISODate(s string) *time.Time {
	layouts := []string{time.RFC3339, "2006-01-02", "2006/01/02", "2006-01-02 15:04:05"}
	for _, l := range layouts {
		if t, err := time.Parse(l, strings.TrimSpace(s)); err == nil {
			u := t.UTC()
			if u.Year() >= 2000 && u.Year() <= 2100 {
				return &u
			}
		}
	}
	return nil
}

// toJSONMap 把结构体转成 model.JSONMap。
func toJSONMap(v any) (model.JSONMap, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := model.JSONMap{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// guessCompanyFromTitle 从标题中提取公司名线索。
// 只做保守的分隔符切分，切不出来就返回空，绝不编造。
func guessCompanyFromTitle(title string) string {
	for _, sep := range []string{"-", "|", "｜", "—", "招聘"} {
		if i := strings.Index(title, sep); i > 0 {
			cand := strings.TrimSpace(title[:i])
			// 公司名通常较短。
			if n := len([]rune(cand)); n >= 2 && n <= 20 {
				return cand
			}
		}
	}
	return ""
}

// sanitizeErr 生成可安全存库与展示的错误描述。
func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// 兜底截断，防止把大段上游响应写进数据库。
	return TruncateRunes(msg, 300)
}
