// Package response 提供统一的 JSON 响应封装。
//
// 约定格式：{"code":0,"message":"success","data":{...}}
// 错误响应只向客户端返回通用信息，详细原因写入服务端日志。
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 业务错误码。
const (
	CodeOK             = 0
	CodeInvalidParam   = 40001
	CodeNotFound       = 40401
	CodeConflict       = 40901
	CodePayloadTooLarge = 41301
	CodeUnsupportedType = 41501
	CodeTooManyRequests = 42901
	CodeInternal        = 50001
	CodeUpstreamFailed  = 50201
	CodeStateConflict   = 40902
	CodeNotImplemented  = 50101
)

// Body 是统一响应体。
type Body struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// OK 返回成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Body{Code: CodeOK, Message: "success", Data: data})
}

// Created 返回创建成功响应。
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Body{Code: CodeOK, Message: "success", Data: data})
}

// Fail 返回错误响应。message 必须是可安全暴露给客户端的通用文案，
// 不得包含 SQL、堆栈、内部地址等细节。
func Fail(c *gin.Context, httpStatus, code int, message string) {
	c.AbortWithStatusJSON(httpStatus, Body{Code: code, Message: message, Data: nil})
}

// FailWithData 返回错误响应，并携带额外的 data 字段（如冲突时的当前资源状态）。
// data 同样只应暴露可安全公开的字段。
func FailWithData(c *gin.Context, httpStatus, code int, message string, data any) {
	c.AbortWithStatusJSON(httpStatus, Body{Code: code, Message: message, Data: data})
}

// InvalidParam 参数错误。
func InvalidParam(c *gin.Context, message string) {
	if message == "" {
		message = "参数错误"
	}
	Fail(c, http.StatusBadRequest, CodeInvalidParam, message)
}

// NotFound 资源不存在。
func NotFound(c *gin.Context, message string) {
	if message == "" {
		message = "资源不存在"
	}
	Fail(c, http.StatusNotFound, CodeNotFound, message)
}

// Conflict 资源冲突。
func Conflict(c *gin.Context, message string) {
	if message == "" {
		message = "资源已存在"
	}
	Fail(c, http.StatusConflict, CodeConflict, message)
}

// StateConflict 状态机非法跃迁。
func StateConflict(c *gin.Context, message string) {
	if message == "" {
		message = "当前状态不允许该操作"
	}
	Fail(c, http.StatusConflict, CodeStateConflict, message)
}

// Internal 服务器内部错误，对外只给通用文案。
func Internal(c *gin.Context) {
	Fail(c, http.StatusInternalServerError, CodeInternal, "服务器内部错误")
}

// UpstreamFailed 上游依赖（LLM / 搜索 / 浏览器 Worker）失败。
func UpstreamFailed(c *gin.Context, message string) {
	if message == "" {
		message = "上游服务暂时不可用，请稍后重试"
	}
	Fail(c, http.StatusBadGateway, CodeUpstreamFailed, message)
}
