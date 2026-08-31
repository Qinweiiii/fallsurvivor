package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/eddiel/fallsurvivor/backend/internal/handler/dto"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	jobsvc "github.com/eddiel/fallsurvivor/backend/internal/service/job"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// JobHandler 处理岗位相关接口。
type JobHandler struct {
	svc  *jobsvc.Service
	user *userResolver
}

// NewJobHandler 创建 handler。
func NewJobHandler(svc *jobsvc.Service, store *repository.Store) *JobHandler {
	return &JobHandler{svc: svc, user: newUserResolver(store)}
}

// List 处理 GET /api/jobs。
func (h *JobHandler) List(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var q dto.JobListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.InvalidParam(c, "查询参数格式错误")
		return
	}
	if err := q.Validate(); err != nil {
		response.InvalidParam(c, err.Error())
		return
	}

	filter := repository.JobFilter{
		Keyword:     q.Keyword,
		Company:     q.Company,
		Title:       q.Title,
		Locations:   q.Locations,
		Languages:   q.Languages,
		Statuses:    q.Statuses,
		Sources:     q.Sources,
		MinScore:    q.MinScore,
		HasDeadline: q.HasDeadlineFilter(),
		SortBy:      q.SortBy,
		SortOrder:   q.SortOrder,
	}

	items, total, err := h.svc.List(c.Request.Context(), userID, filter, q.Query)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, pagination.NewPage(items, q.Query, total))
}

// Detail 处理 GET /api/jobs/:id。
func (h *JobHandler) Detail(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	jobID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "岗位 ID 格式错误")
		return
	}

	d, err := h.svc.GetDetail(c.Request.Context(), userID, jobID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, d)
}

// FilterOptions 处理 GET /api/jobs/filter-options。
func (h *JobHandler) FilterOptions(c *gin.Context) {
	opts, err := h.svc.GetFilterOptions(c.Request.Context())
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, opts)
}

// ---------------- 岗位车 ----------------

// ListCart 处理 GET /api/job-cart。
func (h *JobHandler) ListCart(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var q pagination.Query
	if err := c.ShouldBindQuery(&q); err != nil {
		response.InvalidParam(c, "查询参数格式错误")
		return
	}
	q.Normalize()

	items, total, err := h.svc.ListCart(c.Request.Context(), userID, q)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, pagination.NewPage(items, q, total))
}

// AddToCart 处理 POST /api/job-cart。
func (h *JobHandler) AddToCart(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.CartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	jobID, err := dto.ParseUUID(req.JobID)
	if err != nil {
		response.InvalidParam(c, "岗位 ID 格式错误")
		return
	}

	res, err := h.svc.AddToCart(c.Request.Context(), userID, []model.ID{jobID})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, res)
}

// AddToCartBatch 处理 POST /api/job-cart/batch。
func (h *JobHandler) AddToCartBatch(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.CartBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	ids, err := dto.ParseUUIDs(req.JobIDs)
	if err != nil {
		response.InvalidParam(c, err.Error())
		return
	}
	if len(ids) == 0 {
		response.InvalidParam(c, "请至少选择一个岗位")
		return
	}

	res, err := h.svc.AddToCart(c.Request.Context(), userID, ids)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, res)
}

// RemoveFromCart 处理 DELETE /api/job-cart/:job_id。
func (h *JobHandler) RemoveFromCart(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	jobID, err := dto.ParseUUID(c.Param("job_id"))
	if err != nil {
		response.InvalidParam(c, "岗位 ID 格式错误")
		return
	}

	n, err := h.svc.RemoveFromCart(c.Request.Context(), userID, []model.ID{jobID})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, gin.H{"removed": n})
}

// RemoveFromCartBatch 处理 DELETE /api/job-cart/batch。
func (h *JobHandler) RemoveFromCartBatch(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.CartBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	ids, err := dto.ParseUUIDs(req.JobIDs)
	if err != nil {
		response.InvalidParam(c, err.Error())
		return
	}

	n, err := h.svc.RemoveFromCart(c.Request.Context(), userID, ids)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, gin.H{"removed": n})
}

// ---------------- Dashboard ----------------

// Dashboard 处理 GET /api/dashboard。
func (h *JobHandler) Dashboard(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	data, err := h.svc.GetDashboard(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, data)
}
