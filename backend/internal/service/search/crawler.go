package search

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

// SiteCrawler 负责按站点 Recipe 或浏览器动作采集岗位列表。
// 主目标是先拿到经过关键词筛选的岗位列表；详情页正文与原网页 URL
// 属于后置补全，不应阻塞列表岗位入库。
//
// 导航策略：已知站点走 Recipe Fast Path；未知站点统一交给 Explorer。
//
// 安全与资源约束：
//   - 复用 Worker 的持久登录态（按 siteKey），不处理密码；
//   - 每步导航动作由 Worker 端做安全校验（不执行任意 JS，navigate 仅公网地址）；
//   - 限制最大步数与最大发现岗位数，防止死循环与 token 失控；
//   - 若页面出现真实登录墙/验证页，交由用户在浏览器中先处理。
type SiteCrawler struct {
	browser *browser.Client
	llm     *ai.Client
}

// NewSiteCrawler 构造爬虫。
func NewSiteCrawler(b *browser.Client, llm *ai.Client) *SiteCrawler {
	return &SiteCrawler{browser: b, llm: llm}
}

// defaultMaxDetailFetches 是站点 Recipe 未配置 max_detail_fetches 时的兜底值，
// 限制每个列表页额外抓取详情页 JD 的岗位数，避免浏览器会话耗时过长。
const defaultMaxDetailFetches = 8

