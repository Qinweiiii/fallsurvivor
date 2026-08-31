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
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

// SiteCrawler 负责在已登录的招聘站点上做多步导航，
// 一步步发现「具体岗位详情页」的入口（标题 / URL / 部门）。
//
// 导航策略：
//   - 优先使用站点级半固定脚本（如腾讯），省 LLM token；
//   - 无固定策略时回退到 AI 逐步决策导航。
//
// 安全与资源约束：
//   - 复用 Worker 的持久登录态（按 siteKey），不处理密码；
//   - 每步导航动作由 Worker 端做安全校验（禁提交按钮、navigate 仅公网地址）；
//   - 限制最大步数与最大发现岗位数，防止死循环与 token 失控；
//   - 若页面需要登录，立即返回错误交由用户在浏览器中先登录。
type SiteCrawler struct {
	browser *browser.Client
	llm     *ai.Client
}

// NewSiteCrawler 构造爬虫。browser 或 llm 为 nil 时 Crawl 会直接返回不可用错误。
func NewSiteCrawler(b *browser.Client, llm *ai.Client) *SiteCrawler {
	return &SiteCrawler{browser: b, llm: llm}
}

const (
	crawlMaxSteps = 18
	crawlMaxJobs  = 24
)

// Crawl 从 startURL 开始多步导航，返回发现的岗位入口。
// taskID 用于复用 Worker 会话（同一 taskID 复用已打开的浏览器窗口与登录态）。
// strategy 可选："auto"（按站点路由，默认）、"script"（强制半固定）、"ai"（强制 AI）。
//
// 策略路由：auto 模式下有半固定脚本的站点（如 tencent）走固定步骤，其余走 AI 导航。
func (c *SiteCrawler) Crawl(ctx context.Context, taskID, siteKey, startURL, strategy string) ([]ai.NavJob, error) {
	// 强制 AI 模式（用于调试 / 沉淀真实路径）。
	if strategy == "ai" {
		slog.Info("强制 AI 导航模式", "task_id", taskID, "site", siteKey)
		return c.crawlAI(ctx, taskID, siteKey, startURL)
	}

	// 强制脚本模式 / 站点级策略路由：腾讯走半固定脚本（校招浏览器抽卡）。
	if strategy == "script" || siteKey == "tencent" {
		slog.Info("使用腾讯半固定导航策略", "task_id", taskID)
		_, raws, err := c.CrawlTencent(ctx, taskID, startURL, "")
		if err != nil {
			return nil, err
		}
		// Crawl 仅返回导航入口，落库在 handler 的专用分支完成。
		navJobs := make([]ai.NavJob, 0, len(raws))
		for _, r := range raws {
			navJobs = append(navJobs, ai.NavJob{Title: r.Title, URL: r.URL})
		}
		return navJobs, nil
	}

	// 通用：AI 逐步导航。
	return c.crawlAI(ctx, taskID, siteKey, startURL)
}

// defaultMaxDetailFetches 是站点 Recipe 未配置 max_detail_fetches 时的兜底值，
// 限制每个列表页额外抓取详情页 JD 的岗位数，避免浏览器会话耗时过长。
const defaultMaxDetailFetches = 8

// CrawlByteDance 使用字节跳动招聘的半固定脚本：
//  1. 打开列表页（URL 已带关键词）；
//  2. 滚动若干次以触发无限分页加载；
//  3. 直接从 DOM 抽取结构化岗位卡片（URL + 标题），绕开 LLM 对联网搜索脏文本的解析；
//  4. 对前 N 个卡片打开详情页抓取 JD 正文，连同权威 Meta（公司=字节跳动）一起返回，
//     供 pipeline 标准化入库，从而消除 Tavily 联网搜索带来的噪声。
//
// 返回 (展示用岗位入口, 待入库的结构化原始岗位, error)。
// crawlExtractJobs 通用浏览器抽卡落库流程：打开站点 → 滚动加载 → 抽取卡片 → 逐个打开详情页抓取 JD。
// siteKey 决定 Worker 侧使用哪个抽取适配器；company/companyHint/sourceName 用于落库标识。
// keyword 为可选搜索关键词（目前仅腾讯校招使用），非空时由 Worker 在列表页搜索框提交后抽取过滤结果。
// 字节校招（jobs.bytedance.com/campus）与腾讯校招（join.qq.com）共用本流程，仅入口与适配器不同。
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

