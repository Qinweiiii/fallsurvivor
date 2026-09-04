package source

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// CompanyAdapter 是单家公司官方招聘站的适配器。
//
// 第一版通过「站内定向搜索」实现，不做爬虫绕过。
// 后续可以为某家公司实现真正的官方接口调用，只需替换 Adapter。
type CompanyAdapter interface {
	// Company 返回公司名。
	Company() string
	// Domains 返回该公司招聘站域名，用于定向搜索与链接识别。
	Domains() []string
	// BuildQueries 依据画像生成针对该公司的检索式。
	BuildQueries(roles []string, locations []string, graduationYear int) []string
}

// genericAdapter 是通过定向搜索实现的通用公司适配器。
type genericAdapter struct {
	company string
	domains []string
}

func (a genericAdapter) Company() string   { return a.company }
func (a genericAdapter) Domains() []string { return a.domains }

// BuildQueries 生成「site 限定 + 岗位方向 + 届次」的检索式。
func (a genericAdapter) BuildQueries(roles, locations []string, graduationYear int) []string {
	if len(roles) == 0 {
		roles = []string{"后端开发"}
	}
	year := ""
	if graduationYear > 0 {
		year = fmt.Sprintf("%d届", graduationYear)
	}

	site := ""
	if len(a.domains) > 0 {
		site = "site:" + a.domains[0]
	}

	out := make([]string, 0, len(roles)+1)
	for _, role := range roles {
		parts := []string{a.company, "校园招聘", role, year, site}
		out = append(out, joinNonEmpty(parts, " "))
	}
	// 附加一条城市定向检索式。
	if len(locations) > 0 {
		parts := []string{a.company, "校园招聘", roles[0], locations[0], year, site}
		out = append(out, joinNonEmpty(parts, " "))
	}
	return out
}

// DefaultCompanyAdapters 是第一版内置的公司列表。
// 只覆盖高优先级大厂 / 外企，后续按需扩展。
var DefaultCompanyAdapters = []CompanyAdapter{
	genericAdapter{company: "腾讯", domains: []string{"join.qq.com", "careers.tencent.com"}},
	genericAdapter{company: "字节跳动", domains: []string{"jobs.bytedance.com"}},
	genericAdapter{company: "阿里巴巴", domains: []string{"talent.alibaba.com"}},
	genericAdapter{company: "华为", domains: []string{"career.huawei.com"}},
	genericAdapter{company: "美团", domains: []string{"zhaopin.meituan.com"}},
	genericAdapter{company: "百度", domains: []string{"talent.baidu.com"}},
	genericAdapter{company: "网易", domains: []string{"campus.163.com"}},
	genericAdapter{company: "京东", domains: []string{"campus.jd.com"}},
	genericAdapter{company: "小米", domains: []string{"hr.xiaomi.com"}},
	genericAdapter{company: "OPPO", domains: []string{"careers.oppo.com"}},
	genericAdapter{company: "vivo", domains: []string{"campus.vivo.com"}},
	genericAdapter{company: "大疆", domains: []string{"we.dji.com"}},
	genericAdapter{company: "微软", domains: []string{"jobs.careers.microsoft.com"}},
	genericAdapter{company: "亚马逊", domains: []string{"amazon.jobs"}},
	genericAdapter{company: "英伟达", domains: []string{"nvidia.wd5.myworkdayjobs.com"}},
	genericAdapter{company: "商汤", domains: []string{"sensetime.jobs.feishu.cn"}},
	genericAdapter{company: "月之暗面", domains: []string{"moonshot.cn"}},
	genericAdapter{company: "深度求索", domains: []string{"deepseek.com"}},
}

// OfficialSource 通过定向搜索发现公司官方招聘页。
//
// 它复用一个 searchBackend（第一版即 Tavily），
// 但会把结果标记为 OFFICIAL 来源，以便在界面上区分优先级。
type OfficialSource struct {
	backend  JobSource
	adapters []CompanyAdapter
	// maxCompanies 限制单次任务覆盖的公司数量，控制搜索成本。
	maxCompanies int
}