// crawlExtractJobs 是 Recipe 驱动的浏览器抽卡流程：打开站点、加载列表、抽取卡片。
// siteKey/adapter、company、sourceName、keyword 全部由 Recipe 或请求传入，不按公司硬编码分流。
//
// maxDetailFetches 控制「对多少条岗位打开详情页抓 JD」，由站点 Recipe 提供：
//   - <= 0：完全跳过详情页抓取，直接用 card.RawText 落库。
//     适用于 Worker 侧已在 extractJobs 阶段拿到完整 JD 的站点——
//     腾讯校招即如此（tencentAdapter 通过 jobDetails 接口拿到了 desc/request 与部门信息），
//     再去抓详情页 DOM 反而会覆盖掉这些结构化内容。
//   - > 0：对前 N 条卡片打开详情页抓取正文（字节等站点走此路径）。
//
// 注意：这里刻意用参数而非 siteKey 硬编码分支，使新增站点只需配 Recipe、无需改代码。
func (c *SiteCrawler) crawlExtractJobs(ctx context.Context, taskID, siteKey, startURL, company, sourceName, keyword string, maxDetailFetches int) ([]ai.NavJob, []source.RawJob, error) {
	if c.browser == nil || !c.browser.Health(ctx) {
		return nil, nil, browser.ErrWorkerUnavailable
	}

	if _, err := c.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID:  taskID,
		SiteKey: siteKey,
		URL:     startURL,
	}); err != nil {
		return nil, nil, fmt.Errorf("browser: 打开会话失败: %w", err)
	}
	defer func() { _ = c.browser.CloseSession(ctx, taskID) }()

	// 滚动加载列表（校招列表页多为无限滚动 / 前端过滤）。
	for i := 0; i < 3; i++ {
		if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{
			Type:      browser.NavScroll,
			Direction: "down",
			Amount:    1200,
		}); err != nil {
			slog.Warn(siteKey+" 列表滚动失败", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(800 * time.Millisecond):
		}
	}

	extracted, err := c.browser.ExtractJobs(ctx, taskID, keyword)
	if err != nil {
		return nil, nil, fmt.Errorf("browser: 抽取岗位卡片失败: %w", err)
	}
	if len(extracted.Jobs) == 0 {
		return nil, nil, fmt.Errorf("browser: 未在页面发现岗位卡片，请确认列表页已正确加载（可能需要先登录或更换关键词）")
	}

	navJobs := make([]ai.NavJob, 0, len(extracted.Jobs))
	raws := make([]source.RawJob, 0, len(extracted.Jobs))

	// maxDetailFetches <= 0：Worker 侧已在 extractJobs 拿到完整 JD，跳过详情页抓取。
	// 否则限制抓取条数，控制 LLM 与网络开销。
	limit := maxDetailFetches
	if limit > len(extracted.Jobs) {
		limit = len(extracted.Jobs)
	}

	for i, card := range extracted.Jobs {
		navJobs = append(navJobs, ai.NavJob{Title: card.Title, URL: card.URL})
		if maxDetailFetches <= 0 {
			// 跳过详情页：直接用 Worker 抽取阶段产出的 raw_text（已含完整 JD）。
			raws = append(raws, cardToRawJob(card, card.RawText, company, sourceName))
			continue
		}
		if i >= limit {
			// 超出详情页抓取上限：跳过浏览，content 落空字符串兜底，
			// pipeline 仍可使用 Card.RawText（已含 JD 摘要）与 Meta（Department）。
			raws = append(raws, cardToRawJob(card, "", company, sourceName))
			continue
		}
		// 打开详情页抓取 JD 正文（SPA 详情页由浏览器渲染，HTTP 直取只能拿到空壳）。
		if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavNavigate,
			URL:  card.URL,
		}); err != nil {
			slog.Warn(siteKey+" 详情页导航失败", "url", card.URL, "error", err.Error())
			raws = append(raws, cardToRawJob(card, card.RawText, company, sourceName))
			continue
		}
		// 详情页是重型 SPA，JD 正文靠接口异步渲染；导航后必须等待再抓取，
		// 否则会抓到加载壳（正文过短），被管线判为「缺少可用文本」而整批丢弃。
		if _, werr := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: 2}); werr != nil {
			slog.Warn(siteKey+" 详情页等待渲染失败", "error", werr.Error())
		}
		scrape, err := c.browser.ScrapePage(ctx, taskID)
		// 自适应：若首抓正文过短，再等一会并重抓一次，应对慢渲染。
		if err == nil && scrape != nil && len([]rune(strings.TrimSpace(scrape.PageText))) < 300 {
			if _, werr := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: 2}); werr != nil {
				slog.Warn(siteKey+" 详情页二次等待渲染失败", "error", werr.Error())
			}
			scrape, err = c.browser.ScrapePage(ctx, taskID)
		}
		if err != nil || scrape == nil {
			raws = append(raws, cardToRawJob(card, card.RawText, company, sourceName))
			continue
		}
		content := strings.TrimSpace(scrape.PageText)
		if content == "" {
			content = strings.TrimSpace(card.RawText)
		}
		raws = append(raws, cardToRawJob(card, content, company, sourceName))
	}

	return navJobs, raws, nil
}