// CrawlByteDance 保留为字节站点的便捷入口，默认走详情页抓取。
// 新的调用方应优先使用 CrawlWithRecipe（由站点 Recipe 驱动）。
func (c *SiteCrawler) CrawlByteDance(ctx context.Context, taskID, startURL string) ([]ai.NavJob, []source.RawJob, error) {
	return c.crawlExtractJobs(ctx, taskID, "bytedance", startURL, "字节跳动", "字节跳动招聘", "", defaultMaxDetailFetches)
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
	// 站点标识：优先用 adapter_key（Worker 侧靠它选 adapter），回退到 site_key。
	siteKey := strings.TrimSpace(rc.AdapterKey)
	if siteKey == "" {
		siteKey = rc.SiteKey
	}
	entry := strings.TrimSpace(startURL)
	if entry == "" {
		entry = strings.TrimSpace(rc.CampusURL)
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

// crawlAI 使用 LLM 逐步决策导航，适用于没有半固定脚本的通用站点。
func (c *SiteCrawler) crawlAI(ctx context.Context, taskID, siteKey, startURL string) ([]ai.NavJob, error) {
	if c.browser == nil || !c.browser.Health(ctx) {
		return nil, browser.ErrWorkerUnavailable
	}
	if c.llm == nil || !c.llm.Enabled() {
		return nil, fmt.Errorf("browser: LLM 未启用，无法驱动导航")
	}

	// 打开会话（复用 siteKey 对应的持久登录态）。
	if _, err := c.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID:  taskID,
		SiteKey: siteKey,
		URL:     startURL,
	}); err != nil {
		return nil, fmt.Errorf("browser: 打开会话失败: %w", err)
	}
	defer func() {
		_ = c.browser.CloseSession(ctx, taskID)
	}()

	startHost := hostOf(startURL)
	jobs := make([]ai.NavJob, 0, crawlMaxJobs)
	seen := make(map[string]bool)
	history := make([]string, 0, crawlMaxSteps)
	first := true

	for step := 0; step < crawlMaxSteps; step++ {
		if ctx.Err() != nil {
			return jobs, nil
		}

		scrape, err := c.browser.ScrapePage(ctx, taskID)
		if err != nil {
			return jobs, fmt.Errorf("browser: 抓取页面失败: %w", err)
		}
		if scrape.NeedsLogin {
			return jobs, fmt.Errorf("browser: 页面需要登录后才能继续，请在浏览器中完成登录后重试")
		}

		if !first {
			if seen[scrape.CurrentURL] {
				slog.Info("导航回到已访问页，停止", "url", scrape.CurrentURL)
				break
			}
		}
		seen[scrape.CurrentURL] = true
		first = false

		decision, err := c.llm.NavigateStep(ctx, ai.NavStepInput{
			Site:     siteKey,
			URL:      scrape.CurrentURL,
			Title:    scrape.Title,
			PageText: scrape.PageText,
			History:  history,
		})
		if err != nil {
			slog.Warn("导航决策失败，停止", "error", err.Error())
			break
		}

		for _, j := range decision.Jobs {
			if j.URL == "" || seen[j.URL] {
				continue
			}
			if startHost != "" && hostOf(j.URL) != startHost {
				continue
			}
			jobs = append(jobs, j)
			seen[j.URL] = true
			if len(jobs) >= crawlMaxJobs {
				return jobs, nil
			}
		}

		switch decision.Action.Type {
		case ai.NavStop:
			return jobs, nil
		case ai.NavClick, ai.NavScroll, ai.NavNavigate, ai.NavWait:
			navAct := browser.NavAction{
				Type:      browser.NavActionType(decision.Action.Type),
				Text:      decision.Action.Text,
				Direction: decision.Action.Direction,
				Amount:    decision.Action.Amount,
				URL:       decision.Action.URL,
				Seconds:   decision.Action.Seconds,
			}
			act, err := c.browser.NavAct(ctx, taskID, navAct)
			if err != nil {
				slog.Warn("导航动作执行失败，停止", "error", err.Error())
				return jobs, err
			}
			if !act.OK {
				slog.Warn("导航动作未成功", "msg", act.Message)
			}
		default:
			return jobs, nil
		}

		history = append(history, fmt.Sprintf("第%d步 [%s] %s",
			step+1, decision.Action.Type, actionDesc(decision.Action)))
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}

	return jobs, nil
}

// actionDesc 把动作转成一段简短可读描述，用于历史记录。
func actionDesc(a ai.NavAction) string {
	switch a.Type {
	case ai.NavClick:
		return "click " + a.Text
	case ai.NavScroll:
		return "scroll " + a.Direction
	case ai.NavNavigate:
		return "navigate " + a.URL
	case ai.NavWait:
		return fmt.Sprintf("wait %ds", a.Seconds)
	default:
		return string(a.Type)
	}
}

// hostOf 提取 URL 的 host（含端口），非法输入返回空串。
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}
