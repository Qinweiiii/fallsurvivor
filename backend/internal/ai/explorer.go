package ai

import (
	"context"
	"encoding/json"
	"strings"
)

// ExploreActionType 是 Explorer 可执行的动作类型。
// 刻意保持极小的动作集——动作越多，LLM 越容易分心，安全性也越难保证。
type ExploreActionType string

const (
	// ExploreClick 按可见文案点击元素（提交/投递类文案会被 Worker 拒绝）。
	ExploreClick ExploreActionType = "click"
	// ExploreScroll 滚动页面，用于触发懒加载。
	ExploreScroll ExploreActionType = "scroll"
	// ExploreNavigate 导航到指定 URL（仅 http/https 公网地址）。
	ExploreNavigate ExploreActionType = "navigate"
	// ExploreWait 等待若干秒，让异步请求完成。
	ExploreWait ExploreActionType = "wait"
	// ExploreInspect 查看某个网络请求的完整响应（按 seq 指定）。
	ExploreInspect ExploreActionType = "inspect"
	// ExploreFinish 认为已找到数据接口，结束探索。
	ExploreFinish ExploreActionType = "finish"
	// ExploreAbort 认为该站点无法自动探索，放弃。
	ExploreAbort ExploreActionType = "abort"
)

// ExploreObservation 是每一步提供给模型的观测结果。
type ExploreObservation struct {
	Step        int      `json:"step"`
	CurrentURL  string   `json:"current_url"`
	PageTitle   string   `json:"page_title"`
	TextSample  string   `json:"text_sample,omitempty"`
	CardSamples []string `json:"card_samples,omitempty"`
	// Elements 当前可交互元素（只含文案与 ref，不含选择器）。
	Elements []ExploreElementView `json:"elements,omitempty"`
	// NewRequests 上一步动作之后新增的网络请求（已打分排序）。
	NewRequests []ExploreRequestView `json:"new_requests,omitempty"`
	// LastActionResult 上一步动作的执行结果。
	LastActionResult string `json:"last_action_result"`
}

// ExploreElementView 是给模型看的元素视图。
type ExploreElementView struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
	Tag  string `json:"tag"`
	Type string `json:"type,omitempty"`
}

// ExploreRequestView 是给模型看的网络请求视图。
type ExploreRequestView struct {
	Seq     int      `json:"seq"`
	Method  string   `json:"method"`
	URL     string   `json:"url"`
	Status  int      `json:"status"`
	Size    int      `json:"size"`
	Sample  string   `json:"sample"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons,omitempty"`
	// FullSample 仅在执行 inspect 后填充。
	FullSample string `json:"full_sample,omitempty"`
}

// ExploreTruncatedError 表示模型输出被截断：只返回了部分字段（通常是 reasoning），
// 关键的 action 丢失。这是可恢复错误，调用方应重试一次。
type ExploreTruncatedError struct {
	// Reasoning 已拿到的推理片段（可能不完整），便于日志排查。
	Reasoning string
}

func (e *ExploreTruncatedError) Error() string {
	msg := "模型输出被截断，缺少 action 字段"
	if e.Reasoning != "" {
		msg += "（已获取片段: " + e.Reasoning + "）"
	}
	return msg
}

// ExploreDecision 是模型每一步的决策输出。
type ExploreDecision struct {
	// Reasoning 简短说明为什么选这个动作（便于日志与调试）。
	Reasoning string `json:"reasoning"`
	Action    string `json:"action"`
	// Target click 的目标文案 / navigate 的目标 URL / inspect 的目标 seq。
	Target string `json:"target,omitempty"`
	// Confidence 对「已找到正确数据接口」的信心，0~100。
	Confidence int `json:"confidence"`
}

