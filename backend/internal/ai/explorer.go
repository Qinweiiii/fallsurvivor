package ai

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// ExploreActionType 是 Explorer 可执行的动作类型。
// 刻意保持极小的动作集——动作越多，LLM 越容易分心，安全性也越难保证。
type ExploreActionType string

const (
	// ExploreClick 按页面快照里的元素 ref 点击；无 ref 时兼容按可见文案点击。
	ExploreClick ExploreActionType = "click"
	// ExploreInput 向页面快照里的输入元素 ref 输入文本。
	ExploreInput ExploreActionType = "input"
	// ExploreScroll 滚动页面，用于触发懒加载。
	ExploreScroll ExploreActionType = "scroll"
	// ExploreNavigate 导航到指定 URL（仅 http/https 公网地址）。
	ExploreNavigate ExploreActionType = "navigate"
	// ExploreWait 等待若干秒，让异步请求完成。
	ExploreWait ExploreActionType = "wait"
	// ExploreSearch 在页面搜索框输入关键词并触发搜索。
	ExploreSearch ExploreActionType = "search"
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
	// Playbook 是按优先级与历史命中排序的通用探索经验。
	Playbook []ExploreTacticView `json:"playbook,omitempty"`
	// Elements 当前可交互元素（只含文案与 ref，不含选择器）。
	Elements []ExploreElementView `json:"elements,omitempty"`
	// NewRequests 上一步动作之后新增的网络请求（已打分排序）。
	NewRequests []ExploreRequestView `json:"new_requests,omitempty"`
	// LastActionResult 上一步动作的执行结果。
	LastActionResult string `json:"last_action_result"`
	// ActionHistory 是最近几步动作及其观测结果。它让模型知道哪些尝试已经
	// 无效，避免只看到上一条结果后重复 inspect、wait 或点击同一元素。
	ActionHistory []string `json:"action_history,omitempty"`
	// ProgressHint 当页面连续无变化或动作失败时给模型的纠偏提示。
	ProgressHint string `json:"progress_hint,omitempty"`
}

// ExploreTacticView 是给模型看的通用探索经验。
type ExploreTacticView struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	HitCount    int    `json:"hit_count"`
}

// ExploreElementView 是给模型看的元素视图。
type ExploreElementView struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
	Tag  string `json:"tag"`
	Type string `json:"type,omitempty"`
	Href string `json:"href,omitempty"`
}

// ExploreRequestView 是给模型看的网络请求视图。
type ExploreRequestView struct {
	Seq           int      `json:"seq"`
	Method        string   `json:"method"`
	URL           string   `json:"url"`
	Status        int      `json:"status"`
	Size          int      `json:"size"`
	Sample        string   `json:"sample"`
	SchemaSummary string   `json:"schema_summary,omitempty"`
	Score         int      `json:"score"`
	Reasons       []string `json:"reasons,omitempty"`
	// RequestBody 页面实际发出的请求体（已脱敏截断）。
	// 模型据此判断复现该接口需要发送什么，而不是靠猜。
	RequestBody string `json:"request_body,omitempty"`
	// RequestContentType 请求的 Content-Type（如 application/json）。
	RequestContentType string `json:"request_content_type,omitempty"`
	// RequestHeaders 影响接口行为的请求头（不含任何凭证）。
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
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
	// click/input 优先使用 TargetRef；Target 只作为兼容与日志说明。
	Target string `json:"target,omitempty"`
	// TargetRef 来自 observation.elements[].ref，用于精确操作具体元素。
	TargetRef string `json:"target_ref,omitempty"`
	// Confidence 对「已找到正确数据接口」的信心，0~100。
	Confidence int `json:"confidence"`
}

