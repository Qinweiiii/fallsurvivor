package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
	"github.com/eddiel/fallsurvivor/backend/internal/handler/dto"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	searchsvc "github.com/eddiel/fallsurvivor/backend/internal/service/search"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// SearchHandler 处理岗位搜索相关接口。
type SearchHandler struct {
	svc     *searchsvc.Service
	user    *userResolver
	crawler *searchsvc.SiteCrawler
	// discovery 提供站点 Recipe 的 Fast Path，可为 nil（此时走原有硬编码分支）。
	discovery *searchsvc.DiscoveryService
}

// NewSearchHandler 创建 handler。
func NewSearchHandler(svc *searchsvc.Service, store *repository.Store, crawler *searchsvc.SiteCrawler) *SearchHandler {
	return &SearchHandler{svc: svc, user: newUserResolver(store), crawler: crawler}
}

// AttachDiscovery 注入站点发现服务（Fast Path）。
// 单独提供是为了不破坏既有构造签名，装配处按需调用。
func (h *SearchHandler) AttachDiscovery(d *searchsvc.DiscoveryService) {
	h.discovery = d
}

// Trigger 处理 POST /api/jobs/search。
//
// 该接口立即返回 task_id，搜索在后台异步执行，不阻塞 HTTP 请求。
func (h *SearchHandler) Trigger(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.SearchRequest
	// 允许空请求体。
	_ = c.ShouldBindJSON(&req)

	task, err := h.svc.Trigger(c.Request.Context(), userID, req.Force)
	if errors.Is(err, searchsvc.ErrTaskRunning) {
		// 把当前进行中任务的信息一并返回，便于前端弹窗展示进度并轮询。
		response.FailWithData(c, http.StatusConflict, response.CodeConflict,
			"已有搜索任务正在执行，请等待其完成", gin.H{
				"task_id":     task.ID,
				"status":      task.Status,
				"query_count": task.QueryCount,
				"found_count": task.FoundCount,
				"new_count":   task.NewCount,
			})
		return
	}
	if err != nil {
		handleServiceError(c, err)
		return
	}

	response.Created(c, gin.H{
		"task_id": task.ID,
		"status":  task.Status,
	})
}

// Get 处理 GET /api/search-tasks/:id。
func (h *SearchHandler) Get(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	taskID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	task, err := h.svc.Get(c.Request.Context(), userID, taskID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, task)
}

// Latest 处理 GET /api/search-tasks/latest。
func (h *SearchHandler) Latest(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	task, err := h.svc.GetLatest(c.Request.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		response.OK(c, nil)
		return
	}
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, task)
}

