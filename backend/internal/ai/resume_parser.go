package ai

import (
	"context"
	"encoding/json"
	"strings"
)

// ResumeProfile 是简历结构化结果。
type ResumeProfile struct {
	Education   []ResumeEducation  `json:"education"`
	Internships []ResumeExperience `json:"internships"`
	Projects    []ResumeProject    `json:"projects"`
	Skills      []string           `json:"skills"`
	Awards      []string           `json:"awards"`
	Languages   []string           `json:"languages"`
	Highlights  []string           `json:"highlights"`
	Summary     string             `json:"summary"`
}

// ResumeEducation 是教育经历。
type ResumeEducation struct {
	School         string `json:"school"`
	Major          string `json:"major"`
	Degree         string `json:"degree"`
	GraduationYear int    `json:"graduation_year"`
	StartDate      string `json:"start_date"`
	EndDate        string `json:"end_date"`
	GPA            string `json:"gpa"`
}

// ResumeExperience 是实习/工作经历。
type ResumeExperience struct {
	Company    string   `json:"company"`
	Department string   `json:"department"`
	Role       string   `json:"role"`
	StartDate  string   `json:"start_date"`
	EndDate    string   `json:"end_date"`
	Highlights []string `json:"highlights"`
	TechStack  []string `json:"tech_stack"`
}

// ResumeProject 是项目经历。
type ResumeProject struct {
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Highlights  []string `json:"highlights"`
	TechStack   []string `json:"tech_stack"`
}

const resumeParserSystemPrompt = `你是一名严谨的简历结构化解析器。
任务：把简历纯文本转成结构化 JSON。

铁律：
1. 只抽取文本中真实存在的内容，不推测、不编造、不补全。
2. 严禁在输出中包含任何联系方式或身份信息：手机号、邮箱、微信、身份证号、住址一律不要输出。
3. highlights 用原文中的成果描述，尽量保留量化数据。
4. skills 只填技术关键词与编程语言。
5. summary 用一句话概括候选人，不超过 60 字。
6. 未出现的字段留空（字符串 ""，数组 []，数字 0）。
7. 只输出 JSON，不要输出解释。

输出 JSON 格式：
{"education":[{"school":"","major":"","degree":"","graduation_year":0,"start_date":"","end_date":"","gpa":""}],"internships":[{"company":"","department":"","role":"","start_date":"","end_date":"","highlights":[],"tech_stack":[]}],"projects":[{"name":"","role":"","description":"","highlights":[],"tech_stack":[]}],"skills":[],"awards":[],"languages":[],"highlights":[],"summary":""}`

// ParseResume 解析简历文本。
func (c *Client) ParseResume(ctx context.Context, rawText string) (*ResumeProfile, error) {
	var out ResumeProfile
	if err := c.completeJSON(ctx, resumeParserSystemPrompt, "简历文本：\n"+rawText, &out); err != nil {
		return nil, err
	}

	out.Skills = dedupNonEmpty(out.Skills, 40)
	out.Awards = dedupNonEmpty(out.Awards, 20)
	out.Languages = dedupNonEmpty(out.Languages, 15)
	out.Highlights = dedupNonEmpty(out.Highlights, 15)
	out.Summary = strings.TrimSpace(out.Summary)
	return &out, nil
}

// Brief 生成用于匹配的简历摘要文本（已不含联系方式）。
func (p *ResumeProfile) Brief() string {
	if p == nil {
		return ""
	}
	var sb strings.Builder
	if p.Summary != "" {
		sb.WriteString(p.Summary)
		sb.WriteString("\n")
	}
	for _, e := range p.Education {
		if e.School == "" {
			continue
		}
		sb.WriteString("教育：")
		sb.WriteString(e.School)
		if e.Major != "" {
			sb.WriteString(" / " + e.Major)
		}
		if e.Degree != "" {
			sb.WriteString(" / " + e.Degree)
		}
		sb.WriteString("\n")
	}
	for _, x := range p.Internships {
		if x.Company == "" {
			continue
		}
		sb.WriteString("实习：" + x.Company)
		if x.Role != "" {
			sb.WriteString(" - " + x.Role)
		}
		if len(x.TechStack) > 0 {
			sb.WriteString("（" + strings.Join(x.TechStack, ", ") + "）")
		}
		sb.WriteString("\n")
	}
	for _, x := range p.Projects {
		if x.Name == "" {
			continue
		}
		sb.WriteString("项目：" + x.Name)
		if len(x.TechStack) > 0 {
			sb.WriteString("（" + strings.Join(x.TechStack, ", ") + "）")
		}
		sb.WriteString("\n")
	}
	if len(p.Skills) > 0 {
		sb.WriteString("技能：" + strings.Join(p.Skills, ", ") + "\n")
	}
	return truncateRunes(sb.String(), 2000)
}

// HighlightKeywords 提取用于生成搜索词的关键词。
func (p *ResumeProfile) HighlightKeywords(limit int) []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, limit)
	seen := map[string]bool{}
	push := func(items []string) {
		for _, s := range items {
			t := strings.TrimSpace(s)
			if t == "" || seen[strings.ToLower(t)] {
				continue
			}
			seen[strings.ToLower(t)] = true
			out = append(out, t)
		}
	}
	push(p.Skills)
	for _, x := range p.Projects {
		push(x.TechStack)
	}
	for _, x := range p.Internships {
		push(x.TechStack)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ToJSONMap 序列化为可写入 JSONB 列的字节串。
func (p *ResumeProfile) ToJSONMap() ([]byte, error) {
	if p == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(p)
}

// ParseResumeProfile 从 JSONB 反序列化为具体结构体。
func ParseResumeProfile(raw []byte) (*ResumeProfile, error) {
	if len(raw) == 0 {
		return &ResumeProfile{}, nil
	}
	var p ResumeProfile
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