const explorerSystemPrompt = `你是一名招聘站点探索专家。你的任务是通过有限步数的操作，找出某个校园招聘站点的"岗位列表数据接口"。

你可以使用的动作（action）：
- click：点击某个元素。若 observation.elements 中存在对应元素，必须填写 target_ref（如 "el:3"），target 填该元素文案只作说明；只有没有 ref 时才用 target 文案兜底。
- input：向某个输入框输入文本。必须填写 target_ref，target 填要输入的文本。
- scroll：向下滚动页面触发懒加载，target 可省略。
- navigate：导航到某个 URL，target 填完整 URL（仅支持 http/https 公网地址）。
- wait：等待若干秒让异步请求完成，target 填秒数。
- search：在可见搜索框输入关键词并触发搜索，target 填关键词，必须填写该输入框的 target_ref。ref 是当前页面快照中的精确元素，不能省略或自行猜测。
- inspect：查看某个网络请求的响应内容，target 填该请求的 seq 数字。
- finish：认为已经找到岗位列表数据接口，结束探索。
- abort：认为该站点无法自动探索（如需要复杂登录、强反爬），放弃。

判断"找到数据接口"的标准：
1. 该请求的响应是 JSON，且包含岗位数组（如 positionList / jobs / list / data 等字段）；
2. 数组元素中包含岗位标题字段（如 positionTitle / jobName / positionName）；
3. 通常还包含岗位 ID（如 postId / jobId / positionId）与城市字段；
4. 字段名只是线索，必须按语义判断。workContent / duty / responsibility / requirement / qualification 等都可能是 JD 内容或任职要求。

策略建议：
- 优先参考 playbook 中靠前的经验；它们按人工优先级和历史命中数排序；
- 优先观察页面加载时自动发出的请求，很多站点的列表接口会在首屏就调用；
- 如果首屏没有，尝试点击分类、搜索、分页等控件，再观察新增请求；
- 每次操作后优先看"新增请求"列表，它们才是该操作触发的；
- schema_summary 展示 JSON 层级和字段名，比 sample 更适合判断数组位置与岗位字段；
- request_body / request_content_type 展示页面**实际发出**的请求内容。POST 型列表接口必须靠它才能复现，
  看到含分页或关键词参数的请求体，通常就是列表查询接口；
- 不确定时可先用 inspect 查看某个请求的完整响应再决定；
- 总步数有限，不要在同一类操作上反复尝试超过 2 次。
- elements 里的 ref 是本次页面快照的精确元素句柄。招聘页面常有大量同名按钮/卡片，优先用 ref，避免点错。
- action_history 记录了已尝试的动作和结果。若其中已说明某个请求已 inspect、某个动作无页面变化或 ref 已过期，不要重复同一动作；请选择新的页面动作或新的候选请求。
- 如果 progress_hint 提示页面没有变化，不要重复点击同一个元素或重复等待；应换入口、搜索、滚动或 inspect 网络候选。

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
{"action":"click","target_ref":"el:3","target":"校园招聘","confidence":60,"reasoning":"简短说明，不超过40字"}

搜索示例：
{"action":"search","target_ref":"el:5","target":"算法","confidence":45,"reasoning":"用当前岗位搜索框触发列表请求"}

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
	case ExploreClick, ExploreInput, ExploreScroll, ExploreNavigate, ExploreWait, ExploreSearch, ExploreInspect, ExploreFinish, ExploreAbort:
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
	out.TargetRef = strings.TrimSpace(out.TargetRef)
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
	case "输入", "填写":
		return string(ExploreInput)
	case "滚动", "下拉":
		return string(ExploreScroll)
	case "导航", "跳转", "打开":
		return string(ExploreNavigate)
	case "等待", "暂停":
		return string(ExploreWait)
	case "搜索", "检索", "输入搜索":
		return string(ExploreSearch)
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
	// RequestBody POST 时要发送的请求体原文。
	//
	// 由模型从观测到的真实请求体推导，而不是由代码猜测——
	// 这是「新增一个站点不用改一行 Go 代码」的关键：
	// 站点要 {} 还是 {"pageSize":20}，是数据而非分支。
	RequestBody string `json:"request_body,omitempty"`
	// RequestContentType 请求体的 Content-Type（application/json 或
	// application/x-www-form-urlencoded）。留空时按 JSON 处理。
	RequestContentType string `json:"request_content_type,omitempty"`
	// RequestHeaders 复现该接口所必需的非凭证请求头。
	RequestHeaders map[string]string `json:"request_headers,omitempty"`
	// IDField 响应中岗位 ID 的字段名。
	IDField string `json:"id_field"`
	// TitleField 响应中岗位标题的字段名。
	TitleField string `json:"title_field"`
	// ListPath 岗位数组在响应 JSON 中的路径（如 data.positionList）。
	ListPath string `json:"list_path"`
	// KeywordParam 关键词对应的查询参数名，可为空。
	KeywordParam string `json:"keyword_param,omitempty"`
	// KeywordInBody 关键词是否要写进请求体（而不是 URL query）。
	// POST 型搜索接口通常把关键词放在请求体里。
	KeywordInBody bool `json:"keyword_in_body,omitempty"`
	// FieldMap 语义字段到响应字段路径的映射，如 title -> jobName, work_content -> workContent。
	FieldMap map[string]string `json:"field_map,omitempty"`
	// Notes 站点特征说明。
	Notes string `json:"notes"`
	// Confidence 对该配置可用性的信心，0~100。
	Confidence int `json:"confidence"`
}

const builderSystemPrompt = `你是一名招聘站点逆向分析专家。给定某个校招站点的观测数据（若干网络请求，含**请求侧**与**响应侧**信息），你需要产出该站点的"岗位列表采集配置"。

