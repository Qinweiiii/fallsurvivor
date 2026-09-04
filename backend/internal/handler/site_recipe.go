package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	searchsvc "github.com/eddiel/fallsurvivor/backend/internal/service/search"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// SiteRecipeHandler 处理站点 Recipe 的配置管理接口。
//
// 这些接口让「某公司校招站点怎么采」成为可配置数据，
// 新增一家公司 = 新增一条记录，不需要改后端代码。
type SiteRecipeHandler struct {
	repo *site.Repository
	// discovery 提供真实请求验证能力；未注入时验证接口返回 501。
	discovery *searchsvc.DiscoveryService
}

// NewSiteRecipeHandler 创建 handler。
func NewSiteRecipeHandler(repo *site.Repository) *SiteRecipeHandler {
	return &SiteRecipeHandler{repo: repo}
}

// AttachDiscovery 注入发现服务，启用「立即验证」能力。
//
// 单独注入而不放进构造函数：验证依赖出网抓取，
// 在未配置 LLM / 关闭出网的部署里应当可以缺省。
func (h *SiteRecipeHandler) AttachDiscovery(d *searchsvc.DiscoveryService) {
	h.discovery = d
}

// List 处理 GET /api/site-recipes。
func (h *SiteRecipeHandler) List(c *gin.Context) {
	list, err := h.repo.ListAll(c.Request.Context())
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, response.CodeInternal, "读取站点配置失败")
		return
	}
	response.OK(c, gin.H{"recipes": list, "count": len(list)})
}

// Create 处理 POST /api/site-recipes。
func (h *SiteRecipeHandler) Create(c *gin.Context) {
	var req site.Recipe
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if err := validateRecipe(&req); err != nil {
		response.InvalidParam(c, err.Error())
		return
	}
	// site_key 唯一性预检，给出比数据库约束更友好的提示。
	if existing, _ := h.repo.GetBySiteKey(c.Request.Context(), req.SiteKey); existing != nil {
		response.Fail(c, http.StatusConflict, response.CodeConflict, "站点标识已存在："+req.SiteKey)
		return
	}
	req.ID = uuid.NewString()
	if req.Source == "" {
		req.Source = site.SourceManual
	}
	if req.Version <= 0 {
		req.Version = 1
	}
	if err := h.repo.Create(c.Request.Context(), &req); err != nil {
		response.Fail(c, http.StatusInternalServerError, response.CodeInternal, "创建站点配置失败")
		return
	}
	response.Created(c, req)
}

// Update 处理 PUT /api/site-recipes/:id。
func (h *SiteRecipeHandler) Update(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		response.InvalidParam(c, "ID 格式错误")
		return
	}
	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil || existing == nil {
		response.NotFound(c, "站点配置不存在")
		return
	}

	var req site.Recipe
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if err := validateRecipe(&req); err != nil {
		response.InvalidParam(c, err.Error())
		return
	}

	// 保留不可变字段，其余按请求体更新。
	req.ID = id
	req.SiteKey = existing.SiteKey
	req.CreatedAt = existing.CreatedAt
	if req.Source == "" {
		req.Source = existing.Source
	}
	// 内容有实质变化时递增版本号，便于追溯。
	if req.Version <= existing.Version {
		req.Version = existing.Version + 1
	}
	if err := h.repo.Update(c.Request.Context(), &req); err != nil {
		response.Fail(c, http.StatusInternalServerError, response.CodeInternal, "更新站点配置失败")
		return
	}
	response.OK(c, req)
}

// Delete 处理 DELETE /api/site-recipes/:id。
func (h *SiteRecipeHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		response.InvalidParam(c, "ID 格式错误")
		return
	}
	if err := h.repo.Delete(c.Request.Context(), id); err != nil {
		response.Fail(c, http.StatusInternalServerError, response.CodeInternal, "删除站点配置失败")
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

// Runs 处理 GET /api/site-recipes/:id/runs，返回该站点最近的执行记录。
func (h *SiteRecipeHandler) Runs(c *gin.Context) {
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		response.InvalidParam(c, "ID 格式错误")
		return
	}
	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil || existing == nil {
		response.NotFound(c, "站点配置不存在")
		return
	}
	runs, err := h.repo.ListRuns(c.Request.Context(), existing.SiteKey, 50)
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, response.CodeInternal, "读取执行记录失败")
		return
	}
	response.OK(c, gin.H{"runs": runs, "count": len(runs)})
}

// Verify 处理 POST /api/site-recipes/:id/verify。
//
// 用真实请求验证该配置能否采到岗位，并把结果写回健康度。
// 「能不能跑」应该由系统自己试出来，而不是靠人读配置猜。
//
// 请求体可选：{"keyword":"后端"}。不传时用通用兜底关键词。
func (h *SiteRecipeHandler) Verify(c *gin.Context) {
	if h.discovery == nil {
		response.Fail(c, http.StatusNotImplemented, response.CodeNotImplemented, "未启用配置验证能力")
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		response.InvalidParam(c, "ID 格式错误")
		return
	}
	existing, err := h.repo.GetByID(c.Request.Context(), id)
	if err != nil || existing == nil {
		response.NotFound(c, "站点配置不存在")
		return
	}
	// browser 策略依赖浏览器会话与登录态，无法脱离会话独立复现。
	if existing.StrategyType != site.StrategyAPI {
		response.InvalidParam(c, "仅接口直调（api）策略支持独立验证；浏览器策略请直接执行一次采集")
		return
	}

	var req struct {
		Keyword string `json:"keyword"`
	}
	// 请求体可缺省，绑定失败不视为错误。
	_ = c.ShouldBindJSON(&req)

	// 脱离请求上下文：验证要发真实外部请求，客户端断开不该中断写回健康度。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 60*time.Second)
	defer cancel()

	res := h.discovery.VerifyRecipe(ctx, existing, req.Keyword)
	response.OK(c, res)
}