const explorerSystemPrompt = `你是一名招聘站点探索专家。你的任务是通过有限步数的操作，找出某个校园招聘站点的"岗位列表数据接口"。

你可以使用的动作（action）：
- click：点击某个元素，target 填该元素的可见文案（如 "校园招聘"、"技术"）。
- scroll：向下滚动页面触发懒加载，target 可省略。
- navigate：导航到某个 URL，target 填完整 URL（仅支持 http/https 公网地址）。
- wait：等待若干秒让异步请求完成，target 填秒数。
- inspect：查看某个网络请求的响应内容，target 填该请求的 seq 数字。
- finish：认为已经找到岗位列表数据接口，结束探索。
- abort：认为该站点无法自动探索（如需要复杂登录、强反爬），放弃。

判断"找到数据接口"的标准：
1. 该请求的响应是 JSON，且包含岗位数组（如 positionList / jobs / list / data 等字段）；
2. 数组元素中包含岗位标题字段（如 positionTitle / jobName / positionName）；
3. 通常还包含岗位 ID（如 postId / jobId / positionId）与城市字段。

策略建议：
- 优先观察页面加载时自动发出的请求，很多站点的列表接口会在首屏就调用；
- 如果首屏没有，尝试点击分类、搜索、分页等控件，再观察新增请求；
- 每次操作后优先看"新增请求"列表，它们才是该操作触发的；
- 不确定时可先用 inspect 查看某个请求的完整响应再决定；
- 总步数有限，不要在同一类操作上反复尝试超过 2 次。

重要：不要过早放弃！
- 很多站点的岗位列表需要先点击"校园招聘""社会招聘"等分类入口，或滚动到列表区域才会加载；
- 如果当前只看到"项目列表""分类列表"这类上层数据，说明还没进入真正的岗位列表页，
  应该继续点击相关入口（如项目名称、职位分类、"查看职位"等），而不是直接 abort；
- 只有在多次尝试后确认站点需要登录/有强反爬/无数据接口时，才使用 abort。

严格禁止：
- 不要点击任何投递、提交、申请、登录相关的元素（系统也会拒绝）；
- 不要尝试绕过验证码或反爬机制；
- 不要编造没有观测到的请求。

只输出 JSON，不要输出解释。

输出 JSON 格式（**必须严格按此字段顺序**，action 放最前面以便在输出被截断时仍能拿到动作）：
{"action":"click","target":"校园招聘","confidence":60,"reasoning":"简短说明，不超过40字"}

注意：reasoning 务必简短（40 字以内），把篇幅留给 action 与 target。
不要在 reasoning 中使用反引号或换行。`

// DecideExploreStep 让模型决策下一步探索动作。
//
// observation 包含当前页面状态与上一步的结果；
// 返回模型决策（动作 + 目标 + 信心）。
func (c *Client) DecideExploreStep(ctx context.Context, obs ExploreObservation) (*ExploreDecision, error) {
	payload, err := json.Marshal(obs)
	if err != nil {
		return nil, err
	}
	var out ExploreDecision
	if err := c.completeJSON(ctx, explorerSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}

	// 动作缺失 = 输出被截断。这是可恢复的错误：
	// 模型本想输出 action，但响应被截断导致字段丢失。
	// 直接降级为 abort 会丢失模型真实意图（它多半是想 click 某个入口），
	// 因此这里返回错误，由调用方重试一次。
	if strings.TrimSpace(out.Action) == "" {
		return nil, &ExploreTruncatedError{Reasoning: strings.TrimSpace(out.Reasoning)}
	}

	// 边界修正：动作归一化 + 白名单。
	//
	// 模型偶尔会返回非标准值（如中文「放弃」、或带多余空白/引号），
	// 这里先归一化再校验；确实无法识别时降级为 abort，
	// 但【保留模型自己的 reasoning】——它是排查与展示的重要信息，不能丢。
	out.Action = normalizeExploreAction(out.Action)
	switch ExploreActionType(out.Action) {
	case ExploreClick, ExploreScroll, ExploreNavigate, ExploreWait, ExploreInspect, ExploreFinish, ExploreAbort:
	default:
		out.Action = string(ExploreAbort)
		if strings.TrimSpace(out.Target) == "" {
			out.Target = "模型返回了未知动作"
		}
	}
	if out.Confidence < 0 {
		out.Confidence = 0
	}
	if out.Confidence > 100 {
		out.Confidence = 100
	}
	out.Target = strings.TrimSpace(out.Target)
	out.Reasoning = strings.TrimSpace(out.Reasoning)
	return &out, nil
}

// normalizeExploreAction 把模型返回的动作名归一化为标准枚举值。
// 兼容大小写、多余空白、包裹引号，以及常见的中文表述。
func normalizeExploreAction(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	// 去掉可能包裹的引号。
	s = strings.Trim(s, `"'`+"`")
	// 去掉 action: 前缀（模型有时会把整个片段塞进来）。
	if idx := strings.LastIndex(s, ":"); idx >= 0 && idx < 12 {
		s = strings.TrimSpace(s[idx+1:])
	}
	// 中文映射。
	switch s {
	case "点击", "单击":
		return string(ExploreClick)
	case "滚动", "下拉":
		return string(ExploreScroll)
	case "导航", "跳转", "打开":
		return string(ExploreNavigate)
	case "等待", "暂停":
		return string(ExploreWait)
	case "查看", "检查", "检查响应":
		return string(ExploreInspect)
	case "完成", "结束", "找到":
		return string(ExploreFinish)
	case "放弃", "中止", "终止", "无法探索":
		return string(ExploreAbort)
	}
	return s
}

