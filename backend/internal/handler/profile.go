package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/eddiel/fallsurvivor/backend/internal/handler/dto"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	profilesvc "github.com/eddiel/fallsurvivor/backend/internal/service/profile"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
)

// ProfileHandler 处理求职画像、简历与申请信息接口。
type ProfileHandler struct {
	svc  *profilesvc.Service
	user *userResolver
}

// NewProfileHandler 创建 handler。
func NewProfileHandler(svc *profilesvc.Service, store *repository.Store) *ProfileHandler {
	return &ProfileHandler{svc: svc, user: newUserResolver(store)}
}

// GetProfile 处理 GET /api/profile。
func (h *ProfileHandler) GetProfile(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	p, err := h.svc.GetProfile(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, p)
}

// UpdateProfile 处理 PUT /api/profile。
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req dto.UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if err := req.Validate(); err != nil {
		response.InvalidParam(c, err.Error())
		return
	}

	p, err := h.svc.UpdateProfile(c.Request.Context(), userID, profilesvc.UpdateProfileInput{
		TargetRoles:        req.TargetRoles,
		PreferredLanguages: req.PreferredLanguages,
		PreferredLocations: req.PreferredLocations,
		CompanyPreferences: req.CompanyPreferences,
		TargetIndustries:   req.TargetIndustries,
		GraduationYear:     req.GraduationYear,
	})
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, p)
}

// ---------------- 简历 ----------------

// ListResumes 处理 GET /api/resumes。
func (h *ProfileHandler) ListResumes(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	items, err := h.svc.ListResumes(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, gin.H{"items": items})
}

// UploadResume 处理 POST /api/resumes。
func (h *ProfileHandler) UploadResume(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	header, err := c.FormFile("file")
	if err != nil {
		response.InvalidParam(c, "请选择要上传的简历文件")
		return
	}

	rec, err := h.svc.UploadResume(c.Request.Context(), userID, header)
	switch {
	case errors.Is(err, profilesvc.ErrFileTooLarge):
		response.Fail(c, http.StatusRequestEntityTooLarge,
			response.CodePayloadTooLarge, "简历文件超出大小限制")
		return
	case errors.Is(err, profilesvc.ErrUnsupportedType),
		errors.Is(err, profilesvc.ErrContentMismatch),
		errors.Is(err, profilesvc.ErrInvalidFileName):
		response.Fail(c, http.StatusUnsupportedMediaType,
			response.CodeUnsupportedType, "只支持 PDF / DOCX / TXT / Markdown 格式的简历")
		return
	case err != nil:
		handleServiceError(c, err)
		return
	}
	response.Created(c, rec)
}

// GetResume 处理 GET /api/resumes/:id。
func (h *ProfileHandler) GetResume(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "简历 ID 格式错误")
		return
	}
	rec, err := h.svc.GetResume(c.Request.Context(), userID, id)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, rec)
}

// SetResumeText 处理 PUT /api/resumes/:id/text。
//
// 说明：PDF / DOCX 的自动文本抽取需要重型依赖，第一版让用户粘贴文本，
// 换取更稳定的解析效果与更低的维护成本。
func (h *ProfileHandler) SetResumeText(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "简历 ID 格式错误")
		return
	}

	var req dto.ResumeTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}
	if err := req.Validate(); err != nil {
		response.InvalidParam(c, err.Error())
		return
	}

	rec, err := h.svc.SetResumeText(c.Request.Context(), userID, id, req.Text)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, rec)
}

// SetCurrentResume 处理 PUT /api/resumes/:id/current。
func (h *ProfileHandler) SetCurrentResume(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "简历 ID 格式错误")
		return
	}
	if err := h.svc.SetCurrentResume(c.Request.Context(), userID, id); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// DeleteResume 处理 DELETE /api/resumes/:id。
func (h *ProfileHandler) DeleteResume(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	id, err := dto.ParseUUID(c.Param("id"))
	if err != nil {
		response.InvalidParam(c, "简历 ID 格式错误")
		return
	}
	if err := h.svc.DeleteResume(c.Request.Context(), userID, id); err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, gin.H{"ok": true})
}

// ---------------- 可复用申请信息 ----------------

// GetApplicationProfile 处理 GET /api/application-profile。
func (h *ProfileHandler) GetApplicationProfile(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}
	data, err := h.svc.GetApplicationProfile(c.Request.Context(), userID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, data)
}

// UpdateApplicationProfile 处理 PUT /api/application-profile。
//
// 只接受白名单字段，任何身份证、银行卡、密码等字段都不会被存储。
func (h *ProfileHandler) UpdateApplicationProfile(c *gin.Context) {
	userID, ok := h.user.requireUser(c)
	if !ok {
		return
	}

	var req profilesvc.ApplicationProfileData
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式错误")
		return
	}

	data, err := h.svc.UpdateApplicationProfile(c.Request.Context(), userID, &req)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	response.OK(c, data)
}
