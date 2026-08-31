package handler

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/handler/dto"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// BrowserHandler 处理浏览器辅助填写相关接口。
//
// 注意：本 handler 刻意不提供任何「提交申请」接口。
// 最终提交必须由用户在招聘网站上亲自完成。
type BrowserHandler struct {
	svc  *browser.Service
	user *userResolver
}

// NewBrowserHandler 创建 handler。
func NewBrowserHandler(svc *browser.Service, store *repository.Store) *BrowserHandler {
	return &BrowserHandler{svc: svc, user: newUserResolver(store)}
}

// Start 处理 POST /api/applications/:id/browser/start。
func (h *BrowserHandler) Start(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	appID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	res, err := h.svc.Start(c.Request.Context(), userID, appID)
	if errors.Is(err, browser.ErrWorkerUnavailable) {
		response.UpstreamFailed(c, "Playwright Worker 未运行，请先执行 make bw 启动")
		return
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, "")
			return
		}
		// 业务性错误（缺少申请地址等）可以直接告知用户。
		logErr(c, "启动浏览器辅助任务失败", err)
		response.InvalidParam(c, err.Error())
		return
	}
	response.OK(c, res)
}

// Get 处理 GET /api/browser-tasks/:id。
func (h *BrowserHandler) Get(c *gin.Context) {
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

// Resume 处理 POST /api/browser-tasks/:id/resume。
//
// 用户完成登录、验证码、敏感字段填写等人工操作后调用。
func (h *BrowserHandler) Resume(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	taskID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	task, err := h.svc.Resume(c.Request.Context(), userID, taskID)
	if errors.Is(err, browser.ErrWorkerUnavailable) {
		response.UpstreamFailed(c, "Playwright Worker 未运行，请先启动")
		return
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, "")
			return
		}
		logErr(c, "继续浏览器任务失败", err)
		response.InvalidParam(c, err.Error())
		return
	}
	response.OK(c, task)
}

// Pause 处理 POST /api/browser-tasks/:id/pause。
func (h *BrowserHandler) Pause(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	taskID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "任务 ID 格式错误")
		return
	}

	task, err := h.svc.Pause(c.Request.Context(), userID, taskID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, task)
}

// ScrapeJob 处理 POST /api/jobs/:id/scrape。
//
// 用已登录浏览器抓取岗位真实 JD。若目标页面需要登录，返回 needs_login=true
// 与 task_id，由前端引导用户在浏览器中完成登录后再调用 ScrapeJobResume。
func (h *BrowserHandler) ScrapeJob(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	jobID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "岗位 ID 格式错误")
		return
	}

	res, err := h.svc.Scrape(c.Request.Context(), userID, jobID)
	if errors.Is(err, browser.ErrWorkerUnavailable) {
		response.UpstreamFailed(c, "Playwright Worker 未运行，请先执行 make bw 启动")
		return
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			response.NotFound(c, "")
			return
		}
		logErr(c, "抓取岗位 JD 失败", err)
		response.InvalidParam(c, err.Error())
		return
	}
	response.OK(c, res)
}

// ScrapeJobResume 处理 POST /api/jobs/:id/scrape/resume。
// 用户在浏览器中完成登录后，带上 task_id 继续抓取真实 JD。
func (h *BrowserHandler) ScrapeJobResume(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	jobID, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "岗位 ID 格式错误")
		return
	}

	var body struct {
		TaskID string `json:"task_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.TaskID == "" {
		response.InvalidParam(c, "缺少 task_id")
		return
	}

	res, err := h.svc.ScrapeResume(c.Request.Context(), userID, jobID, body.TaskID)
	if errors.Is(err, browser.ErrWorkerUnavailable) {
		response.UpstreamFailed(c, "Playwright Worker 未运行，请先启动")
		return
	}
	if err != nil {
		logErr(c, "继续抓取岗位 JD 失败", err)
		response.InvalidParam(c, err.Error())
		return
	}
	response.OK(c, res)
}
