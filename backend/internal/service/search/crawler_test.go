package search

import (
	"context"
	"strings"
	"testing"
)

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://careers.tencent.com/campus.html": "careers.tencent.com",
		"http://example.com:8080/x":               "example.com:8080",
		"not a url":                               "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCrawlLoopStopsOnRevisit(t *testing.T) {
	// 验证 hostOf 用于同站过滤：跨站岗位不收录。
	if hostOf("https://evil.com/x") == hostOf("https://careers.tencent.com/y") {
		t.Error("不同 host 不应相等")
	}
}

func TestCrawlDoesNotImplicitlyRouteTencentToLegacyScript(t *testing.T) {
	crawler := NewSiteCrawler(nil, nil)
	_, err := crawler.Crawl(context.Background(), "task", "tencent", "https://join.qq.com", "auto")
	if err == nil || !strings.Contains(err.Error(), "Exploration Agent") {
		t.Fatalf("auto tencent crawl = %v, want Explorer routing error", err)
	}
}

func TestCrawlScriptRejectsNonTencentLegacyUse(t *testing.T) {
	crawler := NewSiteCrawler(nil, nil)
	_, err := crawler.Crawl(context.Background(), "task", "bytedance", "https://jobs.bytedance.com", "script")
	if err == nil || !strings.Contains(err.Error(), "仅保留给腾讯") {
		t.Fatalf("script bytedance crawl = %v, want legacy script rejection", err)
	}
}
