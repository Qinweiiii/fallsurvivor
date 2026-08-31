package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// SearchRunner 是搜索管线需要实现的接口。
//
// 通过接口而不是具体类型依赖，避免 task 与 service/search 相互 import。
type SearchRunner interface {
	// Run 执行指定的搜索任务。
	Run(ctx context.Context, taskID uuid.UUID) error
}

// Handler 处理异步任务。
type Handler struct {
	search SearchRunner
}

// NewHandler 创建处理器。
func NewHandler(search SearchRunner) *Handler {
	return &Handler{search: search}
}

// Register 注册所有任务处理函数。
func (h *Handler) Register(mux *asynq.ServeMux) {
	mux.HandleFunc(TypeJobSearch, h.handleJobSearch)
}

// handleJobSearch 执行岗位搜索任务。
func (h *Handler) handleJobSearch(ctx context.Context, t *asynq.Task) error {
	// 反序列化到具体结构体，不使用 map[string]any。
	var payload JobSearchPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		// 载荷不合法时不重试。
		return fmt.Errorf("解析任务载荷失败: %v: %w", err, asynq.SkipRetry)
	}

	taskID, err := uuid.Parse(payload.TaskID)
	if err != nil {
		return fmt.Errorf("非法的任务 ID: %w", asynq.SkipRetry)
	}

	slog.Info("开始执行岗位搜索任务", "task_id", payload.TaskID)
	if err := h.search.Run(ctx, taskID); err != nil {
		slog.Error("岗位搜索任务执行失败", "task_id", payload.TaskID, "error", err.Error())
		// 被取消（服务关闭/重启）的任务不应重试：DB 中已标记为 TERMINATED，
		// 重试只会把它重新塞回队列，导致重启后已终止的批次被反复执行。
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("搜索任务被取消，不重试: %w", asynq.SkipRetry)
		}
		return err
	}
	slog.Info("岗位搜索任务完成", "task_id", payload.TaskID)
	return nil
}
