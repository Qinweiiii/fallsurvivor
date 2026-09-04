package search

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
	"github.com/eddiel/fallsurvivor/backend/pkg/safefetch"
)

// DiscoveryService 编排「先查 Recipe、再决定怎么采」的两条路径：
//
//   - Fast Path：命中已配置的站点 Recipe，直接按 Recipe 采集。
//     数据驱动，新增一家公司只需加一条 Recipe 记录，不改代码。
//   - Discovery Path：未命中，交由 Exploration Agent 自动探索站点结构，
//     产出候选配置并（可选）保存为新 Recipe，下次即走 Fast Path。
//
// 依赖方向说明：本包导入 executor，而 executor 只依赖 site/source，
// 其 Crawler 接口由 *SiteCrawler 隐式实现，因此不存在循环依赖。
type DiscoveryService struct {
	registry *site.Registry
	executor *executor.Executor
	explorer *Explorer
}

// NewDiscoveryService 构造发现服务。
// crawler 提供底层采集能力（实现 executor.Crawler 接口），runs 用于记录执行历史（可为 nil）。
func NewDiscoveryService(reg *site.Registry, crawler *SiteCrawler, runs *site.Repository, fetcher *safefetch.Client) *DiscoveryService {
	return &DiscoveryService{
		registry: reg,
		executor: executor.New(crawler, runs, fetcher),
	}
}

// AttachExplorer 注入探索器，启用 Discovery Path。
// 单独注入是为了让探索能力可选：未注入时未命中即返回，不做自动探索。
func (d *DiscoveryService) AttachExplorer(e *Explorer) {
	d.explorer = e
}

// DiscoveryParams 一次发现请求的参数。
type DiscoveryParams struct {
	// TaskID 复用 Worker 浏览器会话与登录态。
	TaskID string
	// SiteKey 显式指定的站点标识（前端表单或 Recipe 配置）。
	SiteKey string
	// URL 目标 URL，用于按域名反查站点。
	URL string
	// Company 公司名，用于按公司名反查站点。
	Company string
	// Keyword 搜索关键词。
	Keyword string
}

// DiscoveryResult 发现结果。
type DiscoveryResult struct {
	// Hit 是否命中了站点 Recipe（true 表示走的是 Fast Path）。
	Hit bool
	// Recipe 命中的站点配置；未命中时为 nil。
	// NeedsRexplore 为 true 时，这里是那条已失效的配置（供排查参考）。
	Recipe *site.Recipe
	// Jobs 采集到的原始岗位；未命中或执行失败时为 nil。
	Jobs []source.RawJob
	// JobsFound 采集到的岗位数。
	JobsFound int
	// FieldQuality 关键字段命中质量。
	FieldQuality []executor.FieldQuality
	// NeedsRexplore 命中的 Recipe 已失效，需要重新探索。
	//
	// 与「单纯未命中」区分开是有意义的：调用方可以据此在日志或前端
	// 提示「该站点配置已失效，正在重新摸索」，而不是让用户以为从未配过。
	NeedsRexplore bool
	// RexploreReason 触发重探的原因（上次失败信息）。
	RexploreReason string
}

