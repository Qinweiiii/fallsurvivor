package search

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

func TestPreferredSearchKeywordKeepsUserKeyword(t *testing.T) {
	got := preferredSearchKeyword(" 算法 ", "技术")
	if got != "算法" {
		t.Fatalf("preferredSearchKeyword = %q, want 算法", got)
	}
}

func TestPreferredSearchKeywordFallsBackToModelTarget(t *testing.T) {
	got := preferredSearchKeyword("", " 技术 ")
	if got != "技术" {
		t.Fatalf("preferredSearchKeyword fallback = %q, want 技术", got)
	}
}

func TestFirstPositiveIntParsesInspectTargetText(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want int
	}{
		{name: "plain", in: []string{"1"}, want: 1},
		{name: "with label", in: []string{"inspect seq 12"}, want: 12},
		{name: "fallback ref", in: []string{"", "req:3"}, want: 3},
		{name: "none", in: []string{"inspect latest", ""}, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstPositiveInt(tc.in...); got != tc.want {
				t.Fatalf("firstPositiveInt(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestInspectSeqIgnoresURLDigits(t *testing.T) {
	if got := inspectSeq("inspect sgm-w.jd.com/h5", "1"); got != 1 {
		t.Fatalf("inspectSeq URL digits = %d, want fallback ref 1", got)
	}
}

func TestInspectSeqParsesExplicitSeq(t *testing.T) {
	if got := inspectSeq("inspect seq 3", "1"); got != 3 {
		t.Fatalf("inspectSeq explicit seq = %d, want 3", got)
	}
}

func TestInspectSeqRejectsElementRef(t *testing.T) {
	if got := inspectSeq("", "el:11"); got != 0 {
		t.Fatalf("inspectSeq element ref = %d, want 0", got)
	}
}

func TestStrongestJobListSignalAcceptsVerifiedLookingRequest(t *testing.T) {
	reqs := []ai.ExploreRequestView{
		{
			Seq:         3,
			Method:      "POST",
			URL:         "https://example.com/api/position/page",
			Status:      200,
			Score:       92,
			RequestBody: `{"pageSize":10,"parameter":{"positionName":"算法"}}`,
			SchemaSummary: `body.items: array[10]
body.items[].positionName: string
body.items[].workContent: string
body.items[].qualification: string`,
		},
	}

	got := strongestJobListSignal(reqs, "算法")
	if got == nil || got.Seq != 3 {
		t.Fatalf("strongestJobListSignal = %#v, want seq 3", got)
	}
}

func TestStrongestJobListSignalRequiresKeywordWhenProvided(t *testing.T) {
	reqs := []ai.ExploreRequestView{
		{
			Seq:           3,
			Status:        200,
			Score:         92,
			RequestBody:   `{"pageSize":10,"parameter":{"positionName":"产品"}}`,
			SchemaSummary: `items: array[10] item{positionName, workContent, qualification}`,
		},
	}

	if got := strongestJobListSignal(reqs, "算法"); got != nil {
		t.Fatalf("strongestJobListSignal = %#v, want nil", got)
	}
}

func TestRecipeCandidateInputsPrefersKeywordBoundListRequest(t *testing.T) {
	reqs := []ai.ExploreRequestView{
		{
			Seq:           1,
			Status:        200,
			Score:         95,
			URL:           "https://example.com/api/position/page?type=present",
			SchemaSummary: `items: array[10] item{positionName, workContent, qualification}`,
		},
		{
			Seq:           2,
			Status:        200,
			Score:         90,
			RequestBody:   `{"pageSize":10,"keyword":"算法"}`,
			SchemaSummary: `items: array[10] item{positionName, workContent, qualification}`,
		},
	}

	got := recipeCandidateInputs(reqs, "算法")
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("recipeCandidateInputs = %#v, want only keyword-bound request", got)
	}
}

func TestStrongestJobListSignalRequiresContentSignal(t *testing.T) {
	reqs := []ai.ExploreRequestView{
		{
			Seq:           3,
			Status:        200,
			Score:         92,
			RequestBody:   `{"pageSize":10,"parameter":{"positionName":"算法"}}`,
			SchemaSummary: `items: array[10] item{positionName, cityName}`,
		},
	}

	if got := strongestJobListSignal(reqs, "算法"); got != nil {
		t.Fatalf("strongestJobListSignal = %#v, want nil", got)
	}
}

func TestObservedKeywordRequest(t *testing.T) {
	reqs := []ai.ExploreRequestView{
		{URL: "https://example.com/api/jobs", RequestBody: `{"keyword":"算法"}`},
	}
	if !observedKeywordRequest(reqs, "算法") {
		t.Fatalf("observedKeywordRequest should find keyword in request body")
	}
	if observedKeywordRequest(reqs, "产品") {
		t.Fatalf("observedKeywordRequest should not match unrelated keyword")
	}
}

func TestFirstUninspectedRequestSkipsSeenSeqs(t *testing.T) {
	candidates := []ai.ExploreRequestView{
		{Seq: 1},
		{Seq: 3},
	}
	got := firstUninspectedRequest(candidates, map[int]bool{1: true})
	if got != 3 {
		t.Fatalf("firstUninspectedRequest = %d, want 3", got)
	}
}

func TestSearchInputRefUsesRequestedSnapshotRef(t *testing.T) {
	elements := []ai.ExploreElementView{
		{Ref: "el:1", Text: "站内搜索", Tag: "input", Type: "text"},
		{Ref: "el:2", Text: "搜索职位或关键词", Tag: "input", Type: "search"},
	}
	if got := searchInputRef(elements, "el:2"); got != "el:2" {
		t.Fatalf("searchInputRef requested = %q, want el:2", got)
	}
	if got := searchInputRef(elements, "missing"); got != "el:2" {
		t.Fatalf("searchInputRef semantic fallback = %q, want el:2", got)
	}
}

func TestRecentActionHistoryKeepsPriorFailure(t *testing.T) {
	history := recentActionHistory([]ExploreStepTrace{
		{Step: 1, Action: "inspect", Target: "1", Result: "已读取请求 1"},
		{Step: 2, Action: "search", TargetRef: "el:5", Result: "未观测到页面或候选数据变化"},
	}, 6)
	if len(history) != 2 || !containsFold(history[1], "未观测到页面") {
		t.Fatalf("recentActionHistory = %#v, want prior failure retained", history)
	}
}

func TestRecipeQualityIssueRequiresCoreFields(t *testing.T) {
	fields := []executor.FieldQuality{
		{Field: "title", OK: true, Hit: 10, Total: 10},
		{Field: "department", OK: false, Hit: 0, Total: 10},
		{Field: "business", OK: false, Hit: 0, Total: 10},
	}
	if issue := recipeQualityIssue(fields); issue != "" {
		t.Fatalf("recipeQualityIssue = %q, want empty", issue)
	}
}

func TestRecipeQualityIssueAllowsMissingOptionalListFields(t *testing.T) {
	fields := []executor.FieldQuality{
		{Field: "title", OK: true, Hit: 10, Total: 10},
		{Field: "location", OK: false, Hit: 0, Total: 10},
	}
	if issue := recipeQualityIssue(fields); issue != "" {
		t.Fatalf("recipeQualityIssue = %q, want optional fields accepted", issue)
	}
}

func TestDetailTemplateFromURLRequiresExactListFieldValue(t *testing.T) {
	id, template, ok := detailTemplateFromURL(
		"https://example.com/post_detail.html?postid=199907820022",
		[]executor.JobIdentifier{{Field: "id", Value: "1"}, {Field: "postId", Value: "199907820022"}},
	)
	if !ok || id.Field != "postId" || template != "https://example.com/post_detail.html?postid={id}" {
		t.Fatalf("id=%#v template=%q ok=%v", id, template, ok)
	}
}

func TestMergeDetailContentsMatchesVerifiedDetailURL(t *testing.T) {
	jobs := mergeDetailContents([]source.RawJob{{URL: "https://example.com/post?id=7"}}, map[string]string{
		"https://example.com/post?id=7": "岗位职责：负责模型训练与上线。",
	})
	if jobs[0].Content == "" {
		t.Fatal("expected verified detail content to be merged")
	}
}