这份配置会被程序**原样复现**成一次 HTTP 请求。因此你必须完整描述"怎么发这个请求"，而不只是"接口地址是什么"。

请分析并填写：
- list_api：岗位列表接口的完整 URL。**必须照抄观测数据中该请求的 url 原文，包含全部查询参数**。
  很多接口缺一个参数就会返回「parameter is incorrect」之类的业务错误，因此不要精简、不要只留域名和路径。
  只有当某个参数值需要随关键词变化时，才把该值替换为示例值或 {keyword} 占位符。
- method：列表接口的 HTTP 方法（GET 或 POST）。必须与观测到的一致。
- request_body：**仅当 method 为 POST 时填写**。直接照抄观测数据中该请求的 request_body 原文。
  若观测到的请求体为空或只是空对象，就填 {} 。不要凭空增删字段——多加一个站点不认识的字段就可能被拒。
- request_content_type：照抄观测到的 request_content_type（如 application/json 或 application/x-www-form-urlencoded）。
- request_headers：仅当某个观测到的请求头明显影响返回内容时才填（如站点自定义的渠道/语言头）。没有就留空对象。
- keyword_in_body：若关键词是放在请求体里传的（POST 搜索接口常见），填 true；若放在 URL query 里，填 false。
- keyword_param：关键词对应的参数名（无论在 query 还是 body 中）。若关键词在嵌套 JSON 请求体里，必须写点路径，如 parameter.positionName；不支持搜索则留空。
- detail_api：岗位详情接口的 URL 模板，用 {id} 占位岗位 ID。只有观测到真实详情接口，或列表项明确给出可推导的详情 API 时才填写。
- detail_url_template：详情页网页 URL 模板，用 {id} 占位。只有观测到真实详情页 URL，或列表项里有 detailUrl/jobUrl/url/href 等字段可推导时才填写；不要按站点名臆造。
- id_field：响应中岗位 ID 的字段名（如 postId）。
- title_field：响应中岗位标题的字段名（如 positionTitle）。
- list_path：岗位数组在响应 JSON 中的路径，用点号分隔（如 data.positionList）。若数组在根层则填根数组字段名。
- field_map：把语义字段映射到响应字段路径。建议包含 title/id/location/company/department/business/detail_url/description/responsibilities/requirements/work_content 中能确定的字段。
- notes：简述站点特征与注意事项（如是否 SPA、是否需要登录、分页方式、请求体要求）。
- confidence：你对该配置可用性的信心（0-100）。若观测数据不足以确定，请给较低分数。

