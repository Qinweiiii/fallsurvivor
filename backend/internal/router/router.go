// Package router 负责路由与依赖装配。
package router

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/agent"
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/handler"
	"github.com/eddiel/fallsurvivor/backend/internal/middleware"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	appsvc "github.com/eddiel/fallsurvivor/backend/internal/service/application"
	jobsvc "github.com/eddiel/fallsurvivor/backend/internal/service/job"
	profilesvc "github.com/eddiel/fallsurvivor/backend/internal/service/profile"
	searchsvc "github.com/eddiel/fallsurvivor/backend/internal/service/search"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
	"github.com/eddiel/fallsurvivor/backend/pkg/safefetch"
)

// Deps 是构建路由所需的依赖。
type Deps struct {
	Cfg      *config.Config
	Store    *repository.Store
	Enqueuer *asynq.Client
}

// BuildServices 组装全部 service，供 API 服务与 Worker 共用。
type Services struct {
	Job         *jobsvc.Service
	Search      *searchsvc.Service
	Application *appsvc.Service
	Profile     *profilesvc.Service
	Browser     *browser.Service
	Pipeline    *searchsvc.Pipeline
	LLM         *ai.Client
	Crawler     *searchsvc.SiteCrawler
	// SiteRegistry 持有全部站点 Recipe，供 Fast Path 命中判定。
	SiteRegistry *site.Registry
	// Discovery 编排 Fast Path（命中 Recipe）/ Discovery Path（回退通用搜索）。
	Discovery *searchsvc.DiscoveryService
	// Enrichment 为 JD 不完整的岗位（如 BOSS 摘要）补全正文并重算评分。
	Enrichment *searchsvc.EnrichmentService
}

// BuildServices 构建服务集合。
func BuildServices(cfg *config.Config, store *repository.Store, enqueuer *asynq.Client) (*Services, error) {
	llm := ai.NewClient(cfg.LLM)
	fetcher := safefetch.New(cfg.AllowOutboundFetch)

	// 来源注册：Tavily 作为底层搜索能力，Official / BOSS 复用它做定向发现。
	tavily := source.NewTavilySource(cfg.Tavily)
	registry := source.NewRegistry(
		source.NewOfficialSource(tavily, source.DefaultCompanyAdapters, 6),
		source.NewBossSource(tavily),
	)

	pipeline := searchsvc.NewPipeline(store, registry, llm, fetcher)

	profileSvc, err := profilesvc.NewService(store, llm, cfg.StorageDir, cfg.MaxUploadMB)
	if err != nil {
		return nil, err
	}

	applicationSvc := appsvc.NewService(store)
	browserClient := browser.NewClient(cfg.BrowserWorkerURL, cfg.BrowserWorkerToken)
	browserSvc := browser.NewService(store, browserClient, llm, profileSvc, applicationSvc)
	// 浏览器导航爬虫：在已登录招聘站点上做多步 AI 导航发现岗位入口。
	siteCrawler := searchsvc.NewSiteCrawler(browserClient, llm)

	// ---- Site Recipe Registry ----
	// 把「已知站点怎么采」从硬编码分支变成数据：
	// 启动时写入内置预置（腾讯 / 字节），并装载到内存供 Fast Path 命中判定。
	siteReg := site.NewRegistry(store.Site.Inner())
	if err := store.Site.SeedPresets(context.Background()); err != nil {
		// 预置写入失败不阻断启动：退化为空 Registry，全部走通用发现路径。
		slogWarn("站点 Recipe 预置写入失败（将全部走通用发现路径）", err)
	}
	if err := store.Site.Inner().SeedPlaybook(context.Background()); err != nil {
		slogWarn("探索 Playbook 预置写入失败（探索仍可运行，但不会获得通用经验提示）", err)
	}
	if err := siteReg.Load(context.Background()); err != nil {
		slogWarn("站点 Recipe 装载失败（将全部走通用发现路径）", err)
	}
	discovery := searchsvc.NewDiscoveryService(siteReg, siteCrawler, store.Site.Inner(), fetcher)

	// ---- Exploration Agent ----
	// 只在 Fast Path 未命中时启用：自动探索未知站点的采集方式，
	// 产出候选并可保存为新 Recipe，使下次直接走 Fast Path。
	discovery.AttachExplorer(searchsvc.NewExplorer(browserClient, llm, store.Site.Inner()))

	// ---- Option B：Python 探索 Agent 微服务（可选、可灰度）----
	// 设置 AGENT_EXPLORER_URL 即装配客户端；USE_PYTHON_AGENT=true 才真正切到 Python，
	// 否则（默认）完全走 Go 内置 Explorer，行为与改动前一致。双向校验复用 BROWSER_WORKER_TOKEN。
	if agentURL := strings.TrimSpace(os.Getenv("AGENT_EXPLORER_URL")); agentURL != "" {
		usePython := strings.EqualFold(os.Getenv("USE_PYTHON_AGENT"), "true")
		discovery.AttachAgentExplorer(agent.New(agentURL, cfg.BrowserWorkerToken), usePython)
	}

	// ---- JD 补全服务 ----
	// BOSS 等站点受反爬限制只能拿到摘要，这里复用用户已登录的浏览器会话
	// 打开岗位原网页读取完整 JD，补全后重新解析并重新评分。
	enrichment := searchsvc.NewEnrichmentService(browserClient, llm, store, pipeline)

	return &Services{
		Job:          jobsvc.NewService(store),
		Search:       searchsvc.NewService(store, enqueuer, pipeline),
		Application:  applicationSvc,
		Profile:      profileSvc,
		Browser:      browserSvc,
		Pipeline:     pipeline,
		LLM:          llm,
		Crawler:      siteCrawler,
		SiteRegistry: siteReg,
		Discovery:    discovery,
		Enrichment:   enrichment,
	}, nil
}