// FastPath 尝试走站点 Recipe 快速通道。
//
// 判定顺序：site_key → URL 域名 → 公司名，任一命中即用对应 Recipe 采集。
// 未命中返回 Hit=false，调用方应回退到 Discovery Path（自动探索）。
//
// 自愈：命中的 Recipe 若已被标记为 invalid（连续失败达阈值），
// 视为未命中并返回 NeedsRexplore=true，让调用方重新探索。
// 抱着一条已知跑不通的配置反复执行毫无意义，只会累积失败记录。
//
// 注意：未命中不算错误，返回 nil error，由调用方决定后续行为。
func (d *DiscoveryService) FastPath(ctx context.Context, p DiscoveryParams) (*DiscoveryResult, error) {
	if d.registry == nil {
		return &DiscoveryResult{Hit: false}, nil
	}

	rc := d.registry.Match(p.SiteKey, p.URL, p.Company)
	if rc == nil {
		slog.Debug("未命中站点 Recipe，回退通用发现路径",
			"site_key", p.SiteKey, "url", p.URL, "company", p.Company)
		return &DiscoveryResult{Hit: false}, nil
	}

	// 自愈触发点：配置已失效，交回探索路径重新摸索。
	if rc.VerifyStatus == site.VerifyInvalid {
		slog.Warn("站点 Recipe 已标记失效，转为重新探索",
			"site_key", rc.SiteKey,
			"consecutive_failures", rc.ConsecutiveFailures,
			"last_error", rc.LastError)
		return &DiscoveryResult{
			Hit:            false,
			Recipe:         rc,
			NeedsRexplore:  true,
			RexploreReason: rc.LastError,
		}, nil
	}

	slog.Info("命中站点 Recipe，走 Fast Path",
		"site_key", rc.SiteKey, "company", rc.CompanyName, "strategy", rc.StrategyType)

	res, err := d.executor.Execute(ctx, executor.Params{
		TaskID:   p.TaskID,
		Recipe:   rc,
		Keyword:  p.Keyword,
		StartURL: p.URL,
	})
	if err != nil {
		// 命中了 Recipe 但执行失败：把错误交给调用方，
		// 由调用方决定是否回退 Discovery Path 或直接上报。
		//
		// 注意执行失败已由 Executor 计入连续失败计数；
		// 累计到阈值后下一次命中就会走上面的重探分支。
		return &DiscoveryResult{Hit: true, Recipe: rc}, err
	}

	return &DiscoveryResult{
		Hit:          true,
		Recipe:       rc,
		Jobs:         res.Jobs,
		JobsFound:    len(res.Jobs),
		FieldQuality: executor.BuildFieldQuality(res.Jobs),
	}, nil
}

// ExploreSiteResult 是 Discovery Path 的输出。
type ExploreSiteResult struct {
	// RunID 对应探索运行记录；失败时可据此读取完整动作轨迹。
	RunID string
	// Success 是否产出可用配置。
	Success bool
	// Recipe 保存后的站点配置；未保存（或保存失败）时为 nil。
	Recipe *site.Recipe
	// Candidate 探索产出的配置候选。
	Candidate *ai.RecipeCandidate
	// Trace 探索轨迹，便于排查。
	Trace []ExploreStepTrace
	// Saved 是否已写入 Registry（下次即可走 Fast Path）。
	Saved bool
	// Verified 配置是否通过了真实请求验证。
	Verified bool
	// VerifiedJobs 验证时实际采到的岗位数。
	VerifiedJobs int
	// VerifySampleTitles 验证时采到的前几条岗位标题，便于人工确认采对了没。
	VerifySampleTitles []string
	// FieldQuality 关键字段命中质量。
	FieldQuality []executor.FieldQuality
	// Jobs 是首次探索时页面真实响应解析出的岗位，可直接入库。
	Jobs []source.RawJob
	// RefineRounds 自修正轮数（0 表示首次尝试即通过）。
	RefineRounds int
	// Reason 失败或跳过的说明。
	Reason string
	// DurationMS 耗时。
	DurationMS int64
}

