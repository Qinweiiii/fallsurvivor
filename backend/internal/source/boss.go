package source

import (
	"context"
	"fmt"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// BossSource 发现 BOSS 直聘上的岗位。
//
// 重要边界（依据设计文档第 6 节与第 16 节）：
//   - 不做登录态爬取，不绕过验证码，不绕过反爬；
//   - 第一版仅通过搜索引擎发现 BOSS 岗位 URL；
//   - 真实的 JD 内容由用户点击「查看原网页」自行浏览，
//     或在投递阶段由用户登录后的浏览器会话读取。
type BossSource struct {
	backend JobSource
}

// NewBossSource 创建 BOSS 来源。
func NewBossSource(backend JobSource) *BossSource {
	return &BossSource{backend: backend}
}

// Type 实现 JobSource。
func (s *BossSource) Type() string { return model.SourceBoss }

// Name 实现 JobSource。
func (s *BossSource) Name() string { return "BOSS直聘" }

// Available 实现 JobSource。
func (s *BossSource) Available() bool {
	return s.backend != nil && s.backend.Available()
}

// Search 实现 JobSource。
func (s *BossSource) Search(ctx context.Context, q SearchQuery) ([]RawJob, error) {
	if !s.Available() {
		return nil, fmt.Errorf("boss: 底层搜索能力不可用")
	}

	queries := buildBossQueries(q)
	if len(queries) == 0 {
		return nil, nil
	}

	raws, err := s.backend.Search(ctx, SearchQuery{
		Queries:            queries,
		Locations:          q.Locations,
		CompanyPreferences: q.CompanyPreferences,
		GraduationYear:     q.GraduationYear,
		MaxResultsPerQuery: 8,
	})
	if err != nil {
		return nil, err
	}

	out := make([]RawJob, 0, len(raws))
	for _, r := range raws {
		host := strings.ToLower(extractHost(r.URL))
		if !isBossHost(host) {
			continue
		}
		r.SourceType = model.SourceBoss
		r.SourceName = "BOSS直聘"
		out = append(out, r)
	}
	return out, nil
}

func buildBossQueries(q SearchQuery) []string {
	unknownCompanies := unknownOfficialCompanies(q.CompanyPreferences, DefaultCompanyAdapters)
	hasSpecific := len(specificCompanyPrefs(q.CompanyPreferences)) > 0
	hasBroad := hasBroadCompanyPref(q.CompanyPreferences)

	queries := make([]string, 0, 8)
	if len(unknownCompanies) > 0 {
		for _, company := range unknownCompanies {
			for _, base := range q.Queries {
				queries = append(queries, company+" "+base+" site:zhipin.com")
				if len(queries) >= 8 {
					return queries
				}
			}
		}
		return queries
	}

	if hasSpecific && !hasBroad {
		return nil
	}

	for _, base := range q.Queries {
		queries = append(queries, base+" site:zhipin.com")
		if len(queries) >= 8 {
			break
		}
	}
	if len(queries) == 0 {
		return nil
	}
	return queries
}

// isBossHost 判断是否为 BOSS 直聘域名。
func isBossHost(host string) bool {
	for _, d := range bossDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}
