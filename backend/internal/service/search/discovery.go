package search

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
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
func NewDiscoveryService(reg *site.Registry, crawler *SiteCrawler, runs *site.Repository) *DiscoveryService {
	return &DiscoveryService{
		registry: reg,
		executor: executor.New(crawler, runs),
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
	Recipe *site.Recipe
	// Jobs 采集到的原始岗位；未命中或执行失败时为 nil。
	Jobs []source.RawJob
	// JobsFound 采集到的岗位数。
	JobsFound int
}

// FastPath 尝试走站点 Recipe 快速通道。
//
// 判定顺序：site_key → URL 域名 → 公司名，任一命中即用对应 Recipe 采集。
// 未命中返回 Hit=false，调用方应回退到 Discovery Path（AI 导航或联网搜索）。
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
		return &DiscoveryResult{Hit: true, Recipe: rc}, err
	}

	return &DiscoveryResult{
		Hit:       true,
		Recipe:    rc,
		Jobs:      res.Jobs,
		JobsFound: len(res.Jobs),
	}, nil
}

// ExploreSiteResult 是 Discovery Path 的输出。
type ExploreSiteResult struct {
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
// saveAsRecipe 为 true 且探索成功时，会把候选写入 site_recipes 并重载 Registry，
// 使该站点下次直接走 Fast Path（这是「探索一次、长期复用」的关键）。
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
		Success:    res.Success,
		Candidate:  res.Candidate,
		Trace:      res.Trace,
		Reason:     res.Reason,
		DurationMS: res.DurationMS,
	}
	if !res.Success || res.Candidate == nil {
		return out, nil
	}

	// 按需保存为 Recipe，让下次走 Fast Path。
	if saveAsRecipe && d.registry != nil {
		rc, err := saveExploredRecipe(ctx, d.registry, p, res.Candidate)
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

// saveExploredRecipe 把探索产出的候选保存为站点 Recipe 并重载 Registry。
//
// 保存的 Recipe 默认 strategy_type 为 browser（第一版 Executor 只实现了该策略），
// adapter_key 用站点标识——新站点在 Worker 侧尚未实现适配器的情况下，
// 这条记录主要作为「站点已知」的标记与人工完善的起点。
func saveExploredRecipe(
	ctx context.Context,
	reg *site.Registry,
	p DiscoveryParams,
	cand *ai.RecipeCandidate,
) (*site.Recipe, error) {
	repo := reg.Repository()
	if repo == nil {
		return nil, fmt.Errorf("Registry 未绑定仓储，无法保存")
	}

	// 站点标识：优先用调用方给的，其次从入口 URL 域名推导。
	siteKey := strings.TrimSpace(p.SiteKey)
	domain := ""
	if u, err := url.Parse(p.URL); err == nil {
		domain = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if siteKey == "" && domain != "" {
			siteKey = domainToSiteKey(domain)
		}
	}
	if siteKey == "" {
		return nil, fmt.Errorf("无法确定站点标识")
	}

	// 已存在则更新（保留 id 与创建时间），否则新建。
	existing, err := repo.GetBySiteKey(ctx, siteKey)
	if err != nil {
		return nil, err
	}

	notes := cand.Notes
	if notes == "" {
		notes = "由 Exploration Agent 自动生成"
	}

	if existing != nil {
		existing.CampusURL = p.URL
		existing.Notes = notes
		existing.Source = site.SourceExploration
		existing.Version = existing.Version + 1
		if err := repo.Update(ctx, existing); err != nil {
			return nil, err
		}
		if err := reg.Reload(ctx); err != nil {
			return nil, err
		}
		return existing, nil
	}

	rc := &site.Recipe{
		SiteKey:          siteKey,
		CompanyName:      defaultCompanyName(p.Company, siteKey),
		Domain:           domain,
		CampusURL:        p.URL,
		StrategyType:     site.StrategyBrowser,
		AdapterKey:       siteKey,
		Enabled:          true,
		MaxJobsPerSearch: 20,
		// 探索产出的是接口信息，JD 需后续完善；先按 0（不额外抓详情页）保存，
		// 待人工确认接口可用后再调整。
		MaxDetailFetches: 0,
		Notes:            notes,
		Source:           site.SourceExploration,
		Version:          1,
	}
	if err := repo.Create(ctx, rc); err != nil {
		return nil, err
	}
	if err := reg.Reload(ctx); err != nil {
		return nil, err
	}
	return rc, nil
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