// NewOfficialSource 创建官方来源。
func NewOfficialSource(backend JobSource, adapters []CompanyAdapter, maxCompanies int) *OfficialSource {
	if len(adapters) == 0 {
		adapters = DefaultCompanyAdapters
	}
	if maxCompanies <= 0 {
		maxCompanies = 6
	}
	return &OfficialSource{backend: backend, adapters: adapters, maxCompanies: maxCompanies}
}

// Type 实现 JobSource。
func (s *OfficialSource) Type() string { return model.SourceOfficial }

// Name 实现 JobSource。
func (s *OfficialSource) Name() string { return "公司官方招聘站" }

// Available 实现 JobSource。
func (s *OfficialSource) Available() bool {
	return s.backend != nil && s.backend.Available()
}

// Search 实现 JobSource。
func (s *OfficialSource) Search(ctx context.Context, q SearchQuery) ([]RawJob, error) {
	if !s.Available() {
		return nil, fmt.Errorf("official: 底层搜索能力不可用")
	}

	// 从检索式里推断岗位方向关键词。
	roles := extractRoleHints(q.Queries)

	adapters := s.selectAdapters(q.CompanyPreferences)
	if len(adapters) == 0 {
		slog.Debug("official: 无匹配的官方公司偏好，跳过官网来源")
		return nil, nil
	}

	var (
		queries     []string
		domainOwner = map[string]string{}
	)
	for _, a := range adapters {
		queries = append(queries, a.BuildQueries(roles, q.Locations, q.GraduationYear)...)
		for _, d := range a.Domains() {
			domainOwner[strings.ToLower(d)] = a.Company()
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}

	sub := SearchQuery{
		Queries:            queries,
		Locations:          q.Locations,
		CompanyPreferences: q.CompanyPreferences,
		GraduationYear:     q.GraduationYear,
		MaxResultsPerQuery: 5,
	}

	raws, err := s.backend.Search(ctx, sub)
	if err != nil {
		return nil, err
	}

	out := make([]RawJob, 0, len(raws))
	for _, r := range raws {
		host := strings.ToLower(extractHost(r.URL))
		company, matched := matchDomainOwner(domainOwner, host)
		if !matched {
			// 不是官方域名的结果交给 Tavily 来源处理，这里丢弃避免重复计数。
			continue
		}
		r.SourceType = model.SourceOfficial
		r.SourceName = company + " 招聘官网"
		if r.CompanyHint == "" {
			r.CompanyHint = company
		}
		out = append(out, r)
	}

	slog.Debug("official: 定向搜索完成", "queries", len(queries), "hits", len(out))
	return out, nil
}

func (s *OfficialSource) selectAdapters(prefs []string) []CompanyAdapter {
	specific := specificCompanyPrefs(prefs)
	hasBroad := hasBroadCompanyPref(prefs)

	var out []CompanyAdapter
	if len(specific) > 0 {
		for _, a := range s.adapters {
			if adapterMatchesAny(a, specific) {
				out = append(out, a)
			}
		}
	} else if hasBroad || len(prefs) == 0 {
		out = append(out, s.adapters...)
	}

	if len(out) > s.maxCompanies {
		out = out[:s.maxCompanies]
	}
	return out
}

// matchDomainOwner 判断 host 是否属于已登记的公司域名。
func matchDomainOwner(owner map[string]string, host string) (string, bool) {
	if host == "" {
		return "", false
	}
	for domain, company := range owner {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return company, true
		}
	}
	return "", false
}

// roleKeywords 用于从检索式中提取岗位方向。
var roleKeywords = []string{
	"后端", "AI后端", "AI 后端", "AI全栈", "AI 全栈", "Agent",
	"算法", "Infra", "基础架构", "服务端", "全栈", "大模型", "机器学习",
}

// extractRoleHints 从已生成的检索式中提取岗位方向关键词。
func extractRoleHints(queries []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, q := range queries {
		for _, kw := range roleKeywords {
			if strings.Contains(q, kw) && !seen[kw] {
				seen[kw] = true
				out = append(out, kw)
			}
		}
	}
	if len(out) == 0 {
		out = []string{"后端开发"}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// joinNonEmpty 拼接非空片段。
func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, sep)
}
