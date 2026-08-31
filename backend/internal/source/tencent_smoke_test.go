package source

import (
	"context"
	"testing"
	"time"
)

// 冒烟测试：真实调用腾讯招聘公开接口，验证字段映射与可用性。
// 依赖外网，网络不可用时跳过（不因网络波动导致 CI 失败）。
func TestTencentSourceSmoke(t *testing.T) {
	s := NewTencentSource(nil)
	if !s.Available() {
		t.Fatal("腾讯数据源应恒为可用（公开接口无需凭据）")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	jobs, err := s.Search(ctx, SearchQuery{
		Queries:            []string{"Go 后端开发"},
		MaxResultsPerQuery: 5,
	})
	if err != nil {
		t.Skipf("跳过：网络不可用或接口异常: %v", err)
	}
	if len(jobs) == 0 {
		t.Skip("跳过：接口未返回岗位（可能是网络或接口变更）")
	}

	t.Logf("✅ 获取到 %d 个岗位", len(jobs))
	for i, j := range jobs {
		if i >= 3 {
			break
		}
		t.Logf("───── 岗位 %d ─────", i+1)
		t.Logf("  标题  : %s", j.Title)
		t.Logf("  公司  : %s", j.CompanyHint)
		t.Logf("  地点  : %s", j.LocationHint)
		t.Logf("  URL   : %s", j.URL)
		t.Logf("  来源  : %s / %s", j.SourceType, j.SourceName)
		t.Logf("  Content 前 200 字: %s", truncate(j.Content, 200))
	}

	// 关键断言：核心字段必须被正确填充。
	first := jobs[0]
	if first.Title == "" {
		t.Error("标题不应为空")
	}
	if first.URL == "" {
		t.Error("URL 不应为空")
	}
	if first.CompanyHint != "腾讯" {
		t.Errorf("CompanyHint 应为腾讯，实际 %q", first.CompanyHint)
	}
	if first.Content == "" {
		t.Error("Content 不应为空（需包含 JD 正文供后续解析）")
	}
}

// truncate 按 rune 截断，避免截断多字节字符。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
