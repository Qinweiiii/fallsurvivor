package search

import (
	"context"
	"errors"
	"log/slog"

	"github.com/hibiken/asynq"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
	"github.com/eddiel/fallsurvivor/backend/internal/task"
)

// ErrTaskRunning 表示已有搜索任务在执行中。
var ErrTaskRunning = errors.New("已有搜索任务正在执行，请稍候")

// Service 负责搜索任务的创建与查询。
type Service struct {
	store    *repository.Store
	enqueuer *asynq.Client
	// pipeline 在无队列（同步）模式下直接执行。
	pipeline *Pipeline
}

// NewService 创建服务。
func NewService(store *repository.Store, enqueuer *asynq.Client, pipeline *Pipeline) *Service {
	return &Service{store: store, enqueuer: enqueuer, pipeline: pipeline}
}

// Trigger 创建并派发一次搜索任务。
//
// force 为 true 时忽略「已有进行中任务」的限制。
// 接口立即返回 task_id，不阻塞 HTTP 请求。
func (s *Service) Trigger(ctx context.Context, userID model.ID, force bool) (*model.SearchTask, error) {
	if !force {
		if existing, err := s.store.SearchTask.FindActive(ctx, userID); err == nil && existing != nil {
			return existing, ErrTaskRunning
		} else if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}

	t := &model.SearchTask{
		UserID:        userID,
		Status:        model.SearchTaskPending,
		Queries:       model.JSONStringArray{},
		Warnings:      model.JSONStringArray{},
		SourceResults: model.JSONMap{},
	}
	if err := s.store.SearchTask.Create(ctx, t); err != nil {
		return nil, err
	}

	asynqTask, err := task.NewJobSearchTask(t.ID.String(), userID.String())
	if err != nil {
		return nil, err
	}

	if _, err := s.enqueuer.EnqueueContext(ctx, asynqTask); err != nil {
		_ = s.store.SearchTask.Update(ctx, t.ID, map[string]any{
			"status":        model.SearchTaskFailed,
			"error_message": "任务派发失败，请检查 Redis 是否可用",
		})
		return nil, err
	}

	slog.Info("已派发岗位搜索任务", "task_id", t.ID.String())
	return t, nil
}

// Get 查询搜索任务。
func (s *Service) Get(ctx context.Context, userID, taskID model.ID) (*model.SearchTask, error) {
	t, err := s.store.SearchTask.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	// 强制校验归属。
	if t.UserID != userID {
		return nil, repository.ErrNotFound
	}
	return t, nil
}

// GetLatest 查询最近一次搜索任务。
func (s *Service) GetLatest(ctx context.Context, userID model.ID) (*model.SearchTask, error) {
	return s.store.SearchTask.GetLatest(ctx, userID)
}

// IngestRawJobs 把已结构化的原始岗位（如浏览器半固定脚本抽取的结果）直接走
// 去重 + 标准化 + LLM 解析流程入库，绕开联网搜索的脏文本。
//
// 用于「B 沉淀策略」的浏览器脚本：Worker 已从官方列表页抽取出真实岗位链接与
// JD 正文，这里复用 pipeline 的标准化能力，让字节跳动等需要浏览器的站点也能
// 产出干净、字段准确的结构化岗位记录。
func (s *Service) IngestRawJobs(ctx context.Context, userID model.ID, raws []source.RawJob) (*IngestResult, error) {
	return s.pipeline.IngestRawJobs(ctx, userID, raws)
}
