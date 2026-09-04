package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

/**
 * VerifyResult 是一次 Recipe 独立验证的结果。
 *
 * 「独立」的含义：不依赖浏览器会话、不写执行历史、不入库，
 * 只回答一个问题——**照这份配置真的能拿到岗位吗？**
 */
type VerifyResult struct {
	// OK 是否验证通过（真的采到了带标题的岗位）。
	OK bool
	// JobsFound 实际采到的岗位数。
	JobsFound int
	// NoJobs 表示接口请求与 JSON 解析都成功，但当前关键词没有解析出岗位。
	NoJobs bool
	// Error 失败原因。会原样喂回 LLM 供自修正，因此必须具体、可诊断。
	Error string
	// ResponseSample 实际拿到的响应片段，帮助模型判断路径写错在哪。
	ResponseSample string
	// SampleTitles 采到的前几条岗位标题，用于人工快速判断「采对了没」。
	SampleTitles []string
	// FieldQuality 展示关键字段是否在样本岗位中命中，用于判断 Recipe 字段映射质量。
	FieldQuality []FieldQuality
	// BrowserBound 表示当前页面真实响应可解析，需要 browser_observed 策略执行动作。
	BrowserBound bool
}

// FieldQuality 是 Recipe 字段映射的样本级质量诊断。
type FieldQuality struct {
	Field   string   `json:"field"`
	OK      bool     `json:"ok"`
	Hit     int      `json:"hit"`
	Total   int      `json:"total"`
	Samples []string `json:"samples,omitempty"`
}

/**
 * Verify 用真实请求验证一份 API Recipe 是否可用。
 *
 * 这是「探索 → 验证 → 自修正」闭环的裁判环节。它的存在解决了一个
 * 具体问题：LLM 产出的配置看起来合理，但真跑起来可能路径写错、
 * 请求体不对、或者指向了一个筛选项数组而不是岗位数组。
 * 没有这一步，错误配置会被直接落库，最后只能靠人去发现和修。
 *
 * 判定标准刻意严格——「拿到 HTTP 200」不算通过，
 * 必须真的解析出至少一条带标题的岗位，否则不算找到了岗位列表接口。
 *
 * 只支持 api 策略：browser 策略依赖浏览器会话与登录态，
 * 无法在探索会话之外独立复现，其正确性由探索过程本身保证。
 */
func (e *Executor) Verify(ctx context.Context, rc *site.Recipe, keyword string) VerifyResult {
	if rc == nil {
		return VerifyResult{Error: "Recipe 为空"}
	}
	if rc.StrategyType != site.StrategyAPI {
		return VerifyResult{Error: fmt.Sprintf("仅支持验证 api 策略，当前为 %s", rc.StrategyType)}
	}
	if e.fetcher == nil {
		return VerifyResult{Error: "API 采集器未初始化"}
	}

	// 单次请求同时产出岗位与诊断片段：即使解析失败，也能把
	// 「接口实际返回了什么」告诉模型。只给报错文本而不给响应内容，
	// 模型往往只能瞎猜；而重复发一次请求既慢又可能触发站点限流。
	jobs, sample, err := e.executeAPIWithDiag(ctx, Params{Recipe: rc, Keyword: keyword})
	if err != nil {
		return VerifyResult{
			Error:          err.Error(),
			ResponseSample: sample,
		}
	}
	if len(jobs) == 0 {
		return VerifyResult{
			NoJobs: true,
			Error: "接口调用成功但未解析出任何岗位：list_path 可能指向了非岗位数组，" +
				"或 title_field 写错导致每条记录都被跳过",
			ResponseSample: sample,
		}
	}

	titles := make([]string, 0, 3)
	for i := 0; i < len(jobs) && i < 3; i++ {
		titles = append(titles, jobs[i].Title)
	}
	return VerifyResult{
		OK:           true,
		JobsFound:    len(jobs),
		SampleTitles: titles,
		FieldQuality: BuildFieldQuality(jobs),
	}
}

// BuildFieldQuality 从原始岗位样本中生成关键字段质量诊断。
func BuildFieldQuality(jobs []source.RawJob) []FieldQuality {
	total := len(jobs)
	checks := []struct {
		field string
		value func(source.RawJob) string
	}{
		{field: "title", value: func(j source.RawJob) string { return j.Title }},
		{field: "description", value: func(j source.RawJob) string { return j.Content }},
		{field: "company", value: func(j source.RawJob) string { return j.CompanyHint }},
		{field: "location", value: func(j source.RawJob) string {
			return firstVerifyNonEmpty(j.LocationHint, metaValue(j, "Location"))
		}},
		{field: "department", value: func(j source.RawJob) string { return metaValue(j, "Department") }},
		{field: "business", value: func(j source.RawJob) string { return metaValue(j, "Business") }},
	}
	out := make([]FieldQuality, 0, len(checks))
	for _, check := range checks {
		q := FieldQuality{Field: check.field, Total: total}
		seen := map[string]bool{}
		for _, job := range jobs {
			v := cleanVerifyScalar(check.value(job))
			if v == "" {
				continue
			}
			q.Hit++
			if !seen[v] && len(q.Samples) < 3 {
				q.Samples = append(q.Samples, truncateVerifySample(v, 120))
				seen[v] = true
			}
		}
		q.OK = q.Hit > 0
		out = append(out, q)
	}
	return out
}

func metaValue(job source.RawJob, key string) string {
	if job.Meta == nil {
		return ""
	}
	return job.Meta[key]
}

func firstVerifyNonEmpty(values ...string) string {
	for _, v := range values {
		if t := cleanVerifyScalar(v); t != "" {
			return t
		}
	}
	return ""
}

func cleanVerifyScalar(v string) string {
	s := strings.TrimSpace(v)
	switch strings.ToLower(s) {
	case "", "<nil>", "nil", "null", "undefined":
		return ""
	default:
		return s
	}
}

func truncateVerifySample(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}
