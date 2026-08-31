package search

import "testing"

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"去掉追踪参数", "https://jobs.example.com/p/123?utm_source=wx&id=9", "https://jobs.example.com/p/123?id=9"},
		{"去掉 www 与 fragment", "https://WWW.Example.com/Job/1#apply", "https://example.com/Job/1"},
		{"去掉默认端口与尾斜杠", "https://example.com:443/job/1/", "https://example.com/job/1"},
		{"参数排序稳定", "https://example.com/j?b=2&a=1", "https://example.com/j?a=1&b=2"},
		{"非法输入返回空", "not a url", ""},
		{"空输入返回空", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeURL(c.in); got != c.want {
				t.Errorf("NormalizeURL(%q) = %q, 期望 %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeURLIdempotent(t *testing.T) {
	in := "https://WWW.Example.com/job/1/?utm_medium=x&z=1#frag"
	once := NormalizeURL(in)
	twice := NormalizeURL(once)
	if once != twice {
		t.Errorf("规范化不幂等: %q vs %q", once, twice)
	}
}

func TestNormalizeCompany(t *testing.T) {
	cases := map[string]string{
		// 括号内的地域修饰与公司后缀都应被剔除，使其与简称一致。
		"腾讯科技（深圳）有限公司":      "腾讯",
		"腾讯":                "腾讯",
		"字节跳动有限公司":          "字节跳动",
		"北京字节跳动网络技术有限公司":    "北京字节跳动",
		"Example Co., Ltd.": "example",
		"":                  "",
	}
	for in, want := range cases {
		if got := NormalizeCompany(in); got != want {
			t.Errorf("NormalizeCompany(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestNormalizeCompanyIdempotent(t *testing.T) {
	in := "腾讯科技（深圳）有限公司"
	once := NormalizeCompany(in)
	if twice := NormalizeCompany(once); once != twice {
		t.Errorf("公司名清洗不幂等: %q vs %q", once, twice)
	}
}

func TestNormalizeCity(t *testing.T) {
	cases := map[string]string{
		"深圳市南山区": "深圳",
		"深圳":     "深圳",
		"深圳·南山":  "深圳",
		"深圳 南山区": "深圳",
		"广州市":    "广州",
		"广东省":    "广东",
		"":       "",
	}
	for in, want := range cases {
		if got := NormalizeCity(in); got != want {
			t.Errorf("NormalizeCity(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint("腾讯科技（深圳）有限公司", "后端开发工程师（校招）", "深圳市南山区")
	b := Fingerprint("腾讯", "后端开发工程师", "深圳")
	if a != b {
		t.Errorf("同一岗位的不同写法应产生相同指纹: %q vs %q", a, b)
	}

	if Fingerprint("", "后端", "深圳") != "" {
		t.Error("公司名缺失时应返回空指纹")
	}
	if Fingerprint("腾讯", "", "深圳") != "" {
		t.Error("岗位名缺失时应返回空指纹")
	}
}

func TestLooksLikeNoise(t *testing.T) {
	if !LooksLikeNoise("腾讯后端面经分享") {
		t.Error("面经类内容应被判定为噪声")
	}
	if LooksLikeNoise("后端开发工程师 - 腾讯招聘") {
		t.Error("正常岗位标题不应被判定为噪声")
	}
}

func TestLooksLikeInternship(t *testing.T) {
	cases := []struct {
		title, desc string
		want        bool
	}{
		{"后端开发实习生-豆包", "面向2027届毕业生", true},
		{"前端开发实习生", "", true},
		{"Backend Intern", "Summer internship program", true},
		{"后端开发工程师", "负责核心系统设计", false},
		{"高级Go工程师", "3年以上经验", false},
		{"Java开发", "实习生优先", true}, // 描述中含实习关键词
	}
	for _, c := range cases {
		t.Run(c.title, func(t *testing.T) {
			if got := LooksLikeInternship(c.title, c.desc); got != c.want {
				t.Errorf("LooksLikeInternship(%q, %q) = %v, 期望 %v", c.title, c.desc, got, c.want)
			}
		})
	}
}

func TestStripHTML(t *testing.T) {
	html := `<div><script>alert(1)</script><p>岗位职责</p><ul><li>负责后端开发</li></ul>&nbsp;结束</div>`
	got := StripHTML(html)
	if contains(got, "alert") {
		t.Errorf("script 内容未被剥离: %q", got)
	}
	if !contains(got, "岗位职责") || !contains(got, "负责后端开发") {
		t.Errorf("正文内容丢失: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