// Crawl 处理 POST /api/jobs/crawl。
//
// 在已登录的招聘站点上做多步导航，发现具体岗位详情页入口。
// 需要 Playwright Worker 已启动且对应站点已手动登录过一次（登录态持久化复用）。
//
// 首次使用某站点时流程：
//  1. 确保 Worker 已启动（make bw）；
//  2. 调用本接口 → Worker 弹出浏览器窗口打开目标站点；
//  3. 在弹出的浏览器窗口中**手动完成登录/验证**；
//  4. 登录成功后再次调用本接口，登录态自动复用。
func (h *SearchHandler) Crawl(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	if h.crawler == nil {
		response.Fail(c, http.StatusNotImplemented, response.CodeNotImplemented, "未启用浏览器导航爬虫")
		return
	}

	var req struct {
		URL     string `json:"url"`
		SiteKey string `json:"site_key"`
		// ai 是历史参数，现作为 explore 的别名，避免同类请求进入旧导航器。
		Strategy string `json:"strategy"` // auto / script / explore / ai(alias)
		Keyword  string `json:"keyword"`  // 可选搜索关键词（如腾讯校招在列表页搜索框提交）
		Company  string `json:"company"`  // 可选公司名，用于按公司反查站点 Recipe
		// SaveAsRecipe 为 true 时，未命中 Recipe 会触发 Exploration Path，
		// 成功后把探索出的 API 路径保存为站点 Recipe，供下次 Fast Path 复用。
		SaveAsRecipe bool `json:"save_as_recipe"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if req.URL == "" {
		response.InvalidParam(c, "url 不能为空")
		return
	}
	siteKey := req.SiteKey
	if siteKey == "" {
		siteKey = detectSiteKey(req.URL)
	}
	forceExplore := shouldExploreCrawl(req.Strategy, req.SaveAsRecipe)
	slog.Info("jobs/crawl 路由判定",
		"site_key", siteKey,
		"strategy", req.Strategy,
		"save_as_recipe", req.SaveAsRecipe,
		"force_explore", forceExplore,
		"discovery_attached", h.discovery != nil)

	taskID := uuid.NewString()

	// ---- Fast Path：先查站点 Recipe ----
	// 命中已配置站点（如腾讯、字节校招）时，按 Recipe 采集并直接标准化入库，
	// 绕开联网搜索脏文本的 LLM 解析。未命中则回退到下方通用 AI 导航。
	// 新增一家公司只需在配置页加一条 Recipe，无需改动本文件。
	if h.discovery != nil && !forceExplore {
		fast, fastErr := h.discovery.FastPath(c.Request.Context(), searchsvc.DiscoveryParams{
			TaskID:  taskID,
			SiteKey: siteKey,
			URL:     req.URL,
			Company: req.Company,
			Keyword: req.Keyword,
		})
		if fast != nil && fast.Hit {
			if fastErr != nil {
				// 本次真实执行已经证明 Recipe 不可用，立即用同一个用户请求
				// 重新探索并覆盖旧配置；不先做额外接口验证或盲目重试。
				forceExplore = true
				slog.Warn("Recipe Fast Path 失败，转入重新探索", "site_key", siteKey, "error", fastErr.Error())
			} else {
				// 同步入库并返回真实诊断：后台 goroutine 会把失败吞掉，
				// 返回 ingested=len(raws) 只是「投进队列的条数」，库里是否真落库无从得知。
				opCtx, opCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Minute)
				defer opCancel()
				ingestRes, ingestErr := h.svc.IngestRawJobs(opCtx, userID, fast.Jobs)
				if ingestErr != nil {
					slog.Warn("Recipe 岗位入库失败", "site_key", siteKey, "error", ingestErr.Error())
				}
				var ingested, dup, skipped int
				var reasons map[string]int
				if ingestRes != nil {
					ingested, dup, skipped, reasons = ingestRes.NewCount, ingestRes.DupCount, ingestRes.Skipped, ingestRes.SkippedReasons
				}
				siteName := siteKey
				company := ""
				if fast.Recipe != nil {
					siteName = displayName(fast.Recipe.CompanyName, siteKey)
					company = fast.Recipe.CompanyName
				}
				response.OK(c, gin.H{
					"site_key":        siteKey,
					"site_name":       siteName,
					"company":         company,
					"count":           fast.JobsFound,
					"jobs":            jobsFromRaw(fast.Jobs),
					"strategy":        "站点 Recipe（" + string(fast.Recipe.StrategyType) + "）",
					"ingested":        ingested,
					"duplicate":       dup,
					"skipped":         skipped,
					"skipped_reasons": reasons,
					"field_quality":   fast.FieldQuality,
				})
				return
			}
		}
		// 自愈：命中的 Recipe 已被标记失效（连续失败达阈值），
		// 自动转入探索路径重新摸索，而不是让用户手动去删配置。
		// 这是「Agent 自己维护采集路径」闭环的最后一环。
		if fast != nil && fast.NeedsRexplore {
			forceExplore = true
			slog.Warn("站点 Recipe 已失效，自动转入重新探索",
				"site_key", siteKey, "last_error", fast.RexploreReason)
		}
		// 未命中 Recipe：默认对非半固定脚本站点转入探索，避免回到旧的通用导航。
		if shouldAutoExplore(siteKey, req.Strategy) {
			forceExplore = true
		}
	}

	if h.discovery != nil && forceExplore {
		opCtx, opCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Minute)
		defer opCancel()

		explored, exploreErr := h.discovery.ExploreSite(opCtx, searchsvc.DiscoveryParams{
			TaskID:  taskID,
			SiteKey: siteKey,
			URL:     req.URL,
			Company: req.Company,
			Keyword: req.Keyword,
		}, req.SaveAsRecipe)
		if exploreErr != nil {
			response.UpstreamFailed(c, "站点探索失败："+exploreErr.Error())
			return
		}
		if explored == nil || !explored.Success {
			reason := "未能生成可复用的站点 Recipe"
			if explored != nil && explored.Reason != "" {
				reason = explored.Reason
			}
			data := gin.H{}
			if explored != nil && explored.RunID != "" {
				data["exploration_run_id"] = explored.RunID
			}
			response.FailWithData(c, http.StatusBadGateway, response.CodeUpstreamFailed, "站点探索失败："+reason, data)
			return
		}
		if !explored.Saved || explored.Recipe == nil {
			response.OK(c, gin.H{
				"site_key":       siteKey,
				"site_name":      displayName(req.Company, siteKey),
				"company":        req.Company,
				"count":          0,
				"jobs":           []ai.NavJob{},
				"strategy":       "站点探索（已验证，未保存 Recipe）",
				"saved":          false,
				"trace":          explored.Trace,
				"candidate":      explored.Candidate,
				"verified":       explored.Verified,
				"verified_jobs":  explored.VerifiedJobs,
				"verify_samples": explored.VerifySampleTitles,
				"field_quality":  explored.FieldQuality,
				"refine_rounds":  explored.RefineRounds,
				"duration_ms":    explored.DurationMS,
			})
			return
		}
		// 首次探索已经拿到了页面真实响应，直接入库；不要求用户再发一次
		// crawl 才能保存这次已成功获取的数据。
		ingestRes, ingestErr := h.svc.IngestRawJobs(opCtx, userID, explored.Jobs)
		if ingestErr != nil {
			slog.Warn("探索岗位入库失败", "site_key", siteKey, "error", ingestErr.Error())
		}
		var ingested, dup, skipped int
		var reasons map[string]int
		if ingestRes != nil {
			ingested, dup, skipped, reasons = ingestRes.NewCount, ingestRes.DupCount, ingestRes.Skipped, ingestRes.SkippedReasons
		}

		response.OK(c, gin.H{
			"site_key":        explored.Recipe.SiteKey,
			"site_name":       displayName(explored.Recipe.CompanyName, explored.Recipe.SiteKey),
			"company":         explored.Recipe.CompanyName,
			"count":           len(explored.Jobs),
			"jobs":            jobsFromRaw(explored.Jobs),
			"strategy":        "站点探索（已保存页面动作与本次真实结果）",
			"recipe_id":       explored.Recipe.ID,
			"saved":           explored.Saved,
			"trace":           explored.Trace,
			"candidate":       explored.Candidate,
			"ingested":        ingested,
			"duplicate":       dup,
			"skipped":         skipped,
			"skipped_reasons": reasons,
			"ingest_status":   "completed",
			"next_action":     "下次调用 crawl 且不传 strategy=explore 时会执行保存的页面动作；失败时自动重新探索并覆盖旧 Recipe。",
			// 验证信息：让使用者看到「Agent 自己试跑通了，采到了这些岗位」，
			// 以及为此做了几轮自修正。这是判断探索质量的关键依据。
			"verified":       explored.Verified,
			"verified_jobs":  explored.VerifiedJobs,
			"verify_samples": explored.VerifySampleTitles,
			"field_quality":  explored.FieldQuality,
			"refine_rounds":  explored.RefineRounds,
			"duration_ms":    explored.DurationMS,
		})
		return
	}

	// BOSS 当前仍保留可视化详情抓取路径。其他站点（包括腾讯、字节）统一
	// 通过 Explorer 生成 Recipe，再由通用 Fast Path 执行。
	if siteKey == "boss" {
		// 脱离请求上下文：HTTP 服务端超时 / 客户端断开会取消 c.Request.Context()，
		// 而整个 crawl+入库（含浏览器等待与逐条 LLM 解析）往往耗时数十秒，
		// 一旦被取消，入库首步的 DB 查询就会报 context canceled 且库里一条都没落。
		// 这里用 WithoutCancel 隔离，并单独设一个宽上限避免无限挂起。
		opCtx, opCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Minute)
		defer opCancel()

		var navJobs []ai.NavJob
		var raws []source.RawJob
		var crawlErr error
		navJobs, raws, crawlErr = h.crawler.CrawlVisibleDetails(
			opCtx, taskID, "boss", req.URL, req.Keyword,
			model.SourceBoss, "BOSS直聘", "", 3,
		)
		if crawlErr != nil {
			msg := crawlErr.Error()
			if strings.Contains(msg, "需要登录") || strings.Contains(msg, "请先登录") {
				msg = fmt.Sprintf("%s\n\n操作指引：\n1. 确保终端已运行 make bw（Playwright Worker）\n2. Worker 应已弹出浏览器窗口，请在其中完成 %s 登录\n3. 登录成功后重新点击「开始爬取」", msg, siteDisplayName(siteKey))
			}
			response.UpstreamFailed(c, "导航爬虫执行失败："+msg)
			return
		}
		// 同步入库并返回真实诊断：之前用后台 goroutine 会把失败全部吞掉，
		// 返回 ingested=len(raws) 只是「投进队列的条数」，库里是否真落库无从得知。
		ingestRes, ingestErr := h.svc.IngestRawJobs(opCtx, userID, raws)
		if ingestErr != nil {
			slog.Warn("脚本岗位入库失败", "error", ingestErr.Error())
		}
		var ingested, dup, skipped int
		var reasons map[string]int
		if ingestRes != nil {
			ingested, dup, skipped, reasons = ingestRes.NewCount, ingestRes.DupCount, ingestRes.Skipped, ingestRes.SkippedReasons
		}
		response.OK(c, gin.H{
			"site_key":        siteKey,
			"site_name":       siteDisplayName(siteKey),
			"count":           len(navJobs),
			"jobs":            navJobs,
			"strategy":        strategyName(siteKey),
			"ingested":        ingested,
			"duplicate":       dup,
			"skipped":         skipped,
			"skipped_reasons": reasons,
			"field_quality":   executor.BuildFieldQuality(raws),
		})
		return
	}

	jobs, err := h.crawler.Crawl(c.Request.Context(), taskID, siteKey, req.URL, req.Strategy)
	if err != nil {
		// 需要登录时给出明确的手动操作引导。
		msg := err.Error()
		if strings.Contains(msg, "需要登录") || strings.Contains(msg, "请先登录") {
			siteName := siteDisplayName(siteKey)
			msg = fmt.Sprintf("%s\n\n操作指引：\n1. 确保终端已运行 make bw（Playwright Worker）\n2. Worker 应已弹出浏览器窗口，请在其中完成 %s 登录\n3. 登录成功后重新点击「开始爬取」", msg, siteName)
		}
		response.UpstreamFailed(c, "导航爬虫执行失败："+msg)
		return
	}

	response.OK(c, gin.H{
		"site_key":  siteKey,
		"site_name": siteDisplayName(siteKey),
		"count":     len(jobs),
		"jobs":      jobs,
		"strategy":  strategyName(siteKey),
	})
}

func shouldExploreCrawl(strategy string, saveAsRecipe bool) bool {
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if saveAsRecipe || strategy == "explore" || strategy == "ai" {
		return true
	}
	return false
}

func shouldAutoExplore(siteKey, strategy string) bool {
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if strategy != "" && strategy != "auto" {
		return false
	}
	return siteKey != "boss"
}

// detectSiteKey 按域名推断站点标识，未知站点回退 generic。
func detectSiteKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "generic"
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "tencent.com") || strings.Contains(host, "join.qq.com"):
		return "tencent"
	case strings.Contains(host, "bytedance.com") || strings.Contains(host, "job.toutiao.com"):
		return "bytedance"
	case strings.Contains(host, "zhipin.com"):
		return "boss"
	default:
		return "generic"
	}
}

// siteDisplayName 返回站点的人类可读名称。
func siteDisplayName(siteKey string) string {
	switch siteKey {
	case "tencent":
		return "腾讯招聘"
	case "bytedance":
		return "字节跳动招聘"
	case "boss":
		return "BOSS直聘"
	default:
		return siteKey
	}
}

// strategyName 返回当前使用的导航策略名称。
func strategyName(siteKey string) string {
	switch siteKey {
	case "tencent", "bytedance":
		return "半固定脚本（省 token）"
	case "boss":
		return "可视化逐详情采集（最多 3 条）"
	default:
		return "AI 逐步导航"
	}
}

// crawlHint 把采集错误包装成带操作指引的可读文案。
// 主要针对「未登录」这类需要用户手动介入的场景。
func (h *SearchHandler) crawlHint(rawMsg, siteKey string) string {
	if strings.Contains(rawMsg, "需要登录") || strings.Contains(rawMsg, "请先登录") {
		return fmt.Sprintf("%s\n\n操作指引：\n1. 确保终端已运行 make bw（Playwright Worker）\n2. Worker 应已弹出浏览器窗口，请在其中完成 %s 登录\n3. 登录成功后重新点击「开始爬取」",
			rawMsg, siteDisplayName(siteKey))
	}
	return rawMsg
}

// displayName 优先使用 Recipe 中配置的中文公司名，为空时回退站点标识。
func displayName(company, siteKey string) string {
	if strings.TrimSpace(company) != "" {
		return company
	}
	return siteDisplayName(siteKey)
}

// jobsFromRaw 把待入库的原始岗位转换成前端展示用的岗位入口列表。
func jobsFromRaw(raws []source.RawJob) []ai.NavJob {
	out := make([]ai.NavJob, 0, len(raws))
	for _, r := range raws {
		out = append(out, ai.NavJob{
			Title:      r.Title,
			URL:        r.URL,
			Department: firstCleanCrawlMeta(r.Meta["Department"], r.Meta["Business"]),
		})
	}
	return out
}

func jobsFromTitles(titles []string) []ai.NavJob {
	out := make([]ai.NavJob, 0, len(titles))
	for _, title := range titles {
		if t := strings.TrimSpace(title); t != "" {
			out = append(out, ai.NavJob{Title: t})
		}
	}
	return out
}

func firstCleanCrawlMeta(values ...string) string {
	for _, v := range values {
		t := strings.TrimSpace(v)
		switch strings.ToLower(t) {
		case "", "<nil>", "nil", "null", "undefined":
			continue
		default:
			return t
		}
	}
	return ""
}
