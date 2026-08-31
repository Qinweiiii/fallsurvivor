// Package application 负责投递任务的业务逻辑。
package application

import (
	"errors"
	"fmt"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// ErrIllegalTransition 表示状态跃迁不被允许。
var ErrIllegalTransition = errors.New("状态跃迁不被允许")

// transitions 定义合法的状态跃迁图。
//
// 这是投递流程的唯一权威定义。任何状态变更都必须通过 CanTransition 校验，
// 禁止在 handler 或 repository 中直接写状态。
//
// 注意：图中不存在任何「系统自动提交」的路径。
// READY_TO_SUBMIT → SUBMITTED 只能由用户在招聘网站真实提交后，
// 主动调用 mark-submitted 触发。
var transitions = map[string][]string{
	model.AppStatusPreparing: {
		model.AppStatusLoginRequired,
		model.AppStatusFormAnalyzing,
		model.AppStatusWaitingUser,
		model.AppStatusReadyToSubmit, // 允许用户跳过自动化直接手工投
		model.AppStatusSubmitted,     // 允许用户手工投完直接标记
		model.AppStatusWithdrawn,
		model.AppStatusFailed,
		model.AppStatusBlocked,
	},
	model.AppStatusLoginRequired: {
		model.AppStatusFormAnalyzing,
		model.AppStatusWaitingUser,
		model.AppStatusPreparing,
		model.AppStatusWithdrawn,
		model.AppStatusFailed,
		model.AppStatusBlocked,
	},
	model.AppStatusFormAnalyzing: {
		model.AppStatusFormFilling,
		model.AppStatusWaitingUser,
		model.AppStatusFailed,
		model.AppStatusBlocked,
		model.AppStatusWithdrawn,
	},
	model.AppStatusFormFilling: {
		model.AppStatusWaitingUser,
		model.AppStatusReadyToSubmit,
		model.AppStatusFailed,
		model.AppStatusBlocked,
		model.AppStatusWithdrawn,
	},
	model.AppStatusWaitingUser: {
		model.AppStatusFormFilling,
		model.AppStatusFormAnalyzing,
		model.AppStatusReadyToSubmit,
		model.AppStatusSubmitted,
		model.AppStatusFailed,
		model.AppStatusBlocked,
		model.AppStatusWithdrawn,
	},
	model.AppStatusReadyToSubmit: {
		model.AppStatusSubmitted,
		model.AppStatusWaitingUser,
		model.AppStatusWithdrawn,
		model.AppStatusFailed,
	},
	model.AppStatusSubmitted: {
		model.AppStatusWrittenTest,
		model.AppStatusInterview1,
		model.AppStatusOffer,
		model.AppStatusRejected,
		model.AppStatusWithdrawn,
	},
	model.AppStatusWrittenTest: {
		model.AppStatusInterview1,
		model.AppStatusOffer,
		model.AppStatusRejected,
		model.AppStatusWithdrawn,
	},
	model.AppStatusInterview1: {
		model.AppStatusInterview2,
		model.AppStatusHRInterview,
		model.AppStatusOffer,
		model.AppStatusRejected,
		model.AppStatusWithdrawn,
	},
	model.AppStatusInterview2: {
		model.AppStatusHRInterview,
		model.AppStatusOffer,
		model.AppStatusRejected,
		model.AppStatusWithdrawn,
	},
	model.AppStatusHRInterview: {
		model.AppStatusOffer,
		model.AppStatusRejected,
		model.AppStatusWithdrawn,
	},
	model.AppStatusFailed: {
		model.AppStatusPreparing,
		model.AppStatusWaitingUser,
		model.AppStatusSubmitted,
		model.AppStatusWithdrawn,
	},
	model.AppStatusBlocked: {
		model.AppStatusPreparing,
		model.AppStatusWaitingUser,
		model.AppStatusSubmitted,
		model.AppStatusWithdrawn,
	},
	// 终态无出边。
	model.AppStatusOffer:     {},
	model.AppStatusRejected:  {},
	model.AppStatusWithdrawn: {},
}

// CanTransition 报告 from → to 是否合法。
func CanTransition(from, to string) bool {
	if from == to {
		return true // 幂等更新视为合法
	}
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// AllowedNextStatuses 返回某状态可以跃迁到的状态列表，用于前端渲染可选项。
func AllowedNextStatuses(from string) []string {
	allowed, ok := transitions[from]
	if !ok {
		return []string{}
	}
	out := make([]string, len(allowed))
	copy(out, allowed)
	return out
}

// ValidateTransition 校验跃迁，非法时返回带上下文的错误。
func ValidateTransition(from, to string) error {
	if !isValidStatus(to) {
		return fmt.Errorf("%w: 未知状态 %q", ErrIllegalTransition, to)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("%w: 不能从 %s 变更为 %s",
			ErrIllegalTransition, label(from), label(to))
	}
	return nil
}

// isValidStatus 报告状态是否在枚举内。
func isValidStatus(s string) bool {
	_, ok := transitions[s]
	return ok
}

// label 返回状态的中文名。
func label(s string) string {
	if l, ok := model.StatusLabels[s]; ok {
		return l
	}
	return s
}

// progressOf 返回状态对应的默认进度百分比。
func progressOf(status string) int {
	switch status {
	case model.AppStatusPreparing:
		return 5
	case model.AppStatusLoginRequired:
		return 15
	case model.AppStatusFormAnalyzing:
		return 35
	case model.AppStatusFormFilling:
		return 55
	case model.AppStatusWaitingUser:
		return 70
	case model.AppStatusReadyToSubmit:
		return 90
	case model.AppStatusSubmitted, model.AppStatusWrittenTest,
		model.AppStatusInterview1, model.AppStatusInterview2,
		model.AppStatusHRInterview, model.AppStatusOffer:
		return 100
	case model.AppStatusRejected, model.AppStatusWithdrawn:
		return 100
	default:
		return 0
	}
}

// eventTypeFor 返回状态变更对应的事件类型。
func eventTypeFor(to string) string {
	switch to {
	case model.AppStatusWaitingUser:
		return model.EventUserRequired
	case model.AppStatusFormFilling:
		return model.EventFormFilled
	case model.AppStatusReadyToSubmit:
		return model.EventReadyToSubmit
	case model.AppStatusSubmitted:
		return model.EventUserSubmitted
	case model.AppStatusFailed, model.AppStatusBlocked:
		return model.EventBrowserFailed
	default:
		return model.EventStatusChanged
	}
}
