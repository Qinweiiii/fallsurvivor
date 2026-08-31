package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// QueryGeneratorInput 是搜索词生成的输入。
type QueryGeneratorInput struct {
	TargetRoles        []string `json:"target_roles"`
	PreferredLanguages []string `json:"preferred_languages"`
	Locations          []string `json:"locations"`
	CompanyPreferences []string `json:"company_preferences"`
	GraduationYear     int      `json:"graduation_year"`
	// ResumeHighlights 是简历要点，用于生成更贴合经历的关键词。
	ResumeHighlights []string `json:"resume_highlights,omitempty"`
}

// QueryPlan 是模型返回的搜索策略。
type QueryPlan struct {
	Queries []string `json:"queries"`
	// Rationale 说明策略思路，仅用于调试展示。
	Rationale string `json:"rationale"`
}

const queryGeneratorSystemPrompt = `你是一名资深校园招聘信息检索专家。
任务：根据用户的求职画像，生成一组用于搜索引擎的中文检索式，用来发现校园招聘岗位。

要求：
1. 生成 1 条检索式，使用覆盖面最广、最可能命中目标岗位的单一检索式（合并岗位方向、城市、届次），不要生成多条。
2. 必须包含毕业届次信息（如 2027 / 27届 / 2027届校招）。
3. 应包含面向公司官方招聘站的检索式（例如 "公司名 校园招聘 官网"）。
4. 检索式要像人类在搜索引擎里真实输入的短语，不要写成 SQL 或布尔表达式。
5. 不要编造公司名以外的虚假信息。
6. 只输出 JSON，不要输出解释文字。

输出 JSON 格式：
{"queries":["...","..."],"rationale":"一句话说明策略"}`

// GenerateQueries 生成搜索策略。
func (c *Client) GenerateQueries(ctx context.Context, in QueryGeneratorInput) (*QueryPlan, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	userPrompt := "用户求职画像如下（JSON）：\n" + string(payload)

	var out QueryPlan
	if err := c.completeJSON(ctx, queryGeneratorSystemPrompt, userPrompt, &out); err != nil {
		return nil, err
	}

	out.Queries = dedupNonEmpty(out.Queries, 1)
	if len(out.Queries) == 0 {
		return nil, fmt.Errorf("ai: 模型未生成任何检索式")
	}
	return &out, nil
}

// dedupNonEmpty 去空去重并限制数量。
func dedupNonEmpty(in []string, limit int) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
		if len(out) >= limit {
			break
		}
	}
	return out
}
