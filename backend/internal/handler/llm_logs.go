package handler

import (
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/pkg/response"
	"github.com/gin-gonic/gin"
)

// LLMLogHandler 处理 LLM 调用日志查看接口。
type LLMLogHandler struct{}

func NewLLMLogHandler() *LLMLogHandler { return &LLMLogHandler{} }

// List 处理 GET /api/llm-logs。
// 返回最近 N 条 LLM 调用记录（含完整 request/response），按时间倒序。
func (h *LLMLogHandler) List(c *gin.Context) {
	logs := ai.RecentLLMCalls(0)
	response.OK(c, gin.H{
		"logs":  logs,
		"count": len(logs),
	})
}
