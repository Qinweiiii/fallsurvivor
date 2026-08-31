package ai

import (
	"context"
	"encoding/json"
	"strings"
)

// MatchInput 是岗位匹配的输入。
type MatchInput struct {
	Job     MatchJobBrief `json:"job"`
	Profile MatchProfile  `json:"profile"`
	// ResumeSummary 是简历要点摘要，不含手机号、邮箱等联系方式。
	ResumeSummary string `json:"resume_summary,omitempty"`
	// RuleScore 是代码计算出的规则分，供模型参考但不得直接照抄。
	RuleScore int `json:"rule_score"`
}

// MatchJobBrief 是送入模型的岗位摘要。
type MatchJobBrief struct {
	CompanyName    string   `json:"company_name"`
	Department     string   `json:"department"`
	Title          string   `json:"title"`
	Locations      []string `json:"locations"`
	JobType        string   `json:"job_type"`
	GraduationYear int      `json:"graduation_year"`
	Requirements   []string `json:"requirements"`
	TechnicalStack []string `json:"technical_stack"`
	Description    string   `json:"description"`
}

// MatchProfile 是送入模型的求职画像。
type MatchProfile struct {
	TargetRoles        []string `json:"target_roles"`
	PreferredLanguages []string `json:"preferred_languages"`
	PreferredLocations []string `json:"preferred_locations"`
	CompanyPreferences []string `json:"company_preferences"`
	GraduationYear     int      `json:"graduation_year"`
}

// MatchResult 是模型给出的匹配结论。
type MatchResult struct {
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
	Risks   []string `json:"risks"`
	Summary string   `json:"summary"`
}

const matcherSystemPrompt = `你是一名资深校招求职顾问，负责判断岗位与候选人的匹配程度。

评分维度：
1. 工作内容与候选人目标方向是否一致；
2. 岗位技术栈与候选人技术语言偏好是否一致；
3. 候选人简历经历能否支撑该岗位要求；
4. 工作地点是否符合候选人城市偏好；
5. 岗位的成长方向是否符合候选人规划。

规则：
- score 取 0 到 100 的整数。
- reasons 列出 2 到 5 条匹配理由，每条不超过 20 个字，要具体（例如"Go 技术栈高度匹配"），不要空话。
- risks 列出 0 到 3 条需要注意的短板，每条不超过 20 个字。若确实没有短板则为空数组。
- summary 一句话总结，不超过 40 个字。
- 输入中的 rule_score 是程序计算的规则分，你可以参考，但要独立判断。
- 若岗位主语言与候选人偏好完全不同（例如岗位仅要求 Java 而候选人偏好 Go），应显著降低分数，但不要低于 30，因为候选人仍可能愿意投递。
- 若岗位明显不是校园招聘或届次不符，分数应低于 40。
- 严禁编造岗位中不存在的信息。
- 只输出 JSON，不要输出解释。

输出 JSON 格式：
{"score":0,"reasons":[],"risks":[],"summary":""}`

// MatchJob 计算岗位与画像的语义匹配度。
func (c *Client) MatchJob(ctx context.Context, in MatchInput) (*MatchResult, error) {
	// 主链路优先：完整 JD 送进 LLM，不再截断。
	_ = in

	payload, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}

	var out MatchResult
	if err := c.completeJSON(ctx, matcherSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}

	// 边界修正：不信任模型返回的范围。
	if out.Score < 0 {
		out.Score = 0
	}
	if out.Score > 100 {
		out.Score = 100
	}
	out.Reasons = dedupNonEmpty(out.Reasons, 5)
	out.Risks = dedupNonEmpty(out.Risks, 3)
	out.Summary = strings.TrimSpace(out.Summary)
	return &out, nil
}