// ExploreSite 在未知站点上自动探索采集方式（Discovery Path）。
//
// 只在 Fast Path 未命中时调用——探索需要占用浏览器与多次 LLM 调用，
// 已知站点不应重复探索。
//
// 完整闭环（这是「不用人肉适配每个站点」的核心）：
//
//	探索观测（含真实请求体）→ 产出配置 → **真实请求验证**
//	→ 失败则把错误喂回模型自修正（最多 3 轮）→ 仅验证通过才落库
//
// saveAsRecipe 为 true 且**验证通过**时，才把候选写入 site_recipes 并重载
// Registry，使该站点下次直接走 Fast Path。
// 未通过验证的配置不会落库——落一条跑不通的配置只会制造后续排查成本。
func (d *DiscoveryService) ExploreSite(ctx context.Context, p DiscoveryParams, saveAsRecipe bool) (*ExploreSiteResult, error) {
	if d.explorer == nil {
		return &ExploreSiteResult{Reason: "未启用探索能力"}, nil
	}

	res, err := d.explorer.Explore(ctx, ExploreRequest{
		SiteKey:  p.SiteKey,
		EntryURL: p.URL,
		Keyword:  p.Keyword,
	})
	if err != nil {
		return nil, err
	}

	out := &ExploreSiteResult{
		RunID:      res.RunID,
		Success:    res.Success,
		Candidate:  res.Candidate,
		Trace:      res.Trace,
		Reason:     res.Reason,
		DurationMS: res.DurationMS,
	}
	if !res.Success || res.Candidate == nil {
		return out, nil
	}

	// 探索成功的唯一依据是当前页面真实返回的数据能否被解析。这里绝不额外
	// 发起接口复放；下一次真实搜索才是 Recipe 可复用性的验证。
	cand := res.Candidate
	draft := recipeFromCandidate(p, cand)
	verify := verifyWithObservedResponse(draft, cand, res.Observed, res.DetailContents)
	out.Verified = verify.OK
	out.VerifiedJobs = verify.JobsFound
	out.VerifySampleTitles = verify.SampleTitles
	out.FieldQuality = verify.FieldQuality
	if jobs, err := jobsFromObservedResponse(draft, cand, res.Observed, res.DetailContents); err == nil {
		out.Jobs = jobs
	}

	if !verify.OK {
		out.Success = false
		if verify.Error == "" {
			verify.Error = "未找到可解析的岗位列表响应"
		}
		slog.Warn("探索候选未通过页面响应验证",
			"site_key", p.SiteKey,
			"list_api", cand.ListAPI,
			"list_path", cand.ListPath,
			"title_field", cand.TitleField,
			"error", verify.Error)
		out.Reason = "当前页面已观测到响应，但岗位字段无法解析；未保存 Recipe: " + verify.Error
		return out, nil
	}

	// 只有验证通过的配置才落库。
	if saveAsRecipe && d.registry != nil {
		rc, err := saveExploredRecipe(ctx, d.registry, p, cand, verify, browserObservedEntryURL(p.URL, res.Trace), browserPlanFromTrace(res.Trace))
		if err != nil {
			slog.Warn("保存探索结果失败", "site_key", p.SiteKey, "error", err.Error())
			out.Reason = "探索成功但保存失败: " + err.Error()
			return out, nil
		}
		out.Recipe = rc
		out.Saved = true
	}
	return out, nil
}