// RecipeCandidate 是模型产出的站点采集配置候选。
type RecipeCandidate struct {
	// ListAPI 岗位列表接口 URL（含查询参数模板）。
	ListAPI string `json:"list_api"`
	// DetailAPI 岗位详情接口 URL 模板，用 {id} 占位岗位 ID。可为空。
	DetailAPI string `json:"detail_api"`
	// DetailURLTemplate 详情页 URL 模板，用 {id} 占位。可为空。
	DetailURLTemplate string `json:"detail_url_template"`
	// Method 列表接口的 HTTP 方法。
	Method string `json:"method"`
	// IDField 响应中岗位 ID 的字段名。
	IDField string `json:"id_field"`
	// TitleField 响应中岗位标题的字段名。
	TitleField string `json:"title_field"`
	// ListPath 岗位数组在响应 JSON 中的路径（如 data.positionList）。
	ListPath string `json:"list_path"`
	// KeywordParam 关键词对应的查询参数名，可为空。
	KeywordParam string `json:"keyword_param,omitempty"`
	// Notes 站点特征说明。
	Notes string `json:"notes"`
	// Confidence 对该配置可用性的信心，0~100。
	Confidence int `json:"confidence"`
}

const builderSystemPrompt = `你是一名招聘站点逆向分析专家。给定某个校招站点的观测数据（若干网络请求及其响应片段），你需要产出该站点的"岗位列表采集配置"。

请分析并填写：
- list_api：岗位列表接口的完整 URL（含查询参数）。若参数值需要变化（如关键词），请直接写出一个可用的示例值。
- detail_api：岗位详情接口的 URL 模板，用 {id} 占位岗位 ID。若未观测到详情接口则留空。
- detail_url_template：详情页网页 URL 模板，用 {id} 占位。若未观测到则留空。
- method：列表接口的 HTTP 方法（GET 或 POST）。
- id_field：响应中岗位 ID 的字段名（如 postId）。
- title_field：响应中岗位标题的字段名（如 positionTitle）。
- list_path：岗位数组在响应 JSON 中的路径，用点号分隔（如 data.positionList）。若数组在根层则填根数组字段名。
- keyword_param：若列表接口支持关键词搜索，填对应的查询参数名；不支持则留空。
- notes：简述站点特征与注意事项（如是否 SPA、是否需要登录、分页方式）。
- confidence：你对该配置可用性的信心（0-100）。若观测数据不足以确定，请给较低分数。

严格要求：
- 只依据给定的观测数据，严禁编造未观测到的接口或字段；
- 若信息不足，宁可留空并给低 confidence，也不要猜测；
- URL 必须完整（含协议与域名）。

只输出 JSON，不要输出解释。

输出 JSON 格式：
{"list_api":"","detail_api":"","detail_url_template":"","method":"GET","id_field":"","title_field":"","list_path":"","keyword_param":"","notes":"","confidence":0}`

// BuildRecipeCandidate 让模型根据观测数据产出站点采集配置候选。
func (c *Client) BuildRecipeCandidate(ctx context.Context, reqs []ExploreRequestView, siteURL string) (*RecipeCandidate, error) {
	payload, err := json.Marshal(map[string]any{
		"site_url": siteURL,
		"requests": reqs,
	})
	if err != nil {
		return nil, err
	}
	var out RecipeCandidate
	if err := c.completeJSON(ctx, builderSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}

	// 边界修正：方法白名单。
	out.Method = strings.ToUpper(strings.TrimSpace(out.Method))
	if out.Method != "GET" && out.Method != "POST" {
		out.Method = "GET"
	}
	if out.Confidence < 0 {
		out.Confidence = 0
	}
	if out.Confidence > 100 {
		out.Confidence = 100
	}
	out.ListAPI = strings.TrimSpace(out.ListAPI)
	out.ListPath = strings.TrimSpace(out.ListPath)
	return &out, nil
}
