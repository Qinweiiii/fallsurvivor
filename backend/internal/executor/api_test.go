package executor

import (
	"encoding/json"
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

func TestAPIFieldValuePreservesNumericID(t *testing.T) {
	item := map[string]any{"jobId": float64(1000)}

	got := fieldValue(item, "jobId")
	if got != "1000" {
		t.Fatalf("fieldValue(jobId) = %q, want 1000", got)
	}
}

func TestAPIJobIdentifiersRetainsActualListFields(t *testing.T) {
	rc := &site.Recipe{ListPath: "data.items"}
	ids, err := APIJobIdentifiers(rc, []byte(`{"data":{"items":[{"postId":199907820022,"positionTitle":"算法工程师"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range ids {
		if id.Field == "postId" && id.Value == "199907820022" {
			found = true
		}
	}
	if !found {
		t.Fatalf("identifiers = %#v, want postId", ids)
	}
}

func TestAPIFieldValueTreatsNilAsEmpty(t *testing.T) {
	item := map[string]any{
		"department": nil,
		"location":   "<nil>",
	}

	if got := fieldValue(item, "department"); got != "" {
		t.Fatalf("fieldValue(nil) = %q, want empty", got)
	}
	if got := fieldValue(item, "location"); got != "" {
		t.Fatalf("fieldValue(<nil>) = %q, want empty", got)
	}
}

func TestAPIFieldValueSupportsArrayIndexPath(t *testing.T) {
	item := map[string]any{
		"requirementVoList": []any{
			map[string]any{
				"positionBg": "京东物流",
				"workCity":   "北京市-北京市",
			},
		},
	}

	if got := fieldValue(item, "requirementVoList[0].positionBg"); got != "京东物流" {
		t.Fatalf("fieldValue(array path) = %q, want 京东物流", got)
	}
	if got := fieldValue(item, "requirementVoList[0].workCity"); got != "北京市-北京市" {
		t.Fatalf("fieldValue(nested city) = %q, want 北京市-北京市", got)
	}
	if got := fieldValue(item, "requirementVoList[1].positionBg"); got != "" {
		t.Fatalf("fieldValue(out of range) = %q, want empty", got)
	}
}

func TestAPISemanticContentUsesWorkContent(t *testing.T) {
	rc := &site.Recipe{
		FieldMap: model.JSONMap{
			"work_content": "workContent",
			"requirements": "need",
		},
	}
	item := map[string]any{
		"workContent": "负责推荐系统后端开发",
		"need":        "熟悉 Go 和分布式系统",
	}

	got := semanticContent(item, rc)
	if got != "负责推荐系统后端开发\n\n熟悉 Go 和分布式系统" {
		t.Fatalf("semanticContent() = %q", got)
	}
}

func TestRenderRequestBodyInjectsNestedKeywordPath(t *testing.T) {
	rc := &site.Recipe{
		RequestBody:   `{"pageSize":10,"parameter":{"positionName":"","cityList":[]}}`,
		KeywordInBody: true,
		KeywordParam:  "parameter.positionName",
	}

	got := renderRequestBody(rc, "agent")
	var obj map[string]any
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("renderRequestBody returned invalid JSON: %v", err)
	}
	parameter := obj["parameter"].(map[string]any)
	if parameter["positionName"] != "agent" {
		t.Fatalf("positionName = %v, want agent; body=%s", parameter["positionName"], got)
	}
	if _, exists := obj["positionName"]; exists {
		t.Fatalf("unexpected top-level positionName in body=%s", got)
	}
}

func TestRenderRequestBodyInjectsExistingNestedLeaf(t *testing.T) {
	rc := &site.Recipe{
		RequestBody:   `{"pageSize":10,"parameter":{"positionName":"","cityList":[]}}`,
		KeywordInBody: true,
		KeywordParam:  "positionName",
	}

	got := renderRequestBody(rc, "agent")
	var obj map[string]any
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("renderRequestBody returned invalid JSON: %v", err)
	}
	parameter := obj["parameter"].(map[string]any)
	if parameter["positionName"] != "agent" {
		t.Fatalf("positionName = %v, want agent; body=%s", parameter["positionName"], got)
	}
	if _, exists := obj["positionName"]; exists {
		t.Fatalf("unexpected top-level positionName in body=%s", got)
	}
}

func TestCanonicalJobURLKeepsListIdentityWhenDetailURLExists(t *testing.T) {
	got := canonicalJobURL(
		"https://example.com/api/jobs?_csrf=transient",
		"123",
		"https://example.com/jobs/real",
		"https://example.com/jobs/template-123",
	)
	if got != "https://example.com/api/jobs#job_id=123" {
		t.Fatalf("canonicalJobURL() = %q", got)
	}
}

func TestCanonicalJobURLResolvesRelativeDetailURL(t *testing.T) {
	got := canonicalJobURL("https://example.com/api/jobs", "123", "/jobs/123")
	if got != "https://example.com/api/jobs#job_id=123" {
		t.Fatalf("canonicalJobURL() = %q", got)
	}
}

func TestCanonicalJobURLFallsBackToStableIDFragment(t *testing.T) {
	got := canonicalJobURL("https://example.com/api/jobs?city=beijing", "abc 123")
	if got != "https://example.com/api/jobs#job_id=abc%20123" {
		t.Fatalf("canonicalJobURL() = %q", got)
	}
}

func TestJobURLsSeparatesDetailURLFromStableIdentity(t *testing.T) {
	detail, identity := jobURLs(
		"https://example.com/api/jobs?city=beijing&_csrf=transient",
		"123",
		"/jobs/123",
	)
	if detail != "https://example.com/jobs/123" {
		t.Fatalf("detail URL = %q", detail)
	}
	if identity != "https://example.com/api/jobs#job_id=123" {
		t.Fatalf("identity URL = %q", identity)
	}
}

func TestJobURLsKeepAPIFallbackInternal(t *testing.T) {
	detail, identity := jobURLs("https://example.com/api/jobs", "123")
	if detail != "" {
		t.Fatalf("detail URL = %q, want empty without an observed detail page", detail)
	}
	if identity != "https://example.com/api/jobs#job_id=123" {
		t.Fatalf("identity URL = %q", identity)
	}
}

func TestBuildFieldQualityTreatsNilLikeEmpty(t *testing.T) {
	got := BuildFieldQuality([]source.RawJob{
		{
			Title:        "算法工程师",
			Content:      "负责算法模型开发",
			CompanyHint:  "京东",
			LocationHint: "<nil>",
			Meta: map[string]string{
				"Department": "<nil>",
				"Business":   "数据与算法类",
				"Location":   "北京",
			},
		},
	})

	byField := map[string]FieldQuality{}
	for _, q := range got {
		byField[q.Field] = q
	}
	if byField["department"].OK {
		t.Fatalf("department quality OK = true, want false")
	}
	if !byField["business"].OK || byField["business"].Samples[0] != "数据与算法类" {
		t.Fatalf("business quality = %#v", byField["business"])
	}
	if !byField["location"].OK || byField["location"].Samples[0] != "北京" {
		t.Fatalf("location quality = %#v", byField["location"])
	}
}