// saveExploredRecipe 把**已验证通过**的候选保存为站点 Recipe 并重载 Registry。
//
// 保存的 Recipe strategy_type 为 api：探索阶段已经找到并验证了岗位列表接口，
// 后续搜索应复用这条稳定路径，而不是每次重新打开浏览器探索。
//
// 同时写入健康度字段（verify_status / verified_jobs / verified_at），
// 让后续的失效自愈逻辑有判断依据。
func saveExploredRecipe(
	ctx context.Context,
	reg *site.Registry,
	p DiscoveryParams,
	cand *ai.RecipeCandidate,
	verify executor.VerifyResult,
	entryURL string,
	browserPlan model.JSONMap,
) (*site.Recipe, error) {
	repo := reg.Repository()
	if repo == nil {
		return nil, fmt.Errorf("Registry 未绑定仓储，无法保存")
	}

	draft := recipeFromCandidate(p, cand)
	if strings.TrimSpace(draft.DetailURLTemplate) != "" {
		draft.MaxDetailFetches = defaultMaxDetailFetches
	}
	if verify.BrowserBound {
		draft.StrategyType = site.StrategyBrowserObserved
		draft.BrowserPlan = browserPlan
	}
	if strings.TrimSpace(entryURL) != "" {
		draft.CampusURL = strings.TrimSpace(entryURL)
	}
	if draft.SiteKey == "" {
		return nil, fmt.Errorf("无法确定站点标识")
	}
	if draft.Notes = strings.TrimSpace(cand.Notes); draft.Notes == "" {
		draft.Notes = "由 Exploration Agent 自动探索并验证通过"
	}
	if verify.BrowserBound && !strings.Contains(draft.Notes, "页面动作") {
		draft.Notes = strings.TrimSpace(draft.Notes + "；保存页面动作并解析页面真实响应，不直接复放接口")
	}
	applyVerifyResult(draft, verify)

	// 已存在则更新（保留 id 与创建时间），否则新建。
	existing, err := repo.GetBySiteKey(ctx, draft.SiteKey)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		draft.ID = existing.ID
		draft.CreatedAt = existing.CreatedAt
		// 保留人工可能改过的采集上限，避免自动探索覆盖用户意图。
		if existing.MaxJobsPerSearch > 0 {
			draft.MaxJobsPerSearch = existing.MaxJobsPerSearch
		}
		draft.Version = existing.Version + 1
		if err := repo.Update(ctx, draft); err != nil {
			return nil, err
		}
		if err := reg.Reload(ctx); err != nil {
			return nil, err
		}
		return draft, nil
	}

	draft.Version = 1
	if err := repo.Create(ctx, draft); err != nil {
		return nil, err
	}
	if err := reg.Reload(ctx); err != nil {
		return nil, err
	}
	return draft, nil
}

func browserObservedEntryURL(original string, trace []ExploreStepTrace) string {
	base, err := url.Parse(strings.TrimSpace(original))
	if err != nil || base.Host == "" {
		return original
	}
	for i := len(trace) - 1; i >= 0; i-- {
		step := trace[i]
		target := strings.TrimSpace(step.CurrentURL)
		if target == "" && step.Action == "navigate" {
			target = strings.TrimSpace(step.Target)
		}
		if target == "" {
			continue
		}
		u, err := base.Parse(target)
		if err != nil || u.Host == "" {
			continue
		}
		if strings.EqualFold(u.Host, base.Host) {
			return u.String()
		}
	}
	return original
}

func browserPlanFromTrace(trace []ExploreStepTrace) model.JSONMap {
	actions := make([]any, 0, 3)
	hasSearch := false
	for _, step := range trace {
		if step.Result == "" || strings.Contains(step.Result, "失败") {
			continue
		}
		switch step.Action {
		case "navigate":
			if target := strings.TrimSpace(step.Target); target != "" {
				actions = append(actions, map[string]any{"type": "navigate", "url": target})
			}
		case "click":
			// 只有模型给出可读文案时才能跨会话复用；临时 ref 不保存。
			if step.TargetRef == "" && strings.TrimSpace(step.Target) != "" {
				actions = append(actions, map[string]any{"type": "click", "text": strings.TrimSpace(step.Target)})
			}
		case "search":
			hasSearch = true
		}
	}
	if !hasSearch {
		// 探索前的预搜索不一定写进 trace；搜索是通用且已由本次响应证明有效。
		hasSearch = true
	}
	if hasSearch {
		actions = append(actions, map[string]any{"type": "search"})
	}
	return model.JSONMap{"actions": actions}
}

/**
 * siteKeyAndDomain 从发现参数推导站点标识与域名。
 *
 * 站点标识优先用调用方给的，其次从入口 URL 域名推导
 * （如 campus.jd.com → campusjdcom），保证同一站点重复探索时
 * 命中同一条记录而不是每次新建。
 */
func siteKeyAndDomain(p DiscoveryParams) (string, string) {
	siteKey := strings.TrimSpace(p.SiteKey)
	domain := ""
	if u, err := url.Parse(p.URL); err == nil {
		domain = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if siteKey == "" && domain != "" {
			siteKey = domainToSiteKey(domain)
		}
	}
	return siteKey, domain
}

