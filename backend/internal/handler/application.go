package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/eddiel/fallsurvivor/backend/internal/handler/dto"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	appsvc "github.com/eddiel/fallsurvivor/backend/internal/service/application"
	"github.com/eddiel/fallsurvivor/backend/pkg/pagination"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// ApplicationHandler 处理投递任务相关接口。
type ApplicationHandler struct {
	svc  *appsvc.Service
	user *userResolver
}

// NewApplicationHandler 创建 handler。
func NewApplicationHandler(svc *appsvc.Service, store *repository.Store) *ApplicationHandler {
	return &ApplicationHandler{svc: svc, user: newUserResolver(store)}
}

// List 处理 GET /api/applications。
func (h *ApplicationHandler) List(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var q dto.ApplicationListQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		response.InvalidParam(c, "查询参数格式错误")
		return
	}
	if err := q.Validate(); err != nil {
		response.InvalidParam(c, err.Error())
		return
	}

	filter := repository.ApplicationFilter{
		Statuses: q.Statuses,
		Keyword:  q.Keyword,
		Company:  q.Company,
	}
	switch q.Scope {
	case "active":
		filter.OnlyActive = true
	case "finished":
		filter.OnlyFinished = true
	}

	items, total, err := h.svc.List(c.Request.Context(), userID, filter, q.Query)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, pagination.NewPage(items, q.Query, total))
}

// Create 处理 POST /api/applications。
func (h *ApplicationHandler) Create(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.ApplicationCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	jobID, err := dto.ParseUUID(req.JobID)
	if err != nil {
		response.InvalidParam(c, "岗位 ID 格式错误")
		return
	}

	res, err := h.svc.CreateFromJobs(c.Request.Context(), userID, []model.ID{jobID})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	if res.Created == 0 && res.Existing == 0 {
		response.InvalidParam(c, "该岗位不在岗位车中，请先加入岗位车")
		return
	}
	response.Created(c, res)
}

// CreateBatch 处理 POST /api/applications/batch。
func (h *ApplicationHandler) CreateBatch(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.ApplicationBatchRequest
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

	res, err := h.svc.CreateFromJobs(c.Request.Context(), userID, ids)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.Created(c, res)
}

// Detail 处理 GET /api/applications/:id。
func (h *ApplicationHandler) Detail(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	d, err := h.svc.GetDetail(c.Request.Context(), userID, id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, d)
}

// UpdateStatus 处理 PUT /api/applications/:id/status。
func (h *ApplicationHandler) UpdateStatus(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	var req dto.UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if err := req.Validate(); err != nil {
		response.InvalidParam(c, "状态值不合法")
		return
	}

	app, err := h.svc.UpdateStatus(c.Request.Context(), userID, id, req.Status, req.Note)
	if errors.Is(err, appsvc.ErrIllegalTransition) {
		response.StateConflict(c, err.Error())
		return
	}
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, app)
}

// MarkSubmitted 处理 POST /api/applications/:id/mark-submitted。
//
// 语义：用户已经在招聘网站真实完成提交，此处只更新本系统状态。
// 系统不提供任何代替用户点击提交按钮的接口。
func (h *ApplicationHandler) MarkSubmitted(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	app, err := h.svc.MarkSubmitted(c.Request.Context(), userID, id)
	if errors.Is(err, appsvc.ErrIllegalTransition) {
		response.StateConflict(c, err.Error())
		return
	}
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, app)
}

// StatusOptions 处理 GET /api/applications/status-options。
func (h *ApplicationHandler) StatusOptions(c *gin.Context) {
	type option struct {
		Value string `json:"value"`
		Label string `json:"label"`
	}
	opts := make([]option, 0, len(model.AllApplicationStatuses))
	for _, s := range model.AllApplicationStatuses {
		opts = append(opts, option{Value: s, Label: model.StatusLabels[s]})
	}
	response.OK(c, opts)
}