// CrawlWithRecipe 按站点 Recipe 执行一次浏览器采集。
//
// 这是「数据驱动采集」的统一入口：全部参数从 Recipe 读取，
// 不再按 siteKey 走硬编码分支。新增一家公司 = 新增一条 Recipe 记录，无需改 Go 代码。
//
// 参数说明：
//   - rc：站点 Recipe，提供 site_key / company_name / adapter / 各项限制；
//   - startURL：搜索起点，为空时回退到 rc.CampusURL；
//   - keyword：搜索关键词，透传给 Worker adapter；
//
// 返回的 RawJob 已带权威 Meta（Company，以及 adapter 提供的 Department），
// 可直接交给 Pipeline 入库。
func (c *SiteCrawler) CrawlWithRecipe(ctx context.Context, taskID string, rc *site.Recipe, startURL, keyword string) ([]source.RawJob, error) {
	if rc == nil {
		return nil, fmt.Errorf("crawl: Recipe 为空")
	}
	if rc.StrategyType == site.StrategyBrowserObserved {
		return c.crawlBrowserObserved(ctx, taskID, rc, startURL, keyword)
	}
	// 站点标识：优先用 adapter_key（Worker 侧靠它选 adapter），回退到 site_key。
	siteKey := strings.TrimSpace(rc.AdapterKey)
	if siteKey == "" {
		siteKey = rc.SiteKey
	}
	entry := strings.TrimSpace(rc.CampusURL)
	if entry == "" {
		entry = strings.TrimSpace(startURL)
	}
	if entry == "" {
		return nil, fmt.Errorf("crawl: 站点 %s 未配置采集入口 URL", rc.SiteKey)
	}
	// 公司名作为权威 Meta 写入，避免依赖 LLM 从正文推断。
	company := strings.TrimSpace(rc.CompanyName)
	sourceName := company + "校招"

	maxDetail := rc.MaxDetailFetches
	// 未配置或配置为负时走兜底值，避免误配导致全量抓取。
	if rc.MaxDetailFetches < 0 {
		maxDetail = defaultMaxDetailFetches
	}

	_, raws, err := c.crawlExtractJobs(ctx, taskID, siteKey, entry, company, sourceName, keyword, maxDetail)
	if err != nil {
		return nil, err
	}

	// 按 Recipe 限制返回条数，控制下游 LLM 开销。
	if rc.MaxJobsPerSearch > 0 && len(raws) > rc.MaxJobsPerSearch {
		raws = raws[:rc.MaxJobsPerSearch]
	}
	return raws, nil
}

// crawlBrowserObserved 执行保存的页面动作，并解析页面本次真实收到的列表响应。
// 不会重新发起一条脱离页面生命周期的 HTTP 请求。
func (c *SiteCrawler) crawlBrowserObserved(ctx context.Context, taskID string, rc *site.Recipe, startURL, keyword string) ([]source.RawJob, error) {
	if c.browser == nil || !c.browser.Health(ctx) {
		return nil, browser.ErrWorkerUnavailable
	}
	entry := strings.TrimSpace(rc.CampusURL)
	if entry == "" {
		entry = strings.TrimSpace(startURL)
	}
	if entry == "" {
		return nil, fmt.Errorf("crawl: 站点 %s 未配置采集入口 URL", rc.SiteKey)
	}

	siteKey := strings.TrimSpace(rc.SiteKey)
	if siteKey == "" {
		siteKey = "generic"
	}
	sess, err := c.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID:  taskID,
		SiteKey: siteKey,
		URL:     entry,
	})
	if err != nil {
		return nil, fmt.Errorf("browser_observed: 打开会话失败: %w", err)
	}
	// 登录态过期是环境故障，不是 Recipe 配置错误：直接透传给上层，
	// 不应计入连续失败、不应把一条好配置废掉。
	if sess.NeedsLogin {
		return nil, browser.ErrLoginExpired
	}
	defer func() {
		_ = c.browser.CloseSession(ctx, taskID)
	}()

	cursor, err := c.browser.ObserveStart(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("browser_observed: 启动网络观测失败: %w", err)
	}
	if err := c.runBrowserPlan(ctx, taskID, rc.BrowserPlan, keyword); err != nil {
		return nil, err
	}
	time.Sleep(exploreWaitAfterAction * time.Second)
	records, err := c.browser.ObserveDiffAll(ctx, taskID, cursor, 80)
	if err != nil {
		return nil, fmt.Errorf("browser_observed: 读取页面请求失败: %w", err)
	}
	for _, record := range records {
		if record.Status < 200 || record.Status >= 300 || !sameAPIPath(rc.ListAPI, record.URL) {
			continue
		}
		full, inspectErr := c.browser.InspectRequest(ctx, taskID, record.Seq)
		if inspectErr != nil {
			continue
		}
		body := firstNonEmpty(full.FullSample, full.Sample)
		jobs, parseErr := executor.ParseAPIJobs(rc, full.URL, []byte(body))
		if parseErr != nil || len(jobs) == 0 {
			continue
		}
		if rc.MaxJobsPerSearch > 0 && len(jobs) > rc.MaxJobsPerSearch {
			jobs = jobs[:rc.MaxJobsPerSearch]
		}
		slog.Info("browser_observed 使用页面真实响应解析岗位", "site_key", rc.SiteKey, "jobs", len(jobs))
		return jobs, nil
	}
	return nil, fmt.Errorf("browser_observed: 页面动作后未观测到可解析的岗位列表响应")
}

