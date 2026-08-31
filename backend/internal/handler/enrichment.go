package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	searchsvc "github.com/eddiel/fallsurvivor/backend/internal/service/search"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// EnrichmentHandler 处理 JD 补全相关接口。
//
// 背景：BOSS 直聘等站点有反爬，本项目不做登录态爬取，
// 只能经搜索引擎拿到摘要片段（snippet），导致 JD 不完整、匹配分不可信。
// 补全接口复用用户在 Worker 中**已登录**的浏览器打开岗位原网页读取完整 JD，
// 然后重新解析并重新评分。
type EnrichmentHandler struct {
	svc  *searchsvc.EnrichmentService
	user *userResolver
}

// NewEnrichmentHandler 创建 handler。
func NewEnrichmentHandler(svc *searchsvc.EnrichmentService, store *repository.Store) *EnrichmentHandler {
	return &EnrichmentHandler{svc: svc, user: newUserResolver(store)}
}

// Enrich 处理 POST /api/jobs/enrich-jd。
//
// 请求体可为空（自动挑选全部 JD 不完整的岗位），
// 也可指定 {"job_ids": ["uuid", ...]} 只补全特定岗位，
// 或用 {"max_jobs": 5} 限制本次处理条数。
func (h *EnrichmentHandler) Enrich(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req struct {
		JobIDs  []string `json:"job_ids"`
		MaxJobs int      `json:"max_jobs"`
	}
	// 请求体允许为空，解析失败时忽略（不阻断）。
	_ = c.ShouldBindJSON(&req)

	ids := make([]model.ID, 0, len(req.JobIDs))
	for _, s := range req.JobIDs {
		u, err := uuid.Parse(s)
		if err != nil {
			response.InvalidParam(c, "岗位 ID 格式错误: "+s)
			return
		}
		ids = append(ids, u)
	}

	summary, err := h.svc.EnrichIncomplete(c.Request.Context(), searchsvc.EnrichRequest{
		JobIDs:  ids,
		UserID:  userID,
		MaxJobs: req.MaxJobs,
	})
	if err != nil {
		// 未登录是需要用户操作的场景，单独给出 409 与操作指引。
		if strings.Contains(err.Error(), "需要登录") {
			response.Fail(c, http.StatusConflict, response.CodeConflict, err.Error())
			return
		}
		response.UpstreamFailed(c, "JD 补全失败："+err.Error())
		return
	}

	response.OK(c, gin.H{
		"total":       summary.Total,
		"enriched":    summary.Enriched,
		"skipped":     summary.Skipped,
		"failed":      summary.Failed,
		"duration_ms": summary.DurationMS,
		"results":     summary.Results,
	})
}
