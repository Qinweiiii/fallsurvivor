package task

import (
	"encoding/json"
	"testing"
)

func TestNewJobEnrichJDTaskPayload(t *testing.T) {
	task, err := NewJobEnrichJDTask("user-1", []string{"job-1", "job-2"}, 3, "ai")
	if err != nil {
		t.Fatalf("NewJobEnrichJDTask returned error: %v", err)
	}
	if task.Type() != TypeJobEnrichJD {
		t.Fatalf("unexpected task type: %s", task.Type())
	}

	var payload JobEnrichJDPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatalf("invalid payload: %v", err)
	}
	if payload.UserID != "user-1" || payload.MaxJobs != 3 || payload.Keyword != "ai" || len(payload.JobIDs) != 2 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