// validateRecipe 校验 Recipe 必填与取值合法性。
func validateRecipe(rc *site.Recipe) error {
	rc.SiteKey = strings.TrimSpace(rc.SiteKey)
	rc.CompanyName = strings.TrimSpace(rc.CompanyName)
	rc.Domain = strings.TrimSpace(rc.Domain)
	rc.AdapterKey = strings.TrimSpace(rc.AdapterKey)
	rc.ListAPI = strings.TrimSpace(rc.ListAPI)
	rc.DetailAPI = strings.TrimSpace(rc.DetailAPI)
	rc.DetailURLTemplate = strings.TrimSpace(rc.DetailURLTemplate)
	rc.Method = strings.ToUpper(strings.TrimSpace(rc.Method))
	rc.IDField = strings.TrimSpace(rc.IDField)
	rc.TitleField = strings.TrimSpace(rc.TitleField)
	rc.ListPath = strings.TrimSpace(rc.ListPath)
	rc.KeywordParam = strings.TrimSpace(rc.KeywordParam)
	rc.RequestBody = strings.TrimSpace(rc.RequestBody)
	rc.RequestContentType = strings.ToLower(strings.TrimSpace(rc.RequestContentType))

	if rc.SiteKey == "" {
		return errRecipe("站点标识不能为空")
	}
	if rc.CompanyName == "" {
		return errRecipe("公司名不能为空")
	}
	if rc.Domain == "" {
		return errRecipe("域名不能为空")
	}
	// 策略白名单。
	switch rc.StrategyType {
	case site.StrategyBrowser, site.StrategyAPI, site.StrategyBrowserObserved, site.StrategyURLTemplate:
	default:
		return errRecipe("采集策略不合法，应为 browser / api / browser_observed / url_template")
	}
	if rc.Method == "" {
		rc.Method = "GET"
	}
	if rc.Method != "GET" && rc.Method != "POST" {
		return errRecipe("method 必须为 GET 或 POST")
	}
	// browser 策略必须有 adapter_key，否则 Worker 侧无法路由。
	if rc.StrategyType == site.StrategyBrowser && rc.AdapterKey == "" {
		return errRecipe("浏览器策略必须填写 adapter_key")
	}
	if rc.StrategyType == site.StrategyAPI || rc.StrategyType == site.StrategyBrowserObserved {
		if rc.ListAPI == "" {
			return errRecipe("api/browser_observed 策略必须填写 list_api")
		}
		if rc.ListPath == "" {
			return errRecipe("api/browser_observed 策略必须填写 list_path")
		}
		if rc.TitleField == "" {
			return errRecipe("api/browser_observed 策略必须填写 title_field")
		}
	}
	if err := validateRequestBody(rc); err != nil {
		return err
	}
	if err := validateVerifyStatus(rc); err != nil {
		return err
	}
	// 兜底默认值，避免误配导致全量抓取或零结果。
	if rc.MaxJobsPerSearch == 0 {
		rc.MaxJobsPerSearch = 20
	}
	if rc.MaxJobsPerSearch < 0 {
		return errRecipe("max_jobs_per_search 不能为负数")
	}
	if rc.MaxDetailFetches < 0 {
		return errRecipe("max_detail_fetches 不能为负数")
	}
	return nil
}

// validateRequestBody 校验请求侧配置。
//
// 请求体统一以 JSON 文本存储（表单型在执行时再转换），
// 这样无论人工录入还是探索产出都能被同一套逻辑校验与展示。
// 在此处拦掉畸形 JSON，避免执行时才失败。
func validateRequestBody(rc *site.Recipe) error {
	// GET 不该带请求体：留着只会让「实际发出了什么」变得含糊。
	if rc.Method != "POST" {
		rc.RequestBody = ""
		rc.RequestContentType = ""
		return nil
	}
	switch rc.RequestContentType {
	case "", "application/json", "application/x-www-form-urlencoded":
	default:
		return errRecipe("request_content_type 仅支持 application/json 或 application/x-www-form-urlencoded")
	}
	if rc.RequestBody == "" {
		return nil
	}
	if !json.Valid([]byte(rc.RequestBody)) {
		return errRecipe("request_body 必须是合法 JSON（表单型也用 JSON 对象描述，执行时自动转换）")
	}
	return nil
}

// validateVerifyStatus 校验并兜底验证状态。
//
// 人工录入的配置默认 unverified：只有真实试跑过才算 verified，
// 允许前端随手填 verified 会让健康度失去意义。
func validateVerifyStatus(rc *site.Recipe) error {
	switch rc.VerifyStatus {
	case "":
		rc.VerifyStatus = site.VerifyUnverified
	case site.VerifyUnverified, site.VerifyVerified, site.VerifyInvalid:
	default:
		return errRecipe("verify_status 不合法，应为 unverified / verified / invalid")
	}
	if rc.ConsecutiveFailures < 0 {
		rc.ConsecutiveFailures = 0
	}
	if rc.VerifiedJobs < 0 {
		rc.VerifiedJobs = 0
	}
	return nil
}

// errRecipe 构造一个普通错误，供 validateRecipe 返回。
func errRecipe(msg string) error { return &recipeErr{msg} }

type recipeErr struct{ msg string }

func (e *recipeErr) Error() string { return e.msg }
