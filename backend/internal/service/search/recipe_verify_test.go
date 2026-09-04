package search

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
)

func TestActionChangedPageRejectsUnchangedSearchPage(t *testing.T) {
	before := &browser.ExploreSnapshot{
		CurrentURL: "https://example.com/jobs",
		Title:      "招聘职位",
		TextSample: "在招职位 126 个",
	}
	after := &browser.ExploreSnapshot{
		CurrentURL: "https://example.com/jobs",
		Title:      "招聘职位",
		TextSample: "在招职位 126 个",
	}
	if actionChangedPage(before, after) {
		t.Fatal("input/Enter without visible result must not count as an effective search")
	}
}

func TestActionChangedPageAcceptsUpdatedJobList(t *testing.T) {
	before := &browser.ExploreSnapshot{CurrentURL: "https://example.com/jobs", TextSample: "在招职位 126 个"}
	after := &browser.ExploreSnapshot{CurrentURL: "https://example.com/jobs", TextSample: "在招职位 2 个 算法工程师"}
	if !actionChangedPage(before, after) {
		t.Fatal("updated job list must count as an effective action")
	}
}

func TestBrowserPlanFromTraceSkipsEphemeralRefs(t *testing.T) {
	plan := browserPlanFromTrace([]ExploreStepTrace{
		{Action: "click", TargetRef: "e12", Target: "校园招聘", Result: "已点击"},
		{Action: "navigate", Target: "https://example.com/jobs", Result: "已打开"},
		{Action: "search", Target: "算法", Result: "已搜索"},
	})
	actions, ok := plan["actions"].([]any)
	if !ok || len(actions) != 2 {
		t.Fatalf("actions = %#v, want navigate + search", plan["actions"])
	}
	first, _ := actions[0].(map[string]any)
	second, _ := actions[1].(map[string]any)
	if first["type"] != "navigate" || second["type"] != "search" {
		t.Fatalf("unexpected plan: %#v", actions)
	}
}

func TestHasObservedNonEmptyListResponseMatchesSchema(t *testing.T) {
	cand := &ai.RecipeCandidate{
		ListAPI:    "https://example.com/api/jobs",
		ListPath:   "body.items",
		TitleField: "positionName",
	}
	observed := []ai.ExploreRequestView{{
		URL:           "https://example.com/api/jobs",
		SchemaSummary: "$: object{body}\n$.body.items: array[3] item{publishId, positionName}",
	}}

	if !hasObservedNonEmptyListResponse(cand, observed) {
		t.Fatal("expected non-empty observed list response")
	}
}

func TestHasObservedNonEmptyListResponseRejectsEmptyArray(t *testing.T) {
	cand := &ai.RecipeCandidate{
		ListAPI:    "https://example.com/api/jobs",
		ListPath:   "body.items",
		TitleField: "positionName",
	}
	observed := []ai.ExploreRequestView{{
		URL:           "https://example.com/api/jobs",
		SchemaSummary: "$: object{body}\n$.body.items: array[0] item=undefined",
	}}

	if hasObservedNonEmptyListResponse(cand, observed) {
		t.Fatal("expected empty observed list response to be rejected")
	}
}

func TestJobsFromObservedResponseUsesPagePayload(t *testing.T) {
	cand := &ai.RecipeCandidate{
		ListAPI:    "https://example.com/api/jobs?search=算法",
		ListPath:   "data.items",
		TitleField: "name",
		FieldMap:   map[string]string{"description": "workContent"},
	}
	draft := &site.Recipe{
		ListAPI: cand.ListAPI, ListPath: cand.ListPath, TitleField: cand.TitleField,
		FieldMap: map[string]any{"description": "workContent"},
	}
	jobs, err := jobsFromObservedResponse(draft, cand, []ai.ExploreRequestView{{
		URL:        "https://example.com/api/jobs?search=算法&nonce=current",
		FullSample: `{"data":{"items":[{"name":"算法工程师","workContent":"负责模型训练"}]}}`,
	}}, nil)
	if err != nil || len(jobs) != 1 || jobs[0].Title != "算法工程师" {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
}