严格要求：
- 只依据给定的观测数据，严禁编造未观测到的接口、字段或请求体；
- request_body 要忠实于观测值。这是最容易出错的地方：宁可照抄，也不要"优化"；
- 绝不要在 request_headers 中填写 cookie、authorization、token 等凭证类头部（观测数据里也不会给你）；
- 字段名不需要和语义名一致。比如 workContent 的语义是工作内容，应映射到 work_content 或 description，而不是因为不叫 jobDescription 就忽略；
- 若列表项里存在详情链接字段，应在 field_map 中映射 detail_url/url；若只有岗位 ID 而没有详情 URL，detail_url_template 可以留空，执行器会用 ID 生成稳定去重 URL；
- 若信息不足，宁可留空并给低 confidence，也不要猜测；
- URL 必须完整（含协议与域名）。

只输出 JSON，不要输出解释。

输出 JSON 格式：
{"list_api":"","method":"GET","request_body":"","request_content_type":"","request_headers":{},"keyword_in_body":false,"keyword_param":"","detail_api":"","detail_url_template":"","id_field":"","title_field":"","list_path":"","field_map":{},"notes":"","confidence":0}`

// BuildRecipeCandidate 让模型根据观测数据产出站点采集配置候选。
func (c *Client) BuildRecipeCandidate(ctx context.Context, reqs []ExploreRequestView, siteURL string) (*RecipeCandidate, error) {
	payload, err := json.Marshal(map[string]any{
		"site_url": siteURL,
		"requests": compactRecipeRequests(reqs),
	})
	if err != nil {
		return nil, err
	}
	var out RecipeCandidate
	if err := c.completeJSON(ctx, builderSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}
	normalizeRecipeCandidate(&out)
	return &out, nil
}

// RecipeAttempt 描述一次「配置 → 实际执行」的失败尝试，用于自修正。
type RecipeAttempt struct {
	// Candidate 该次尝试所用的配置。
	Candidate *RecipeCandidate `json:"candidate"`
	// Error 实际执行时的错误信息（如 HTTP 状态、JSON 路径不存在、字段为空）。
	Error string `json:"error"`
	// ResponseSample 实际拿到的响应片段（已截断），帮助模型定位路径写错在哪。
	ResponseSample string `json:"response_sample,omitempty"`
}

const refineSystemPrompt = `你是一名招聘站点逆向分析专家。你上一次产出的"岗位列表采集配置"在**真实执行**时失败了。

现在给你：
1. requests：原始观测数据（含每个请求的请求体与响应结构）；
2. attempts：此前每一次尝试的配置、实际报错、以及实际拿到的响应片段。

请分析失败原因并产出**修正后的**配置。

常见失败原因与对应修正方向：
- "接口返回业务错误：code=xxx parameter is incorrect" → **HTTP 通了但参数不对**。这是缺少必需的查询参数或请求体字段，
  不要去改 list_path！回到观测数据，找到该接口**当时真实发出**的完整 URL（含全部 query 参数）与 request_body，原样照抄。
  页面能拿到数据说明参数组合是存在的，你漏抄了某个参数。
- "接口返回业务错误：...无权限/未登录/token" → 该接口需要登录态，无法脱离浏览器复现。改选观测数据中其他公开接口，或把 confidence 填 0。
- "list_path 不存在字段 X" / "指向的不是数组" → 你写的数组路径不对。回到 schema_summary 重新确认数组的真实层级（注意 data / result / content / records 等中间层）。
- HTTP 4xx / "请求入参类型不正确" / "参数格式不正确" → 请求方式或请求体不对。检查观测数据里该请求真实的 request_content_type 与 request_body，照抄它，不要自己增删字段。
- HTTP 405 → method 写错了（GET/POST 反了）。
- "响应不是合法 JSON" → 你选的可能是网页而不是接口，换一个响应为 JSON 的请求。
- 采集到 0 条岗位 → list_path 可能指向了空数组（如某个筛选项列表），或 title_field 写错导致每条都被跳过。重新确认哪个数组的元素里有岗位标题。

特别注意：若连续两次都是同一个报错，说明你的修改方向错了，换一个方向或换一个候选接口。

