package handler

import "testing"

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
