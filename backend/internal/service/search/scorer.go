package search

import (
	"strings"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// 规则分各维度权重，合计 100。
const (
	weightRole     = 30 // 岗位方向
	weightLanguage = 25 // 技术语言
	weightCity     = 20 // 城市
	weightYear     = 15 // 毕业届次
	weightCompany  = 10 // 公司偏好
)

// 规则分与 LLM 分的融合权重。
const (
	ruleWeight = 0.4
	llmWeight  = 0.6
)

// ScoreInput 是评分所需的输入。
type ScoreInput struct {
	Job     *model.Job
	Profile *model.JobProfile
	// CityScores 由画像的城市顺序生成。
	CityScores map[string]int
}

// RuleScoreDetail 是规则分的分项明细。
type RuleScoreDetail struct {
	Total    int      `json:"total"`
	Role     int      `json:"role"`
	Language int      `json:"language"`
	City     int      `json:"city"`
	Year     int      `json:"year"`
	Company  int      `json:"company"`
	Reasons  []string `json:"reasons"`
	Risks    []string `json:"risks"`
}

// bigTechCompanies 用于判断「大厂」偏好。
var bigTechCompanies = []string{
	"腾讯", "阿里", "字节", "百度", "美团", "京东", "网易", "华为", "小米",
	"拼多多", "滴滴", "快手", "shopee", "shein", "大疆", "oppo", "vivo",
	"蚂蚁", "菜鸟", "b站", "哔哩哔哩", "携程", "小红书",
}

// foreignCompanies 用于判断「外企」偏好。
var foreignCompanies = []string{
	"微软", "microsoft", "亚马逊", "amazon", "谷歌", "google", "苹果", "apple",
	"英伟达", "nvidia", "英特尔", "intel", "amd", "甲骨文", "oracle", "sap",
	"ibm", "西门子", "siemens", "摩根", "高盛", "彭博", "bloomberg",
	"citadel", "jane street", "optiver", "hudson river", "tiktok",
}

// aiCompanies 用于判断「AI 公司」偏好。
var aiCompanies = []string{
	"商汤", "旷视", "依图", "云从", "智谱", "月之暗面", "moonshot", "minimax",
	"深度求索", "deepseek", "百川", "零一万物", "阶跃", "stepfun",
	"openai", "anthropic", "面壁", "无问芯穹",
}

// ComputeRuleScore 计算纯规则分（0~100）。
//
// 这是确定性逻辑，不依赖 LLM，因此 LLM 不可用时系统仍能给出可用的排序。
func ComputeRuleScore(in ScoreInput) RuleScoreDetail {
	d := RuleScoreDetail{}
	job, profile := in.Job, in.Profile
	if job == nil || profile == nil {
		return d
	}

	jobText := strings.ToLower(strings.Join([]string{
		job.Title, job.Department, job.Business, job.Description,
		strings.Join(job.Requirements, " "),
		strings.Join(job.TechnicalStack, " "),
	}, " "))

	// ---- 岗位方向 ----
	matchedRoles := make([]string, 0, len(profile.TargetRoles))
	for _, role := range profile.TargetRoles {
		r := strings.ToLower(strings.TrimSpace(role))
		if r == "" {
			continue
		}
		// 去掉空格后再比对，兼容「AI 后端」与「AI后端」。
		if strings.Contains(jobText, r) ||
			strings.Contains(strings.ReplaceAll(jobText, " ", ""), strings.ReplaceAll(r, " ", "")) {
			matchedRoles = append(matchedRoles, role)
		}
	}
	if len(matchedRoles) > 0 {
		d.Role = weightRole
		d.Reasons = append(d.Reasons, "岗位方向匹配："+strings.Join(matchedRoles, "、"))
	} else if strings.Contains(jobText, "后端") || strings.Contains(jobText, "服务端") ||
		strings.Contains(jobText, "backend") {
		// 泛后端岗位给部分分。
		d.Role = weightRole * 6 / 10
		d.Reasons = append(d.Reasons, "属于后端类岗位")
	} else {
		d.Risks = append(d.Risks, "岗位方向与目标方向差异较大")
	}

	// ---- 技术语言 ----
	// 画像中数组顺序即优先级，越靠前权重越高。
	langScore := 0
	matchedLangs := make([]string, 0, len(profile.PreferredLanguages))
	for i, lang := range profile.PreferredLanguages {
		l := strings.ToLower(strings.TrimSpace(lang))
		if l == "" {
			continue
		}
		if !containsLanguage(jobText, l) {
			continue
		}
		matchedLangs = append(matchedLangs, lang)
		// 首选语言拿满分，之后依次递减。
		switch i {
		case 0:
			langScore = weightLanguage
		case 1:
			if langScore < weightLanguage*8/10 {
				langScore = weightLanguage * 8 / 10
			}
		default:
			if langScore < weightLanguage*6/10 {
				langScore = weightLanguage * 6 / 10
			}
		}
	}
	if langScore > 0 {
		d.Language = langScore
		d.Reasons = append(d.Reasons, "技术语言匹配："+strings.Join(matchedLangs, "、"))
	} else if mentionsAnyLanguage(jobText) {
		// 岗位明确要求了其他语言：显著降分但不判定为不可投。
		d.Language = weightLanguage * 2 / 10
		d.Risks = append(d.Risks, "岗位主要语言与偏好语言不同")
	} else {
		// 岗位未提及具体语言，给中间分，避免误杀。
		d.Language = weightLanguage * 5 / 10
	}

	// ---- 城市 ----
	cityScores := in.CityScores
	if len(cityScores) == 0 {
		cityScores = config.BuildCityScores(profile.PreferredLocations)
	}
	bestCity := 0
	bestCityName := ""
	locations := job.Locations
	if len(locations) == 0 && job.Location != "" {
		locations = []string{job.Location}
	}
	for _, loc := range locations {
		s := config.ScoreForCity(cityScores, loc)
		if s > bestCity {
			bestCity = s
			bestCityName = loc
		}
	}
	if bestCity == 0 {
		bestCity = config.UnknownCityScore
	}
	d.City = weightCity * bestCity / 100
	switch {
	case bestCity >= 95:
		d.Reasons = append(d.Reasons, "工作地点符合首选城市："+bestCityName)
	case bestCity <= config.UnknownCityScore && bestCityName != "":
		d.Risks = append(d.Risks, "工作地点不在偏好城市列表："+bestCityName)
	}

	// ---- 毕业届次 ----
	switch {
	case job.GraduationYear == nil || *job.GraduationYear == 0:
		// 未标注届次，给中间分而非扣满，避免误杀信息不全的岗位。
		d.Year = weightYear * 6 / 10
	case *job.GraduationYear == profile.GraduationYear:
		d.Year = weightYear
		d.Reasons = append(d.Reasons, "招聘届次匹配")
	default:
		d.Year = 0
		d.Risks = append(d.Risks, "招聘届次不符")
	}

	// ---- 公司偏好 ----
	company := strings.ToLower(job.CompanyName)
	companyHit := ""
	for _, pref := range profile.CompanyPreferences {
		switch strings.TrimSpace(pref) {
		case "大厂":
			if matchAny(company, bigTechCompanies) {
				companyHit = "大厂"
			}
		case "外企":
			if matchAny(company, foreignCompanies) {
				companyHit = "外企"
			}
		case "AI公司", "AI 公司":
			if matchAny(company, aiCompanies) {
				companyHit = "AI 公司"
			}
		}
		if companyHit != "" {
			break
		}
	}
	if companyHit != "" {
		d.Company = weightCompany
		d.Reasons = append(d.Reasons, "公司类型符合偏好："+companyHit)
	} else {
		// 不在偏好列表也不扣成 0，用户并未限制只投大厂。
		d.Company = weightCompany * 5 / 10
	}

	d.Total = d.Role + d.Language + d.City + d.Year + d.Company
	if d.Total > 100 {
		d.Total = 100
	}
	if d.Total < 0 {
		d.Total = 0
	}
	return d
}

// languageAliases 处理语言名的常见写法差异。
var languageAliases = map[string][]string{
	"go":     {"go", "golang"},
	"python": {"python", "py"},
	"java":   {"java"},
	"c++":    {"c++", "cpp", "c＋＋"},
	"c":      {"c语言"},
	"rust":   {"rust"},
	"js":     {"javascript", "typescript", "node"},
}

// containsLanguage 判断岗位文本是否提到某语言。
func containsLanguage(jobText, lang string) bool {
	aliases, ok := languageAliases[lang]
	if !ok {
		aliases = []string{lang}
	}
	for _, a := range aliases {
		// Java 会误命中 JavaScript，需要额外排除。
		if a == "java" {
			if idx := strings.Index(jobText, "java"); idx >= 0 {
				rest := jobText[idx:]
				if !strings.HasPrefix(rest, "javascript") {
					return true
				}
				// 继续找下一个 java 出现位置。
				if strings.Count(jobText, "java") > strings.Count(jobText, "javascript") {
					return true
				}
			}
			continue
		}
		if strings.Contains(jobText, a) {
			return true
		}
	}
	return false
}

// allKnownLanguages 是用于判断「岗位是否提及了任何语言」的集合。
var allKnownLanguages = []string{
	"go", "golang", "python", "java", "c++", "cpp", "rust",
	"javascript", "typescript", "scala", "kotlin", "php", "ruby", "c#", ".net",
}

// mentionsAnyLanguage 判断岗位是否明确要求了某种编程语言。
func mentionsAnyLanguage(jobText string) bool {
	return matchAny(jobText, allKnownLanguages)
}

// matchAny 判断文本是否包含列表中的任一项。
func matchAny(text string, list []string) bool {
	for _, s := range list {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// FuseScores 融合规则分与 LLM 分。
// LLM 不可用时直接返回规则分。
func FuseScores(rule RuleScoreDetail, llm *ai.MatchResult) model.MatchAnalysis {
	out := model.MatchAnalysis{
		RuleScore: rule.Total,
		Reasons:   append([]string{}, rule.Reasons...),
		Risks:     append([]string{}, rule.Risks...),
		Analyzer:  "rule",
	}

	if llm == nil {
		out.Score = rule.Total
		out.LLMScore = 0
		return out
	}

	out.LLMScore = llm.Score
	out.Analyzer = "rule+llm"
	out.Summary = llm.Summary
	// LLM 的理由更贴近语义，放在前面。
	out.Reasons = dedupStrings(append(append([]string{}, llm.Reasons...), rule.Reasons...), 6)
	out.Risks = dedupStrings(append(append([]string{}, llm.Risks...), rule.Risks...), 4)

	fused := ruleWeight*float64(rule.Total) + llmWeight*float64(llm.Score)
	score := int(fused + 0.5)
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	out.Score = score
	return out
}

// dedupStrings 去重并限制条数。
func dedupStrings(in []string, limit int) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, limit)
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		k := strings.ToLower(t)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, t)
		if len(out) >= limit {
			break
		}
	}
	return out
}
