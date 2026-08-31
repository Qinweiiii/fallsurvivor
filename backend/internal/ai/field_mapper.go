package ai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/security"
)

// FormField 是 Playwright 从页面提取的表单字段元信息。
type FormField struct {
	// Ref 是 Worker 侧用于定位元素的不透明句柄（由 Worker 生成，后端不构造选择器）。
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Placeholder string   `json:"placeholder"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Options     []string `json:"options,omitempty"`
	MaxLength   int      `json:"max_length,omitempty"`
}

// 字段处理动作。
const (
	ActionFill = "FILL" // 自动填写
	ActionSkip = "SKIP" // 跳过，交给用户
)

// 跳过原因。
const (
	ReasonSensitive     = "sensitive"      // 命中敏感字段黑名单
	ReasonNoMapping     = "no_mapping"     // 找不到对应的申请信息
	ReasonLowConfidence = "low_confidence" // 映射置信度不足
)

// FieldMapping 是单个字段的映射结论。
type FieldMapping struct {
	Ref string `json:"ref"`
	// Action 只能是 FILL 或 SKIP。
	Action string `json:"action"`
	// Source 是申请信息中的取值路径，例如 education.school。SKIP 时为空。
	Source string `json:"source"`
	// Value 是最终要填入页面的值。SKIP 时为空。
	Value string `json:"value"`
	// Confidence 是映射置信度。
	Confidence float64 `json:"confidence"`
	// Reason 是 SKIP 的原因。
	Reason string `json:"reason"`
	// Label 回传字段标签，便于前端提示用户。
	Label string `json:"label"`
	// Required 回传是否必填。
	Required bool `json:"required"`
	// IsSensitive 标记是否敏感。
	IsSensitive bool `json:"is_sensitive"`
}

// minAcceptableConfidence 是允许自动填写的最低置信度。
const minAcceptableConfidence = 0.75

// ruleMap 是规则优先的字段映射表。
// key 为出现在 label/name/placeholder 中的关键词，value 为申请信息路径。
var ruleMap = []struct {
	keywords []string
	source   string
}{
	{[]string{"姓名", "真实姓名", "name", "full name", "申请人"}, "basic.name"},
	{[]string{"手机", "电话", "联系方式", "mobile", "phone", "telephone"}, "basic.phone"},
	{[]string{"邮箱", "电子邮件", "email", "e-mail", "mail"}, "basic.email"},
	{[]string{"性别", "gender", "sex"}, "basic.gender"},
	{[]string{"学校", "院校", "毕业院校", "university", "school", "college"}, "education.school"},
	{[]string{"专业", "所学专业", "major", "field of study"}, "education.major"},
	{[]string{"学历", "最高学历", "degree", "education level"}, "education.degree"},
	{[]string{"毕业时间", "毕业年份", "毕业年月", "graduation", "graduate date"}, "education.graduation_date"},
	{[]string{"入学时间", "enrollment"}, "education.start_date"},
	{[]string{"gpa", "成绩", "绩点", "均分"}, "education.gpa"},
	{[]string{"城市", "期望城市", "工作地点", "意向城市", "preferred city", "location"}, "preference.city"},
	{[]string{"期望岗位", "应聘岗位", "求职意向", "expected position", "desired position"}, "preference.role"},
	{[]string{"技术栈", "技能", "掌握技能", "skills", "technical skills"}, "skills.summary"},
	{[]string{"项目经历", "项目经验", "project experience"}, "experience.projects"},
	{[]string{"实习经历", "实习经验", "internship"}, "experience.internships"},
	{[]string{"工作经历", "work experience"}, "experience.internships"},
	{[]string{"自我评价", "个人优势", "self evaluation", "about me", "个人简介"}, "basic.self_intro"},
	{[]string{"获奖", "荣誉", "awards", "honors"}, "experience.awards"},
	{[]string{"外语", "英语水平", "语言能力", "language proficiency", "cet"}, "skills.languages"},
	{[]string{"github", "个人主页", "博客", "portfolio", "homepage"}, "basic.homepage"},
	{[]string{"籍贯", "户籍", "hometown"}, "basic.hometown"},
	{[]string{"政治面貌"}, ""}, // 明确不填，属于敏感个人信息
}

// MapFieldsByRule 用纯规则完成映射，不调用模型。
//
// 处理顺序至关重要：
//  1. 先判定敏感 → 一律 SKIP；
//  2. 再走规则表；
//  3. 命中不了的留给 LLM 兜底。
//
// 返回：已确定的映射，以及需要 LLM 处理的剩余字段。
func MapFieldsByRule(fields []FormField, values map[string]string) (resolved []FieldMapping, remaining []FormField) {
	for _, f := range fields {
		// 第一优先级：敏感字段无条件跳过。
		if security.IsSensitiveField(f.Label, f.Name, f.Placeholder) ||
			security.IsSensitiveInputType(f.Type) {
			resolved = append(resolved, FieldMapping{
				Ref:         f.Ref,
				Action:      ActionSkip,
				Reason:      ReasonSensitive,
				Label:       f.Label,
				Required:    f.Required,
				IsSensitive: true,
			})
			continue
		}

		if src, ok := matchRule(f); ok {
			if src == "" {
				// 规则明确要求不填。
				resolved = append(resolved, FieldMapping{
					Ref: f.Ref, Action: ActionSkip, Reason: ReasonSensitive,
					Label: f.Label, Required: f.Required, IsSensitive: true,
				})
				continue
			}
			val, exists := values[src]
			if !exists || strings.TrimSpace(val) == "" {
				resolved = append(resolved, FieldMapping{
					Ref: f.Ref, Action: ActionSkip, Reason: ReasonNoMapping,
					Source: src, Label: f.Label, Required: f.Required,
				})
				continue
			}
			resolved = append(resolved, FieldMapping{
				Ref: f.Ref, Action: ActionFill, Source: src,
				Value: clampValue(val, f), Confidence: 1.0,
				Label: f.Label, Required: f.Required,
			})
			continue
		}

		remaining = append(remaining, f)
	}
	return resolved, remaining
}

// matchRule 在规则表中查找字段对应的来源路径。
func matchRule(f FormField) (string, bool) {
	hay := strings.ToLower(f.Label + " " + f.Name + " " + f.Placeholder)
	for _, rule := range ruleMap {
		for _, kw := range rule.keywords {
			if strings.Contains(hay, kw) {
				return rule.source, true
			}
		}
	}
	return "", false
}

// clampValue 按字段的长度限制裁剪值。
func clampValue(v string, f FormField) string {
	v = strings.TrimSpace(v)
	if f.MaxLength > 0 {
		r := []rune(v)
		if len(r) > f.MaxLength {
			return string(r[:f.MaxLength])
		}
	}
	return v
}

// ---------------- LLM 兜底 ----------------

type llmMappingItem struct {
	Ref        string  `json:"ref"`
	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

type llmMappingResult struct {
	Mappings []llmMappingItem `json:"mappings"`
}

const fieldMapperSystemPrompt = `你是一名表单字段语义映射助手。
任务：把招聘网站申请表的字段，映射到候选人可复用申请信息的字段路径。

可用的字段路径只能从下面这份清单中选择：
basic.name, basic.phone, basic.email, basic.gender, basic.hometown, basic.homepage, basic.self_intro,
education.school, education.major, education.degree, education.graduation_date, education.start_date, education.gpa,
preference.city, preference.role,
skills.summary, skills.languages,
experience.projects, experience.internships, experience.awards

规则：
1. 只能使用上面清单中的路径，不得发明新路径。
2. 无法确定对应关系时，source 填 ""。宁可不填，也不要猜。
3. confidence 取 0 到 1，表示你对该映射的确定程度。
4. 绝对不要为身份证、护照、银行卡、密码、验证码、人脸识别、政治面貌、婚育状况等字段给出映射，这类字段必须返回 source 为 ""。
5. 只输出 JSON，不要输出解释。

输出 JSON 格式：
{"mappings":[{"ref":"字段的 ref 原值","source":"","confidence":0}]}`

// allowedSources 是 LLM 允许返回的路径白名单。
// 模型返回任何不在此集合中的路径都会被丢弃，防止越权取值。
var allowedSources = map[string]bool{
	"basic.name": true, "basic.phone": true, "basic.email": true,
	"basic.gender": true, "basic.hometown": true, "basic.homepage": true,
	"basic.self_intro": true,
	"education.school": true, "education.major": true, "education.degree": true,
	"education.graduation_date": true, "education.start_date": true, "education.gpa": true,
	"preference.city": true, "preference.role": true,
	"skills.summary": true, "skills.languages": true,
	"experience.projects": true, "experience.internships": true, "experience.awards": true,
}

// MapFieldsByLLM 对规则无法覆盖的字段做语义映射。
//
// 安全约束：
//   - 只传字段元信息（label/name/type/options），绝不传候选人的实际值；
//   - 模型返回的 source 必须在白名单内；
//   - 敏感字段在调用前已被规则层过滤，此处再兜底校验一次。
func (c *Client) MapFieldsByLLM(ctx context.Context, fields []FormField, values map[string]string) ([]FieldMapping, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	if !c.Enabled() {
		return skipAll(fields, ReasonNoMapping), nil
	}

	// 只序列化元信息。
	type fieldMeta struct {
		Ref         string   `json:"ref"`
		Label       string   `json:"label"`
		Name        string   `json:"name"`
		Placeholder string   `json:"placeholder"`
		Type        string   `json:"type"`
		Required    bool     `json:"required"`
		Options     []string `json:"options,omitempty"`
	}
	metas := make([]fieldMeta, 0, len(fields))
	for _, f := range fields {
		metas = append(metas, fieldMeta{
			Ref: f.Ref, Label: f.Label, Name: f.Name,
			Placeholder: f.Placeholder, Type: f.Type,
			Required: f.Required, Options: f.Options,
		})
	}
	payload, err := json.Marshal(map[string]any{"fields": metas})
	if err != nil {
		return nil, err
	}

	var out llmMappingResult
	if err := c.completeJSON(ctx, fieldMapperSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}

	byRef := make(map[string]FormField, len(fields))
	for _, f := range fields {
		byRef[f.Ref] = f
	}

	seen := make(map[string]bool, len(out.Mappings))
	result := make([]FieldMapping, 0, len(fields))

	for _, m := range out.Mappings {
		f, ok := byRef[m.Ref]
		if !ok || seen[m.Ref] {
			continue
		}
		seen[m.Ref] = true

		// 兜底再查一次敏感性。
		if security.IsSensitiveField(f.Label, f.Name, f.Placeholder) {
			result = append(result, FieldMapping{
				Ref: f.Ref, Action: ActionSkip, Reason: ReasonSensitive,
				Label: f.Label, Required: f.Required, IsSensitive: true,
			})
			continue
		}
		// 路径白名单校验。
		if m.Source == "" || !allowedSources[m.Source] {
			result = append(result, FieldMapping{
				Ref: f.Ref, Action: ActionSkip, Reason: ReasonNoMapping,
				Label: f.Label, Required: f.Required,
			})
			continue
		}
		if m.Confidence < minAcceptableConfidence {
			result = append(result, FieldMapping{
				Ref: f.Ref, Action: ActionSkip, Reason: ReasonLowConfidence,
				Source: m.Source, Confidence: m.Confidence,
				Label: f.Label, Required: f.Required,
			})
			continue
		}
		val := strings.TrimSpace(values[m.Source])
		if val == "" {
			result = append(result, FieldMapping{
				Ref: f.Ref, Action: ActionSkip, Reason: ReasonNoMapping,
				Source: m.Source, Confidence: m.Confidence,
				Label: f.Label, Required: f.Required,
			})
			continue
		}
		result = append(result, FieldMapping{
			Ref: f.Ref, Action: ActionFill, Source: m.Source,
			Value: clampValue(val, f), Confidence: m.Confidence,
			Label: f.Label, Required: f.Required,
		})
	}

	// 模型漏答的字段一律跳过。
	for _, f := range fields {
		if !seen[f.Ref] {
			result = append(result, FieldMapping{
				Ref: f.Ref, Action: ActionSkip, Reason: ReasonNoMapping,
				Label: f.Label, Required: f.Required,
			})
		}
	}
	return result, nil
}

// skipAll 把所有字段标记为跳过，用于 LLM 不可用时的降级。
func skipAll(fields []FormField, reason string) []FieldMapping {
	out := make([]FieldMapping, 0, len(fields))
	for _, f := range fields {
		out = append(out, FieldMapping{
			Ref: f.Ref, Action: ActionSkip, Reason: reason,
			Label: f.Label, Required: f.Required,
			IsSensitive: security.IsSensitiveField(f.Label, f.Name, f.Placeholder),
		})
	}
	return out
}
