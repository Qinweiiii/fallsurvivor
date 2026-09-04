package executor

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

// executeBrowser 执行浏览器抽取策略。
//
// 这是第一版唯一实现的策略，复用 Worker 侧的站点 adapter：
//   - 腾讯：adapter 通过 searchPosition + jobDetails 接口拿到完整 JD 与部门；
//   - 字节：adapter 抽卡后由 Go 侧打开详情页抓 JD。
//
// 两条路径的差异由 Recipe.MaxDetailFetches 控制（0 = 跳过详情页抓取），
// 而不是按 siteKey 硬编码分支。
func (e *Executor) executeBrowser(ctx context.Context, p Params) ([]source.RawJob, error) {
	if e.crawler == nil {
		return nil, fmt.Errorf("浏览器采集器未初始化")
	}
	rc := p.Recipe

	// 传统 browser 策略依赖 Worker 侧站点 adapter；browser_observed 只需要浏览器上下文。
	if rc.StrategyType == site.StrategyBrowser && rc.AdapterKey == "" {
		return nil, fmt.Errorf("站点 %s 的 adapter_key 为空，无法执行浏览器采集", rc.SiteKey)
	}

	slog.Info("执行浏览器 Recipe",
		"site_key", rc.SiteKey,
		"adapter", rc.AdapterKey,
		"keyword", p.Keyword,
		"max_jobs", rc.MaxJobsPerSearch,
		"max_detail", rc.MaxDetailFetches)

	jobs, err := e.crawler.CrawlWithRecipe(ctx, p.TaskID, rc, p.StartURL, p.Keyword)
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// 编译期断言：确保本包用到的 site 类型存在，避免误删。
var (
	_ = site.StrategyBrowser
	_ = site.StrategyBrowserObserved
	_ = site.SourcePreset
	_ = site.RunSuccess
	_ = (source.RawJob{}).SourceType
)