// slogWarn 统一启动期告警日志。
func slogWarn(msg string, err error) {
	if err == nil {
		return
	}
	slog.Warn(msg, "error", err.Error())
}

// New 构建 Gin 引擎。
func New(cfg *config.Config, store *repository.Store, svcs *Services) *gin.Engine {
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// 只信任本机代理，避免 X-Forwarded-For 伪造影响限流。
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery())
	r.Use(middleware.Logger())
	r.Use(middleware.SecurityHeaders())

	// CORS：仅允许显式配置的来源，且不反射任意 Origin。
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSAllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "X-Request-ID"},
		ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	// 请求体上限：普通接口 1 MiB。
	r.Use(middleware.BodyLimit(1 << 20))

	jobH := handler.NewJobHandler(svcs.Job, store)
	searchH := handler.NewSearchHandler(svcs.Search, store, svcs.Crawler)
	// 注入站点发现服务，让 /api/jobs/crawl 支持 Recipe Fast Path。
	searchH.AttachDiscovery(svcs.Discovery)
	siteH := handler.NewSiteRecipeHandler(store.Site.Inner())
	// 注入发现服务，让配置页支持「立即用真实请求验证这条配置」。
	siteH.AttachDiscovery(svcs.Discovery)
	enrichH := handler.NewEnrichmentHandler(svcs.Enrichment, store)
	exploreH := handler.NewExploreHandler(svcs.Discovery)
	appH := handler.NewApplicationHandler(svcs.Application, store)
	profileH := handler.NewProfileHandler(svcs.Profile, store)
	browserH := handler.NewBrowserHandler(svcs.Browser, store)
	llmLogH := handler.NewLLMLogHandler()

	r.GET("/api/health", func(c *gin.Context) {
		response.OK(c, gin.H{
			"status":       "ok",
			"llm_enabled":  svcs.LLM.Enabled(),
			"search_ready": cfg.Tavily.Enabled(),
		})
	})

	api := r.Group("/api")
	{
		// ---- Dashboard ----
		api.GET("/dashboard", jobH.Dashboard)

		// ---- 求职画像 ----
		api.GET("/profile", profileH.GetProfile)
		api.PUT("/profile", profileH.UpdateProfile)

		// ---- 可复用申请信息 ----
		api.GET("/application-profile", profileH.GetApplicationProfile)
		api.PUT("/application-profile", profileH.UpdateApplicationProfile)

		// ---- 简历（上传接口单独放宽体积限制） ----
		api.GET("/resumes", profileH.ListResumes)
		api.POST("/resumes",
			middleware.BodyLimit((cfg.MaxUploadMB+1)<<20),
			middleware.RateLimit(10, time.Minute),
			profileH.UploadResume)
		api.GET("/resumes/:id", profileH.GetResume)
		api.PUT("/resumes/:id/text", middleware.BodyLimit(1<<20), profileH.SetResumeText)
		api.PUT("/resumes/:id/current", profileH.SetCurrentResume)
		api.DELETE("/resumes/:id", profileH.DeleteResume)

		// ---- 岗位 ----
		api.GET("/jobs", jobH.List)
		api.GET("/jobs/filter-options", jobH.FilterOptions)
		// 搜索接口限流：保护上游 API 配额。
		api.POST("/jobs/search", middleware.RateLimit(6, time.Minute), searchH.Trigger)
		api.GET("/jobs/:id", jobH.Detail)
		// 用已登录浏览器抓取真实 JD（需 Playwright Worker）。
		api.POST("/jobs/:id/scrape", browserH.ScrapeJob)
		api.POST("/jobs/:id/scrape/resume", browserH.ScrapeJobResume)
		// 浏览器导航爬虫：在已登录招聘站点多步发现岗位详情页入口（需 Playwright Worker + LLM）。
		api.POST("/jobs/crawl", middleware.RateLimit(3, time.Minute), searchH.Crawl)

		// ---- 搜索任务 ----
		api.GET("/search-tasks/latest", searchH.Latest)
		api.GET("/search-tasks/:id", searchH.Get)

		// ---- 岗位车 ----
		api.GET("/job-cart", jobH.ListCart)
		api.POST("/job-cart", jobH.AddToCart)
		api.POST("/job-cart/batch", jobH.AddToCartBatch)
		api.DELETE("/job-cart/batch", jobH.RemoveFromCartBatch)
		api.DELETE("/job-cart/:job_id", jobH.RemoveFromCart)

		// ---- 投递任务 ----
		api.GET("/applications", appH.List)
		api.GET("/applications/status-options", appH.StatusOptions)
		api.POST("/applications", appH.Create)
		api.POST("/applications/batch", appH.CreateBatch)
		api.GET("/applications/:id", appH.Detail)
		api.PUT("/applications/:id/status", appH.UpdateStatus)
		// 用户已在招聘网站自行提交后，标记本系统状态。
		// 系统刻意不提供代替用户提交的接口。
		api.POST("/applications/:id/mark-submitted", appH.MarkSubmitted)

		// ---- 浏览器辅助填写 ----
		api.POST("/applications/:id/browser/start",
			middleware.RateLimit(10, time.Minute), browserH.Start)
		api.GET("/browser-tasks/:id", browserH.Get)
		api.POST("/browser-tasks/:id/resume", browserH.Resume)
		api.POST("/browser-tasks/:id/pause", browserH.Pause)

		// ---- LLM 调用日志（调试观测用） ----
		api.GET("/llm-logs", llmLogH.List)

		// ---- JD 补全（为 BOSS 等摘要型岗位补齐完整 JD） ----
		api.POST("/jobs/enrich-jd", enrichH.Enrich)

		// ---- 站点探索（Exploration Agent） ----
		api.POST("/explore", exploreH.Explore)

		// ---- 站点 Recipe 配置（数据驱动采集） ----
		api.GET("/site-recipes", siteH.List)
		api.POST("/site-recipes", siteH.Create)
		api.PUT("/site-recipes/:id", siteH.Update)
		api.DELETE("/site-recipes/:id", siteH.Delete)
		api.GET("/site-recipes/:id/runs", siteH.Runs)
		// 用真实请求验证配置能否采到岗位，并写回健康度。
		api.POST("/site-recipes/:id/verify", siteH.Verify)
	}

	r.NoRoute(func(c *gin.Context) {
		response.NotFound(c, "接口不存在")
	})

	return r
}
