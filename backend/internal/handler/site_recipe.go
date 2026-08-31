package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// SiteRecipeHandler 处理站点 Recipe 的配置管理接口。
//
// 这些接口让「某公司校招站点怎么采」成为可配置数据，
// 新增一家公司 = 新增一条记录，不需要改后端代码。
type SiteRecipeHandler struct {
	repo *site.Repository
}

// NewSiteRecipeHandler 创建 handler。
func NewSiteRecipeHandler(repo *site.Repository) *SiteRecipeHandler {
	return &SiteRecipeHandler{repo: repo}
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

// validateRecipe 校验 Recipe 必填与取值合法性。
func validateRecipe(rc *site.Recipe) error {
	rc.SiteKey = strings.TrimSpace(rc.SiteKey)
	rc.CompanyName = strings.TrimSpace(rc.CompanyName)
	rc.Domain = strings.TrimSpace(rc.Domain)

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
	case site.StrategyBrowser, site.StrategyAPI, site.StrategyURLTemplate:
	default:
		return errRecipe("采集策略不合法，应为 browser / api / url_template")
	}
	// browser 策略必须有 adapter_key，否则 Worker 侧无法路由。
	if rc.StrategyType == site.StrategyBrowser && strings.TrimSpace(rc.AdapterKey) == "" {
		return errRecipe("浏览器策略必须填写 adapter_key")
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

// errRecipe 构造一个普通错误，供 validateRecipe 返回。
func errRecipe(msg string) error { return &recipeErr{msg} }

type recipeErr struct{ msg string }

func (e *recipeErr) Error() string { return e.msg }
