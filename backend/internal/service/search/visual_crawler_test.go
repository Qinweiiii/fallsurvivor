package search

import "testing"

func TestVisualAccessBlocked(t *testing.T) {
	if !visualAccessBlocked("https://www.zhipin.com/web/geek/jobs?_security_check=1", "") {
		t.Fatal("security-check URL must stop visual navigation")
	}
	if !visualAccessBlocked("https://example.com/jobs", "请登录后继续") {
		t.Fatal("login prompt must stop visual navigation")
	}
	if visualAccessBlocked("https://example.com/jobs/123", "岗位职责\n任职要求") {
		t.Fatal("ordinary detail page must not be considered blocked")
	}
}

func TestLooksLikeVisibleJobDetail(t *testing.T) {
	valid := "职位详情\n岗位职责\n负责后端系统开发。\n任职要求\n熟悉 Go 与分布式系统。"
	for len([]rune(valid)) < visualMinDetailRunes {
		valid += "补充岗位说明。"
	}
	if !looksLikeVisibleJobDetail(valid) {
		t.Fatal("complete job detail should be accepted")
	}
	if looksLikeVisibleJobDetail("职位详情\n岗位职责") {
		t.Fatal("short content must not be accepted")
	}
}

func TestVisualForbidden(t *testing.T) {
	if !visualForbidden("立即沟通") {
		t.Fatal("communication button must not be clicked")
	}
	if visualForbidden("后端开发工程师") {
		t.Fatal("job title should remain clickable")
	}
}
