package handler

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

func TestDetectSiteKey(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://careers.tencent.com/campus.html", "tencent"},
		{"https://join.qq.com/x", "tencent"},
		{"https://jobs.bytedance.com/campus", "bytedance"},
		{"https://www.zhipin.com/job_detail", "boss"},
		{"https://example.com/careers", "generic"},
		{"not-a-url", "generic"},
	}
	for _, c := range cases {
		if got := detectSiteKey(c.url); got != c.want {
			t.Errorf("detectSiteKey(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestJobsFromRawUsesCleanMetaDepartment(t *testing.T) {
	jobs := jobsFromRaw([]source.RawJob{
		{
			Title: "算法工程师",
			URL:   "https://jobs.example.com/1",
			Meta:  map[string]string{"Department": "平台部", "Business": "算法业务"},
		},
		{
			Title: "后端工程师",
			URL:   "https://jobs.example.com/2",
			Meta:  map[string]string{"Department": "<nil>", "Business": "基础架构"},
		},
	})

	if len(jobs) != 2 {
		t.Fatalf("jobs len = %d, want 2", len(jobs))
	}
	if jobs[0].Department != "平台部" {
		t.Fatalf("department = %q, want 平台部", jobs[0].Department)
	}
	if jobs[1].Department != "基础架构" {
		t.Fatalf("business fallback = %q, want 基础架构", jobs[1].Department)
	}
}

func TestExploreRouting(t *testing.T) {
	if !shouldExploreCrawl("explore", false) {
		t.Fatalf("strategy=explore should force exploration")
	}
	if !shouldExploreCrawl("ai", false) {
		t.Fatalf("strategy=ai should use the exploration alias")
	}
	if !shouldExploreCrawl("", true) {
		t.Fatalf("save_as_recipe should force exploration")
	}
	if shouldExploreCrawl("", false) {
		t.Fatalf("default crawl should try Fast Path before exploration")
	}
	if !shouldAutoExplore("meituan", "") {
		t.Fatalf("unknown/non-hardcoded site should auto explore only after Fast Path miss")
	}
	if !shouldAutoExplore("tencent", "") {
		t.Fatalf("tencent should enter Explorer after Fast Path miss")
	}
	if !shouldAutoExplore("bytedance", "") {
		t.Fatalf("bytedance should enter Explorer after Fast Path miss")
	}
	if shouldAutoExplore("boss", "") {
		t.Fatalf("boss should keep its current visual path")
	}
	if shouldAutoExplore("meituan", "api") {
		t.Fatalf("explicit non-auto strategy should not auto explore")
	}
}

func TestJobsFromTitles(t *testing.T) {
	jobs := jobsFromTitles([]string{"算法工程师", " ", "后端工程师"})
	if len(jobs) != 2 {
		t.Fatalf("jobs len = %d, want 2", len(jobs))
	}
	if jobs[0].Title != "算法工程师" || jobs[1].Title != "后端工程师" {
		t.Fatalf("jobs = %#v", jobs)
	}
}