严格要求：
- 必须换一个**不同的**思路，不要重复提交与 attempts 中完全相同的配置；
- 只依据观测数据，严禁编造；
- 如果反复失败说明该接口不可用，可以改选观测数据里另一个候选请求；
- 若确认所有候选都不可用，把 confidence 填 0 并在 notes 说明原因。

只输出 JSON，不要输出解释。

输出 JSON 格式：
{"list_api":"","method":"GET","request_body":"","request_content_type":"","request_headers":{},"keyword_in_body":false,"keyword_param":"","detail_api":"","detail_url_template":"","id_field":"","title_field":"","list_path":"","field_map":{},"notes":"","confidence":0}`

// RefineRecipeCandidate 让模型根据「真实执行失败」的反馈修正配置。
//
// 这是自修正闭环的核心一步：把 Executor 的真实报错和实际响应喂回模型，
// 让它自己发现「路径写错了」「该发 JSON 不是 form」，
// 而不是由人去代码里加一条站点特判分支。
func (c *Client) RefineRecipeCandidate(
	ctx context.Context,
	reqs []ExploreRequestView,
	siteURL string,
	attempts []RecipeAttempt,
) (*RecipeCandidate, error) {
	payload, err := json.Marshal(map[string]any{
		"site_url": siteURL,
		"requests": compactRecipeRequests(reqs),
		"attempts": compactAttempts(attempts),
	})
	if err != nil {
		return nil, err
	}
	var out RecipeCandidate
	if err := c.completeJSON(ctx, refineSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}
	normalizeRecipeCandidate(&out)
	return &out, nil
}

// normalizeRecipeCandidate 收敛模型输出的边界值。
//
// 集中在一处而不是散落在 Build / Refine 两个函数里，
// 避免两条路径的清洗规则漂移（曾经就因此漏掉 request_body 的裁剪）。
func normalizeRecipeCandidate(out *RecipeCandidate) {
	// 方法白名单。
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
	out.DetailAPI = strings.TrimSpace(out.DetailAPI)
	out.DetailURLTemplate = strings.TrimSpace(out.DetailURLTemplate)
	out.IDField = strings.TrimSpace(out.IDField)
	out.TitleField = strings.TrimSpace(out.TitleField)
	out.ListPath = strings.TrimSpace(out.ListPath)
	out.KeywordParam = strings.TrimSpace(out.KeywordParam)
	out.Notes = strings.TrimSpace(out.Notes)

	// 请求体只在 POST 时有意义；GET 带请求体会让执行器行为不明确。
	out.RequestBody = strings.TrimSpace(out.RequestBody)
	out.RequestContentType = normalizeRequestContentType(out.RequestContentType)
	if out.Method != "POST" {
		out.RequestBody = ""
		out.RequestContentType = ""
	}

	// 请求头兜底防线：即使模型不听话填了凭证，这里也要拦掉。
	// 安全不能只依赖 prompt——模型输出是不可信输入。
	out.RequestHeaders = sanitizeRequestHeaders(out.RequestHeaders)

	if out.FieldMap == nil {
		out.FieldMap = map[string]string{}
	}
	for k, v := range out.FieldMap {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			delete(out.FieldMap, k)
			continue
		}
		if key != k {
			delete(out.FieldMap, k)
		}
		out.FieldMap[key] = val
	}
}

// normalizeRequestContentType 把模型给的 Content-Type 收敛到支持的两种。
// 无法识别时返回空串，由执行器按 JSON 兜底。
func normalizeRequestContentType(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	// 去掉 charset 等参数。
	if idx := strings.Index(s, ";"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	switch {
	case strings.Contains(s, "json"):
		return "application/json"
	case strings.Contains(s, "form-urlencoded"):
		return "application/x-www-form-urlencoded"
	default:
		return ""
	}
}

// sensitiveHeaderPattern 匹配凭证类请求头名。
var sensitiveHeaderPattern = regexp.MustCompile(
	`(?i)(cookie|auth|token|secret|password|session|csrf|signature|sign|credential|key)`)

// requestHeaderAllowlist 允许沉淀到 Recipe 的请求头名。
var requestHeaderAllowlist = map[string]bool{
	"accept":           true,
	"accept-language":  true,
	"content-type":     true,
	"x-requested-with": true,
}

// requestHeaderPrefixAllowlist 允许沉淀的自定义头前缀（渠道/语言等非凭证参数）。
var requestHeaderPrefixAllowlist = []string{"portal-", "x-portal-", "x-site-", "x-lang"}

// sanitizeRequestHeaders 过滤模型给出的请求头。
//
// 双层防护：Worker 侧观测时已按白名单过滤，这里再过滤一次，
// 因为模型输出是不可信输入——它可能凭空编造一个 Cookie 头。
// 最多保留 6 个，避免 Recipe 被塞进大量无用头部。
func sanitizeRequestHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		name := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		if name == "" || val == "" || len(val) > 200 {
			continue
		}
		if sensitiveHeaderPattern.MatchString(name) {
			continue
		}
		allowed := requestHeaderAllowlist[name]
		if !allowed {
			for _, p := range requestHeaderPrefixAllowlist {
				if strings.HasPrefix(name, p) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			continue
		}
		out[name] = val
		if len(out) >= 6 {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// compactAttempts 压缩失败尝试记录，控制上下文体积。
// 只保留最近 3 次——更早的尝试对当前修正帮助有限。
func compactAttempts(attempts []RecipeAttempt) []RecipeAttempt {
	if len(attempts) > 3 {
		attempts = attempts[len(attempts)-3:]
	}
	out := make([]RecipeAttempt, 0, len(attempts))
	for _, a := range attempts {
		a.Error = truncateRunes(a.Error, 400)
		a.ResponseSample = truncateRunes(a.ResponseSample, 800)
		out = append(out, a)
	}
	return out
}

func compactRecipeRequests(reqs []ExploreRequestView) []ExploreRequestView {
	out := make([]ExploreRequestView, 0, len(reqs))
	seen := map[string]int{}
	for _, r := range reqs {
		key := strings.ToUpper(r.Method) + " " + r.URL
		if key == " " {
			continue
		}
		r.Sample = truncateRunes(r.Sample, 500)
		r.SchemaSummary = truncateRunes(r.SchemaSummary, 900)
		// 请求体给足空间：它通常很短，却是复现接口的决定性信息。
		r.RequestBody = truncateRunes(r.RequestBody, 800)
		r.RequestHeaders = sanitizeRequestHeaders(r.RequestHeaders)
		r.FullSample = truncateRunes(r.FullSample, 2_500)
		if len(r.Reasons) > 5 {
			r.Reasons = r.Reasons[:5]
		}
		if idx, ok := seen[key]; ok {
			out[idx] = richerCompactRequest(out[idx], r)
			continue
		}
		out = append(out, r)
		seen[key] = len(out) - 1
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})
	if len(out) > 8 {
		out = out[:8]
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Seq < out[j].Seq
	})
	return out
}

func richerCompactRequest(a, b ExploreRequestView) ExploreRequestView {
	out := a
	if len([]rune(b.FullSample)) > len([]rune(out.FullSample)) {
		out.FullSample = b.FullSample
	}
	if len([]rune(b.Sample)) > len([]rune(out.Sample)) {
		out.Sample = b.Sample
	}
	if len([]rune(b.SchemaSummary)) > len([]rune(out.SchemaSummary)) {
		out.SchemaSummary = b.SchemaSummary
	}
	if b.Score > out.Score {
		out.Score = b.Score
	}
	if len(b.Reasons) > len(out.Reasons) {
		out.Reasons = b.Reasons
	}
	if b.RequestBody != "" {
		out.RequestBody = b.RequestBody
	}
	if b.RequestContentType != "" {
		out.RequestContentType = b.RequestContentType
	}
	if len(b.RequestHeaders) > 0 {
		out.RequestHeaders = b.RequestHeaders
	}
	return out
}
