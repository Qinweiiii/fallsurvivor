// Package executor 负责执行站点 Recipe，产出统一的 []source.RawJob。
//
// 设计要点：
//  1. 依赖倒置：本包不直接依赖 service/search，而是定义 Crawler 接口，
//     由 search.SiteCrawler 实现。这样避免了 executor ↔ search 的循环依赖，
//     也让 Executor 可独立测试（注入 fake crawler）。
//  2. 策略分派：按 Recipe.StrategyType 路由到具体实现。
//     第一版只实现 browser 策略（复用 Worker adapter）；
//     api / url_template 为预留扩展位，命中时返回明确错误而非静默失败。
//  3. 执行留痕：每次执行都会记录一条 recipe_runs，
//     既用于观测，也是后续 Reflection（Recipe 失效自愈）的输入。
package executor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

// Crawler 是底层采集能力的抽象，由 service/search.SiteCrawler 实现。
//
// 定义在本包而非依赖具体实现，是为了切断 executor → search 的依赖方向：
// search 实现本接口，executor 只面向接口编程。
type Crawler interface {
	// CrawlWithRecipe 按站点 Recipe 执行一次采集，返回待入库的原始岗位。
	CrawlWithRecipe(ctx context.Context, taskID string, rc *site.Recipe, startURL, keyword string) ([]source.RawJob, error)
}

// Params 一次 Recipe 执行的入参。
type Params struct {
	// TaskID 用于复用 Worker 的浏览器会话与登录态。
	TaskID string
	// Recipe 目标站点配置。
	Recipe *site.Recipe
	// Keyword 搜索关键词。
	Keyword string
	// StartURL 搜索起点，为空时回退到 Recipe.CampusURL。
	StartURL string
}

// Result 一次 Recipe 执行的结果。
type Result struct {
	// Jobs 待入库的原始岗位。
	Jobs []source.RawJob
	// SiteKey 执行的站点标识，便于日志与观测。
	SiteKey string
	// Strategy 实际采用的策略。
	Strategy site.Strategy
	// DurationMS 执行耗时。
	DurationMS int64
}

// Executor 按 Recipe 执行采集。
type Executor struct {
	crawler Crawler
	runs    *site.Repository
}

// New 构造 Executor。runs 可为 nil（不记录执行历史）。
func New(c Crawler, runs *site.Repository) *Executor {
	return &Executor{crawler: c, runs: runs}
}

// Execute 执行一条 Recipe。
//
// 无论成功失败都会记录 recipe_runs（若配置了 Repository）。
// 返回的错误已带上站点上下文，便于调用方直接上报。
func (e *Executor) Execute(ctx context.Context, p Params) (*Result, error) {
	rc := p.Recipe
	if rc == nil {
		return nil, fmt.Errorf("executor: Recipe 为空")
	}

	start := time.Now()
	res := &Result{SiteKey: rc.SiteKey, Strategy: rc.StrategyType}

	jobs, err := e.executeByStrategy(ctx, p)
	res.Jobs = jobs
	res.DurationMS = time.Since(start).Milliseconds()

	// 执行留痕：失败也记录，为后续「Recipe 失效自愈」提供依据。
	e.recordRun(ctx, rc, p.Keyword, jobs, err, res.DurationMS)

	if err != nil {
		return nil, fmt.Errorf("executor: 站点 %s 执行失败: %w", rc.SiteKey, err)
	}

	slog.Info("Recipe 执行完成",
		"site_key", rc.SiteKey,
		"strategy", rc.StrategyType,
		"jobs", len(jobs),
		"duration_ms", res.DurationMS)
	return res, nil
}

// executeByStrategy 按策略类型分派。
func (e *Executor) executeByStrategy(ctx context.Context, p Params) ([]source.RawJob, error) {
	switch p.Recipe.StrategyType {
	case site.StrategyBrowser:
		return e.executeBrowser(ctx, p)
	case site.StrategyAPI:
		// 预留：直接调用站点公开 HTTP 接口。
		// 当前版本未实现——腾讯社招 API 走的是 source.TencentSource，
		// 尚未抽象成 Recipe。命中时明确报错，避免静默返回空结果。
		return nil, fmt.Errorf("策略 %q 尚未实现（当前仅支持 browser）", site.StrategyAPI)
	case site.StrategyURLTemplate:
		// 预留：按岗位 ID 模板拼详情页 URL。
		return nil, fmt.Errorf("策略 %q 尚未实现（当前仅支持 browser）", site.StrategyURLTemplate)
	default:
		return nil, fmt.Errorf("未知策略类型 %q", p.Recipe.StrategyType)
	}
}

// recordRun 把执行结果写入 recipe_runs。失败不影响主流程。
func (e *Executor) recordRun(ctx context.Context, rc *site.Recipe, keyword string, jobs []source.RawJob, runErr error, durationMS int64) {
	if e.runs == nil {
		return
	}
	run := &site.Run{
		SiteKey:      rc.SiteKey,
		Keyword:      keyword,
		JobsFound:    len(jobs),
		JobsIngested: 0, // 入库数由调用方（Discovery）回填，执行阶段尚不知。
		DurationMS:   durationMS,
	}
	if rc.ID != "" {
		id := rc.ID
		run.RecipeID = &id
	}
	switch {
	case runErr != nil:
		run.Status = site.RunFailed
		run.ErrorMessage = runErr.Error()
	case len(jobs) == 0:
		run.Status = site.RunEmpty
	default:
		run.Status = site.RunSuccess
	}
	if err := e.runs.RecordRun(ctx, run); err != nil {
		slog.Warn("记录 Recipe 执行历史失败", "site_key", rc.SiteKey, "error", err.Error())
	}
}
