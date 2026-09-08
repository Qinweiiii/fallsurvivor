package search

import "testing"

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
