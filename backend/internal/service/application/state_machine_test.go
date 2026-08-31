package application

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

func TestNoAutoSubmitPath(t *testing.T) {
	// 核心安全约束：不存在任何绕过用户的自动提交路径。
	// SUBMITTED 只能由 PREPARING / WAITING_USER / READY_TO_SUBMIT / FAILED / BLOCKED
	// 这些「用户已介入」的状态显式跃迁而来，且必须由 mark-submitted 接口触发。
	allowedFrom := map[string]bool{
		model.AppStatusPreparing:     true,
		model.AppStatusWaitingUser:   true,
		model.AppStatusReadyToSubmit: true,
		model.AppStatusFailed:        true,
		model.AppStatusBlocked:       true,
	}
	for from := range transitions {
		if CanTransition(from, model.AppStatusSubmitted) && from != model.AppStatusSubmitted {
			if !allowedFrom[from] {
				t.Errorf("状态 %s 不应能直接跃迁到 SUBMITTED", from)
			}
		}
	}
}

func TestLegalTransitions(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{model.AppStatusPreparing, model.AppStatusFormAnalyzing, true},
		{model.AppStatusFormAnalyzing, model.AppStatusFormFilling, true},
		{model.AppStatusFormFilling, model.AppStatusWaitingUser, true},
		{model.AppStatusWaitingUser, model.AppStatusReadyToSubmit, true},
		{model.AppStatusReadyToSubmit, model.AppStatusSubmitted, true},
		{model.AppStatusSubmitted, model.AppStatusWrittenTest, true},
		{model.AppStatusWrittenTest, model.AppStatusInterview1, true},
		{model.AppStatusInterview1, model.AppStatusOffer, true},

		// 非法：跳过整个流程
		{model.AppStatusPreparing, model.AppStatusOffer, false},
		{model.AppStatusPreparing, model.AppStatusWrittenTest, false},
		// 非法：终态不可再变
		{model.AppStatusOffer, model.AppStatusRejected, false},
		{model.AppStatusRejected, model.AppStatusOffer, false},
		{model.AppStatusWithdrawn, model.AppStatusSubmitted, false},
		// 非法：倒退到分析阶段
		{model.AppStatusSubmitted, model.AppStatusFormFilling, false},
	}

	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, 期望 %v", c.from, c.to, got, c.want)
		}
	}
}

func TestSameStatusIsIdempotent(t *testing.T) {
	for _, s := range model.AllApplicationStatuses {
		if !CanTransition(s, s) {
			t.Errorf("相同状态的跃迁应视为幂等成功: %s", s)
		}
	}
}

func TestValidateTransitionRejectsUnknownStatus(t *testing.T) {
	if err := ValidateTransition(model.AppStatusPreparing, "NOT_A_STATUS"); err == nil {
		t.Error("未知状态应被拒绝")
	}
}

func TestAllStatusesAreReachableInGraph(t *testing.T) {
	// 每个声明的状态都必须出现在跃迁图中，防止枚举与状态机脱节。
	for _, s := range model.AllApplicationStatuses {
		if _, ok := transitions[s]; !ok {
			t.Errorf("状态 %s 未在跃迁图中定义", s)
		}
	}
}

func TestTerminalStatusesHaveNoOutgoing(t *testing.T) {
	terminals := []string{model.AppStatusOffer, model.AppStatusRejected, model.AppStatusWithdrawn}
	for _, s := range terminals {
		if len(transitions[s]) != 0 {
			t.Errorf("终态 %s 不应有出边", s)
		}
	}
}

func TestProgressMonotonic(t *testing.T) {
	seq := []string{
		model.AppStatusPreparing,
		model.AppStatusLoginRequired,
		model.AppStatusFormAnalyzing,
		model.AppStatusFormFilling,
		model.AppStatusWaitingUser,
		model.AppStatusReadyToSubmit,
		model.AppStatusSubmitted,
	}
	prev := -1
	for _, s := range seq {
		p := progressOf(s)
		if p <= prev {
			t.Errorf("进度应随流程递增，状态 %s 的进度 %d 不大于前一步 %d", s, p, prev)
		}
		prev = p
	}
}