// domainToSiteKey 把域名转换为合法的站点标识（如 campus.jd.com → campusjd）。
func domainToSiteKey(domain string) string {
	var b strings.Builder
	for _, r := range domain {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func jsonMapFromStrings(in map[string]string) model.JSONMap {
	out := model.JSONMap{}
	for k, v := range in {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key != "" && val != "" {
			out[key] = val
		}
	}
	return out
}

// defaultCompanyName 优先用调用方给的公司名，否则用站点标识。
func defaultCompanyName(company, siteKey string) string {
	if c := strings.TrimSpace(company); c != "" {
		return c
	}
	return siteKey
}

// ReloadRecipes 重新装载 Recipe（配置变更后调用）。
func (d *DiscoveryService) ReloadRecipes(ctx context.Context) error {
	if d.registry == nil {
		return nil
	}
	return d.registry.Reload(ctx)
}

// VerifyRecipeResult 是一次手动验证的结果。
type VerifyRecipeResult struct {
	// OK 是否验证通过（真的采到了岗位）。
	OK bool `json:"ok"`
	// JobsFound 实际采到的岗位数。
	JobsFound int `json:"jobs_found"`
	// SampleTitles 采到的前几条岗位标题。
	SampleTitles []string `json:"sample_titles,omitempty"`
	// FieldQuality 关键字段命中质量。
	FieldQuality []executor.FieldQuality `json:"field_quality,omitempty"`
	// Error 失败原因。
	Error string `json:"error,omitempty"`
	// ResponseSample 实际拿到的响应片段，便于人工排查。
	ResponseSample string `json:"response_sample,omitempty"`
}

/**
 * VerifyRecipe 用真实请求验证一条已入库的 Recipe，并写回健康度。
 *
 * 用途：
 *   1. 人工在配置页录入后立即确认「这条配置能不能跑」；
 *   2. 确认历史遗留的 unverified 配置是否仍然有效。
 *
 * 与探索阶段的验证共用同一个 Executor.Verify——保证「手动验证通过」
 * 和「探索验证通过」是同一个口径，不会出现两套标准。
 *
 * 验证结果会写回数据库：通过则标记 verified 并清零失败计数，
 * 失败则累加失败计数（达阈值即转 invalid 触发自动重探）。
 */
func (d *DiscoveryService) VerifyRecipe(ctx context.Context, rc *site.Recipe, keyword string) *VerifyRecipeResult {
	if rc == nil {
		return &VerifyRecipeResult{Error: "站点配置为空"}
	}
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		kw = verifyKeywordFallback
	}

	res := d.executor.Verify(ctx, rc, kw)

	// 写回健康度：手动验证也应影响状态，否则用户点了「验证」却看不到变化。
	if repo := d.recipeRepo(); repo != nil {
		bg := context.WithoutCancel(ctx)
		var err error
		if res.OK {
			err = repo.MarkSuccess(bg, rc.SiteKey, res.JobsFound)
		} else {
			err = repo.MarkFailure(bg, rc.SiteKey, res.Error)
		}
		if err != nil {
			slog.Warn("写回验证结果失败", "site_key", rc.SiteKey, "error", err.Error())
		} else if d.registry != nil {
			// 状态变了，让 Registry 与库保持一致。
			if err := d.registry.Reload(bg); err != nil {
				slog.Warn("验证后重载 Registry 失败", "error", err.Error())
			}
		}
	}

	return &VerifyRecipeResult{
		OK:             res.OK,
		JobsFound:      res.JobsFound,
		SampleTitles:   res.SampleTitles,
		FieldQuality:   res.FieldQuality,
		Error:          res.Error,
		ResponseSample: res.ResponseSample,
	}
}

// recipeRepo 取底层仓储，未绑定时返回 nil。
func (d *DiscoveryService) recipeRepo() *site.Repository {
	if d.registry == nil {
		return nil
	}
	return d.registry.Repository()
}
