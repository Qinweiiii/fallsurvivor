package job

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

func TestSanitizeJobOutputCleansNilPlaceholders(t *testing.T) {
	j := &model.Job{
		CompanyName: "<nil>",
		Department:  "null",
		Business:    "平台业务",
		Location:    "undefined",
		JobType:     "校园招聘",
		MatchAnalysis: model.JSONMap{
			"summary": "整体匹配 <nil>",
			"reasons": []any{"技术栈匹配", "null"},
			"risks":   []any{"工作地点不在偏好城市列表：<nil>", "缺少 CUDA 经验"},
		},
	}

	sanitizeJobOutput(j)

	if j.CompanyName != "" || j.Department != "" || j.Location != "" {
		t.Fatalf("标量字段未清洗干净: company=%q department=%q location=%q", j.CompanyName, j.Department, j.Location)
	}
	if j.Business != "平台业务" || j.JobType != "校园招聘" {
		t.Fatalf("有效标量字段不应被清空: business=%q job_type=%q", j.Business, j.JobType)
	}
	if got := j.MatchAnalysis["summary"]; got != "整体匹配" {
		t.Fatalf("summary = %#v, want 整体匹配", got)
	}
	risks, ok := j.MatchAnalysis["risks"].([]any)
	if !ok {
		t.Fatalf("risks 类型 = %T, want []any", j.MatchAnalysis["risks"])
	}
	if len(risks) != 1 || risks[0] != "缺少 CUDA 经验" {
		t.Fatalf("risks = %#v, want only 缺少 CUDA 经验", risks)
	}
}
