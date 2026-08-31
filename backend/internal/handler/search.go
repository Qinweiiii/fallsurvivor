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
	"github.com/eddiel/fallsurvivor/backend/internal/handler/dto"
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
		URL      string `json:"url"`
		SiteKey  string `json:"site_key"`
		Strategy string `json:"strategy"` // auto / script / ai
		Keyword  string `json:"keyword"`  // 可选搜索关键词（如腾讯校招在列表页搜索框提交）
		Company  string `json:"company"`  // 可选公司名，用于按公司反查站点 Recipe
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

	taskID := uuid.NewString()

	// ---- Fast Path：先查站点 Recipe ----
	// 命中已配置站点（如腾讯、字节校招）时，按 Recipe 采集并直接标准化入库，
	// 绕开联网搜索脏文本的 LLM 解析。未命中则回退到下方通用 AI 导航。
	// 新增一家公司只需在配置页加一条 Recipe，无需改动本文件。
	if h.discovery != nil {
		fast, fastErr := h.discovery.FastPath(c.Request.Context(), searchsvc.DiscoveryParams{
			TaskID:  taskID,
			SiteKey: siteKey,
			URL:     req.URL,
			Company: req.Company,
			Keyword: req.Keyword,
		})
		if fast != nil && fast.Hit {
			if fastErr != nil {
				// 命中了 Recipe 但执行失败：给出明确指引（多为未登录）。
				response.UpstreamFailed(c, "导航爬虫执行失败："+h.crawlHint(fastErr.Error(), siteKey))
				return
			}
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
			})
			return
		}
		// 未命中 Recipe：落到下方通用路径。
	}

	// ---- Discovery Path：通用 AI 导航 ----
	// 字节跳动 / 腾讯校招走「半固定脚本」：Worker 从官方列表页抽取结构化岗位卡片，
	// 并抓取详情页 JD 正文，直接标准化入库（绕开联网搜索脏文本）。
	// 注意：腾讯社招走 careers.tencent.com 官方 API（见 source/tencent.go），本分支仅覆盖校招官网 join.qq.com。
	if siteKey == "bytedance" || siteKey == "tencent" {
		// 脱离请求上下文：HTTP 服务端超时 / 客户端断开会取消 c.Request.Context()，
		// 而整个 crawl+入库（含浏览器等待与逐条 LLM 解析）往往耗时数十秒，
		// 一旦被取消，入库首步的 DB 查询就会报 context canceled 且库里一条都没落。
		// 这里用 WithoutCancel 隔离，并单独设一个宽上限避免无限挂起。
		opCtx, opCancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Minute)
		defer opCancel()

		var navJobs []ai.NavJob
		var raws []source.RawJob
		var crawlErr error
		if siteKey == "bytedance" {
			navJobs, raws, crawlErr = h.crawler.CrawlByteDance(opCtx, taskID, req.URL)
		} else {
			navJobs, raws, crawlErr = h.crawler.CrawlTencent(opCtx, taskID, req.URL, req.Keyword)
		}
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
		out = append(out, ai.NavJob{Title: r.Title, URL: r.URL})
	}
	return out
}