func sameAPIPath(a, b string) bool {
	au, errA := url.Parse(strings.TrimSpace(a))
	bu, errB := url.Parse(strings.TrimSpace(b))
	if errA != nil || errB != nil || au.Host == "" || bu.Host == "" {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return strings.EqualFold(au.Host, bu.Host) && au.Path == bu.Path
}

func (c *SiteCrawler) runBrowserPlan(ctx context.Context, taskID string, plan model.JSONMap, keyword string) error {
	return runBrowserPlan(ctx, c.browser, taskID, plan, keyword, "browser_observed")
}

// runBrowserPlan 只执行可跨会话复用的语义动作。DOM ref 是单个页面实例的临时
// 标识，保存它会让下次任务天然失效，因此这里刻意不支持。
func runBrowserPlan(ctx context.Context, bc *browser.Client, taskID string, plan model.JSONMap, keyword, errPrefix string) error {
	actions, _ := plan["actions"].([]any)
	ranSearch := false
	for _, raw := range actions {
		action, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(fmt.Sprint(action["type"]))
		var nav browser.NavAction
		switch kind {
		case "navigate":
			url := strings.TrimSpace(fmt.Sprint(action["url"]))
			if url == "" {
				continue
			}
			nav = browser.NavAction{Type: browser.NavNavigate, URL: url}
		case "click":
			text := strings.TrimSpace(fmt.Sprint(action["text"]))
			if text == "" {
				continue
			}
			nav = browser.NavAction{Type: browser.NavClick, Text: text}
		case "search":
			if strings.TrimSpace(keyword) == "" {
				continue
			}
			nav = browser.NavAction{Type: browser.NavSearch, Keyword: keyword}
			ranSearch = true
		default:
			continue
		}
		resp, err := bc.NavAct(ctx, taskID, nav)
		if err != nil || !resp.OK {
			if err != nil {
				return fmt.Errorf("%s: 动作 %s 失败: %w", errPrefix, kind, err)
			}
			return fmt.Errorf("%s: 动作 %s 未完成: %s", errPrefix, kind, resp.Message)
		}
	}
	if !ranSearch && strings.TrimSpace(keyword) != "" {
		resp, err := bc.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavSearch, Keyword: keyword})
		if err != nil || !resp.OK {
			if err != nil {
				return fmt.Errorf("%s: 搜索失败: %w", errPrefix, err)
			}
			return fmt.Errorf("%s: 搜索未完成: %s", errPrefix, resp.Message)
		}
	}
	return nil
}

// cardToRawJob 把结构化卡片（含可选的 JD 正文）转换为待入库的原始岗位。
// 公司名与部门（腾讯校招按部门展开，每条 JobCard 带 Department）以权威 Meta 写入，
// pipeline 的 applySourceMeta 会优先采用并覆盖模型从长文本中的解析结果。
func cardToRawJob(card browser.JobCard, content, company, sourceName string) source.RawJob {
	meta := map[string]string{"Company": company}
	if d := strings.TrimSpace(card.Department); d != "" {
		meta["Department"] = d
	}
	return source.RawJob{
		SourceType:  model.SourceBrowser,
		SourceName:  sourceName,
		URL:         card.URL,
		Title:       card.Title,
		Content:     content,
		CompanyHint: company,
		Meta:        meta,
	}
}

// hostOf 仅用于可视化详情抓取时限制同站导航，和已移除的旧 AI 导航器无关。
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
