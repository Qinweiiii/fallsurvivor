// Package task 定义异步任务的类型与派发。
//
// 第一版使用 asynq（基于 Redis），自带重试、超时与死信队列，
// 避免手写队列带来的可靠性问题。
package task

import (
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"
)

// 任务类型。
const (
	TypeJobSearch = "job:search"
)

// 队列名。
const (
	QueueDefault = "default"
)

// JobSearchPayload 是搜索任务载荷。
type JobSearchPayload struct {
	TaskID string `json:"task_id"`
	UserID string `json:"user_id"`
}

// NewJobSearchTask 构造搜索任务。
func NewJobSearchTask(taskID, userID string) (*asynq.Task, error) {
	payload, err := json.Marshal(JobSearchPayload{TaskID: taskID, UserID: userID})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeJobSearch, payload,
		asynq.Queue(QueueDefault),
		// 搜索任务较长，给足超时；失败最多重试 2 次。
		asynq.Timeout(12*time.Minute),
		asynq.MaxRetry(2),
		asynq.Retention(24*time.Hour),
	), nil
}
