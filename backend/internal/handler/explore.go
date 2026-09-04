package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	searchsvc "github.com/eddiel/fallsurvivor/backend/internal/service/search"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// ExploreHandler 处理站点探索接口。
//
// 探索是「一次性投入、长期复用」的能力：对未配置的陌生站点，
// 由 Agent 自动观察页面与网络请求，推断出岗位列表的获取方式，
// 确认后可保存为 Recipe，之后该站点即走 Fast Path，无需再次探索。
type ExploreHandler struct {
	discovery *searchsvc.DiscoveryService
}

// NewExploreHandler 创建 handler。
func NewExploreHandler(d *searchsvc.DiscoveryService) *ExploreHandler {
	return &ExploreHandler{discovery: d}
}

// Explore 处理 POST /api/explore。
//
// 请求体：
//
//	{
//	  "url": "https://campus.example.com",   // 必填，探索起点
//	  "site_key": "example",                 // 可选，缺省时从域名推导
//	  "company": "某某公司",                  // 可选，用于命名
//	  "keyword": "后端",                     // 可选，用于尝试搜索动作
//	  "save": true                           // 可选，是否保存为 Recipe
//	}
//
// 返回探索轨迹与产出的配置候选。
func (h *ExploreHandler) Explore(c *gin.Context) {
	var req struct {
		URL     string `json:"url"`
		SiteKey string `json:"site_key"`
		Company string `json:"company"`
		Keyword string `json:"keyword"`
		Save    bool   `json:"save"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		response.InvalidParam(c, "url 不能为空")
		return
	}

	res, err := h.discovery.ExploreSite(c.Request.Context(), searchsvc.DiscoveryParams{
		SiteKey: req.SiteKey,
		URL:     req.URL,
		Company: req.Company,
		Keyword: req.Keyword,
	}, req.Save)
	if err != nil {
		// 需要登录属于可自助解决的场景，单独用 409 并带指引。
		if strings.Contains(err.Error(), "需要登录") {
			response.Fail(c, http.StatusConflict, response.CodeConflict, err.Error())
			return
		}
		response.UpstreamFailed(c, "站点探索失败："+err.Error())
		return
	}

	response.OK(c, gin.H{
		"success":   res.Success,
		"saved":     res.Saved,
		"reason":    res.Reason,
		"candidate": res.Candidate,
		"recipe":    res.Recipe,
		"trace":     res.Trace,
		// 验证闭环的产出：配置是否真跑通、采到多少条、样例标题，
		// 以及为跑通做了几轮自修正。没有这些，"探索成功"只是模型自我声明。
		"verified":       res.Verified,
		"verified_jobs":  res.VerifiedJobs,
		"verify_samples": res.VerifySampleTitles,
		"field_quality":  res.FieldQuality,
		"refine_rounds":  res.RefineRounds,
		"duration_ms":    res.DurationMS,
	})
}
