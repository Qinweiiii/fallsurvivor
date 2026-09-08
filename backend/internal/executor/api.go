package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
	"github.com/eddiel/fallsurvivor/backend/pkg/safefetch"
)

// JobIdentifier 是列表项中可用于确认详情页 URL 的原始标识字段。
// 它刻意保留字段名，而不仅是值：探索器只能在真实详情 URL 中精确命中
// 某个列表字段时，才允许把该字段沉淀为 Recipe 的 IDField。
type JobIdentifier struct {
	Field string
	Value string
}

// executeAPI 执行探索沉淀出的列表 API Recipe。
//
// 核心原则：**只复现，不猜测**。
//
// 「怎么发这个请求」完全由 Recipe 数据描述（method / request_body /
// request_content_type / request_headers），这些值来自 Exploration Agent
// 对真实浏览器请求的观测。执行器不含任何按站点判断的分支——
// 接入一家新公司是新增一条数据，不是新增一段代码。
//
// 这一点是刻意纠正过的设计：早先版本在此处写过「POST 就发 JSON、
// 参数从 URL query 推导」的猜测逻辑，本质是把某个站点的实测结论
// 硬编码进了通用执行器，每接一家新站点就要再改一次。
//
// 安全边界：
//   - 全程走 safefetch（协议白名单、非法端口拦截、DNS 解析后逐个 IP 校验、
//     连接时固定已校验 IP 防 DNS rebinding、重定向逐跳重新校验、不自动跟随跳转）；
//   - 请求头经白名单过滤，不含任何凭证；需要登录态的接口应走 browser 策略；
//   - 请求体只做「模板占位符替换」，不做字符串拼接执行，且替换值经 JSON/表单转义。
func (e *Executor) executeAPI(ctx context.Context, p Params) ([]source.RawJob, error) {
	jobs, _, err := e.executeAPIWithDiag(ctx, p)
	return jobs, err
}

/**
 * executeAPIWithDiag 是 executeAPI 的诊断版：额外返回原始响应片段。
 *
 * 为什么要这个变体：验证阶段失败时，只有错误文本不足以让模型自修正——
 * 它需要看到接口**实际返回了什么**，才能判断是 list_path 写错了层级，
 * 还是请求体不对导致接口报参数错误。
 *
 * 抽成同一函数（而不是验证时再发一次请求）避免了重复请求：
 * 同一份响应既用于解析岗位，也用于诊断。
 */
// ProbeDirectFetch 落库前探测：用 safefetch 直连 ListAPI 看能否独立采到岗位。
//
// 能 → Fast Path 后续可走 api；不能（被反爬拦截 / 站点改版）→ 应走 browser_observed。
// 这是用户要求的关键环节——api 失败往往只是反爬拦截，不代表配置错误，
// 因此不能把 api 当默认策略，也不能因 api 失败就回退去重探（浪费 token）。
//
// 用多个兜底关键词试放，避免把“当前关键词无岗位”误判为“接口不可用”。
// 仅用于决定保存策略：不落库、不计入健康度、不写 recipe_runs。
func (e *Executor) ProbeDirectFetch(ctx context.Context, rc *site.Recipe) (bool, error) {
	if e.fetcher == nil {
		return false, fmt.Errorf("API 采集器未初始化，跳过探测")
	}
	for _, kw := range []string{"", "工程师", "校招", "实习"} {
		jobs, _, err := e.executeAPIWithDiag(ctx, Params{Recipe: rc, Keyword: kw})
		if err == nil && len(jobs) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (e *Executor) executeAPIWithDiag(ctx context.Context, p Params) ([]source.RawJob, string, error) {
	rc := p.Recipe
	if e.fetcher == nil {
		return nil, "", fmt.Errorf("API 采集器未初始化")
	}

	listURL, err := buildListURL(rc, p.Keyword)
	if err != nil {
		return nil, "", err
	}

	res, err := e.fetchList(ctx, rc, listURL, p.Keyword)
	if err != nil {
		return nil, "", fmt.Errorf("拉取列表接口失败: %w", err)
	}
	sample := truncate(string(res.Body), 800)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// 带上响应片段：自修正阶段要靠它判断是参数错还是路径错。
		return nil, sample, fmt.Errorf("列表接口返回 HTTP %d: %s",
			res.StatusCode, truncate(string(res.Body), 300))
	}

	jobs, err := ParseAPIJobs(rc, listURL, res.Body)
	return jobs, sample, err
}

// ParseAPIJobs 按 Recipe 字段映射解析一个列表接口 JSON 响应。
//
// 这个函数不负责发请求，只负责把“已经拿到的 JSON”转成 RawJob。
// 后端直连 API 与浏览器上下文 API replay 共用它，保证两种策略的字段语义一致。
func ParseAPIJobs(rc *site.Recipe, listURL string, body []byte) ([]source.RawJob, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("列表接口不是合法 JSON（响应片段: %s）", truncate(string(body), 200))
	}
	// 业务错误码优先于 JSON 路径解析。
	//
	// 必须在解析 list_path 之前判断：否则「参数不对」会退化成
	// 「解析不出岗位」，把自修正引向错误方向（反复改数组路径，
	// 而真正的问题在请求参数）。实测快手 open/positions/simple
	// 缺参数时返回 HTTP 200 + code:40014，正是这种情况。
	if bizErr := businessError(payload); bizErr != "" {
		return nil, fmt.Errorf("接口返回业务错误：%s", bizErr)
	}
	items, err := arrayAtPath(payload, rc.ListPath)
	if err != nil {
		return nil, err
	}

	limit := rc.MaxJobsPerSearch
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}

	out := make([]source.RawJob, 0, limit)
	for i := 0; i < limit; i++ {
		item, ok := items[i].(map[string]any)
		if !ok {
			continue
		}
		title := firstValue(item, rc.TitleField, fieldMapValue(rc, "title"))
		if title == "" {
			continue
		}
		id := firstValue(item, rc.IDField, fieldMapValue(rc, "id"))
		detailURL, identityURL := jobURLs(listURL, id,
			firstValue(item,
				fieldMapValue(rc, "detail_url"),
				fieldMapValue(rc, "url"),
				"detailUrl", "detailURL", "jobUrl", "jobURL", "url", "href", "link",
			),
			fillTemplate(rc.DetailURLTemplate, id),
		)

		company := firstNonEmpty(rc.CompanyName, fieldValue(item, fieldMapValue(rc, "company")))
		location := firstValue(item, fieldMapValue(rc, "location"), "location", "city", "cityName", "workCity")
		department := firstValue(item, fieldMapValue(rc, "department"), "department", "dept", "bg", "businessGroup")
		business := firstValue(item, fieldMapValue(rc, "business"), "business", "product", "project")
		content := semanticContent(item, rc)

		out = append(out, source.RawJob{
			SourceType:   sourceTypeForRecipe(rc),
			SourceName:   firstNonEmpty(rc.CompanyName+"校招", rc.SiteKey),
			URL:          detailURL,
			IdentityURL:  identityURL,
			Title:        title,
			Content:      content,
			Snippet:      truncate(content, 500),
			CompanyHint:  company,
			LocationHint: location,
			Meta: map[string]string{
				"Company":    company,
				"Department": department,
				"Business":   business,
				"Location":   location,
			},
		})
	}
	return out, nil
}

// APIJobIDs 从列表响应中提取岗位 ID，供探索阶段把页面真实详情链接收敛为模板。
func APIJobIDs(rc *site.Recipe, body []byte) ([]string, error) {
	if rc == nil {
		return nil, fmt.Errorf("API Recipe 为空")
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("列表响应不是有效 JSON: %w", err)
	}
	items, err := arrayAtPath(payload, rc.ListPath)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := firstValue(obj, rc.IDField, fieldMapValue(rc, "id"))
		if id != "" && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

// APIJobIdentifiers 提取列表项顶层的标量字段，供浏览器探索阶段反推
// “详情 URL 使用的是哪一个列表字段”。它不猜字段名，也不访问网络。
func APIJobIdentifiers(rc *site.Recipe, body []byte) ([]JobIdentifier, error) {
	if rc == nil {
		return nil, fmt.Errorf("API Recipe 为空")
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("列表响应不是有效 JSON: %w", err)
	}
	items, err := arrayAtPath(payload, rc.ListPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]JobIdentifier, 0, len(items)*3)
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := fieldValue(obj, key)
			if value == "" {
				continue
			}
			identity := key + "\x00" + value
			if seen[identity] {
				continue
			}
			seen[identity] = true
			out = append(out, JobIdentifier{Field: key, Value: value})
		}
	}
	return out, nil
}

/**
 * fetchList 按 Recipe 描述的方式发起列表请求。
 *
 * 分派依据只有 Recipe 数据，没有任何站点名判断：
 *   - GET                                        → safefetch.Get
 *   - POST + application/x-www-form-urlencoded   → safefetch.PostForm
 *   - POST + 其他（含留空）                       → safefetch.PostJSON
 *
 * 留空按 JSON 处理是因为绝大多数现代招聘接口用 JSON；
 * 若某站点确实要表单，探索阶段观测到的 request_content_type 会明确指定。
 */
func (e *Executor) fetchList(
	ctx context.Context,
	rc *site.Recipe,
	listURL, keyword string,
) (*safefetch.Result, error) {
	if !strings.EqualFold(strings.TrimSpace(rc.Method), "POST") {
		return e.fetcher.Get(ctx, listURL, requestHeaders(rc))
	}

	body := renderRequestBody(rc, keyword)
	if strings.Contains(rc.RequestContentType, "form-urlencoded") {
		form, err := formFromBody(body)
		if err != nil {
			return nil, err
		}
		return e.fetcher.PostForm(ctx, listURL, form, requestHeaders(rc))
	}
	return e.fetcher.PostJSON(ctx, listURL, []byte(body), requestHeaders(rc))
}

/**
 * renderRequestBody 生成本次要发送的请求体。
 *
 * 以 Recipe 中沉淀的观测原文为模板，只做两件事：
 *   1. 把 {keyword} 占位符替换为本次关键词（经 JSON 字符串转义）；
 *   2. 若配置了 keyword_in_body 且模板里没有占位符，则把关键词写入
 *      keyword_param 指定的位置（支持 JSON 点路径，如 parameter.positionName）。
 *
 * 为什么不做更复杂的模板：请求体是从真实请求抄来的，
 * 除关键词外的参数（分页、筛选）保持原样即可复现成功的那次调用。
 * 引入通用模板引擎只会增加注入面，收益却很低。
 */
func renderRequestBody(rc *site.Recipe, keyword string) string {
	body := strings.TrimSpace(rc.RequestBody)
	if body == "" {
		body = "{}"
	}
	kw := strings.TrimSpace(keyword)

	// 占位符替换：值经 JSON 编码，天然转义引号与控制字符，
	// 不会破坏请求体结构（这是防注入的关键）。
	if strings.Contains(body, keywordPlaceholder) {
		encoded, err := json.Marshal(kw)
		if err != nil {
			return body
		}
		// 模板里占位符通常已被引号包裹（如 "keyword":"{keyword}"），
		// 因此这里去掉 json.Marshal 产生的外层引号只保留转义后的内容。
		inner := strings.Trim(string(encoded), `"`)
		return strings.ReplaceAll(body, keywordPlaceholder, inner)
	}

	// 无占位符但声明了关键词在请求体中：安全地插入到 JSON 对象。
	if rc.KeywordInBody && kw != "" && rc.KeywordParam != "" {
		if injected, ok := injectJSONField(body, rc.KeywordParam, kw); ok {
			return injected
		}
	}
	return body
}

// RenderRequestBody 暴露请求体模板渲染逻辑，供 browser_api 策略复用。
func RenderRequestBody(rc *site.Recipe, keyword string) string {
	return renderRequestBody(rc, keyword)
}

// keywordPlaceholder 是请求体与 URL 模板中的关键词占位符。
const keywordPlaceholder = "{keyword}"

/**
 * injectJSONField 往 JSON 对象体中写入一个字符串字段。
 *
 * 走「反序列化 → 改 map → 重新序列化」而不是字符串拼接：
 * 这样键名与值都会被正确转义，不存在把请求体结构撑破的可能。
 * 非 JSON 对象（如数组或非法 JSON）时返回 ok=false，由调用方保持原样。
 */
func injectJSONField(body, key, value string) (string, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return "", false
	}
	if obj == nil {
		obj = map[string]any{}
	}
	path := splitJSONPath(key)
	if len(path) == 0 {
		return "", false
	}
	if len(path) > 1 {
		setJSONPath(obj, path, value)
	} else if !setExistingJSONLeaf(obj, path[0], value) {
		obj[path[0]] = value
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func splitJSONPath(path string) []string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(path), "."), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setJSONPath(obj map[string]any, path []string, value string) {
	cur := obj
	for i, part := range path {
		if i == len(path)-1 {
			cur[part] = value
			return
		}
		next, ok := lookupCI(cur, part)
		child, isObj := next.(map[string]any)
		if !ok || !isObj {
			child = map[string]any{}
			cur[part] = child
		}
		cur = child
	}
}

func setExistingJSONLeaf(obj map[string]any, key, value string) bool {
	for k, v := range obj {
		if strings.EqualFold(k, key) {
			obj[k] = value
			return true
		}
		if child, ok := v.(map[string]any); ok {
			if setExistingJSONLeaf(child, key, value) {
				return true
			}
		}
	}
	return false
}

/**
 * formFromBody 把 JSON 对象形式的请求体转成表单键值。
 *
 * 探索阶段统一以 JSON 文本沉淀请求体（便于校验与展示），
 * 需要发表单的站点在此转换。只接受 JSON 对象——
 * 嵌套结构无法无损映射为表单，遇到时直接报错而不是静默丢字段。
 */
func formFromBody(body string) (url.Values, error) {
	body = strings.TrimSpace(body)
	if body == "" || body == "{}" {
		return url.Values{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return nil, fmt.Errorf("表单型请求体必须是 JSON 对象，当前无法解析: %w", err)
	}
	form := url.Values{}
	for k, v := range obj {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		switch val := v.(type) {
		case string:
			form.Set(key, val)
		case float64, bool, json.Number:
			form.Set(key, fmt.Sprint(val))
		case nil:
			form.Set(key, "")
		default:
			return nil, fmt.Errorf("表单型请求体不支持嵌套字段 %q", key)
		}
	}
	return form, nil
}

/**
 * businessError 识别「HTTP 200 但业务失败」的响应。
 *
 * 国内接口普遍用响应体里的状态码表达失败，HTTP 状态仍是 200：
 *
 *	{"code":40014,"message":"parameter is incorrect","result":null}
 *
 * 不识别它的后果是错误信息失真：调用方只会看到「解析不出岗位」，
 * 于是自修正一直在改数组路径，而真正的问题是请求参数不对。
 * 返回可读的错误串（含原始 code 与 message）才能让模型改对方向。
 *
 * 判定刻意保守，只在「有明确失败码」时报错：
 *   - 仅认最常见的状态字段名，不猜测任意字段；
 *   - 数字码只有非 0 才算失败（0 是最通用的成功值）；
 *   - 字符串码只认公认的成功值（"0"/"ok"/"success"/"200"）之外的值；
 *   - 布尔型 success/ok 为 false 才算失败。
 *
 * 宁可漏判也不误判：误判会让本来能用的配置被判失败，
 * 代价比漏判（退回原有的「解析不出岗位」提示）大得多。
 */
func businessError(payload any) string {
	obj, ok := payload.(map[string]any)
	if !ok {
		return "" // 顶层是数组时不存在业务码包装
	}

	// 成功标志位：明确为 false 才算失败。
	for _, key := range []string{"success", "ok"} {
		if v, exists := obj[key]; exists {
			if b, isBool := v.(bool); isBool && !b {
				return describeBizError(obj, key+"=false")
			}
		}
	}

	// 状态码字段：按常见程度排序，命中第一个即判定。
	for _, key := range []string{"code", "errcode", "errno", "status", "ret", "rescode"} {
		v, exists := obj[key]
		if !exists {
			continue
		}
		switch val := v.(type) {
		case float64:
			// 0 与 200 都视为成功：部分接口把 HTTP 语义搬进响应体
			// （status:200 表示正常），把它当错误码会误判可用配置。
			// 另一些接口（如美团）使用 status:1 + msg:"成功" 表示成功，
			// 因此遇到明确成功文案时不能仅按非零数字判失败。
			if val != 0 && val != 200 && !hasPositiveMessage(obj) {
				return describeBizError(obj, fmt.Sprintf("%s=%s", key, formatNumber(val)))
			}
		case string:
			s := strings.ToLower(strings.TrimSpace(val))
			if s == "" || isSuccessToken(s) {
				continue
			}
			return describeBizError(obj, key+"="+val)
		}
		// 命中了状态字段且值表示成功，不必再看其他字段。
		return ""
	}
	return ""
}

// successTokens 是字符串状态码中公认的成功值。
var successTokens = map[string]bool{
	"0": true, "00": true, "200": true, "ok": true, "success": true, "true": true,
}

func isSuccessToken(s string) bool { return successTokens[s] }

func hasPositiveMessage(obj map[string]any) bool {
	for _, key := range []string{"message", "msg", "errmsg", "description", "detail"} {
		v, exists := obj[key]
		if !exists {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		t := strings.ToLower(strings.TrimSpace(s))
		if t == "成功" || t == "请求成功" || t == "success" || t == "ok" {
			return true
		}
	}
	return false
}

// describeBizError 拼出「码 + 服务端消息」的可读错误串。
//
// 带上服务端原文很关键：模型据此才能判断是缺参数、无权限还是路径不对。
func describeBizError(obj map[string]any, codePart string) string {
	for _, key := range []string{"message", "msg", "errmsg", "error", "description", "detail"} {
		if v, exists := obj[key]; exists {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) != "" {
				return codePart + " " + truncate(strings.TrimSpace(s), 200)
			}
		}
	}
	return codePart
}

// formatNumber 输出整数形态的数字码（避免 40014 被打成 40014.000000）。
func formatNumber(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

/**
 * requestHeaders 取出 Recipe 中沉淀的额外请求头。
 *
 * 值来自 Worker 观测 → LLM 输出 → 应用层白名单过滤三道关卡，
 * 这里只做类型转换。凭证类头部在前面各层已被剔除。
 */
func requestHeaders(rc *site.Recipe) map[string]string {
	if rc == nil || len(rc.RequestHeaders) == 0 {
		return nil
	}
	out := make(map[string]string, len(rc.RequestHeaders))
	for k, v := range rc.RequestHeaders {
		s, ok := v.(string)
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(s)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

/**
 * buildListURL 组装列表请求 URL。
 *
 * 关键词处理有两种途径，由 Recipe 数据决定而非代码猜测：
 *   - keyword_in_body=false（默认）→ 写入 URL query 的 keyword_param；
 *   - keyword_in_body=true         → 不动 URL，交由 renderRequestBody 写进请求体。
 *
 * 同时支持 URL 中的 {keyword} 占位符（部分站点把关键词放在路径段里）。
 */
func buildListURL(rc *site.Recipe, keyword string) (string, error) {
	raw := strings.TrimSpace(rc.ListAPI)
	kw := strings.TrimSpace(keyword)

	// 路径型占位符：先替换再解析，值经 QueryEscape 避免破坏 URL 结构。
	if strings.Contains(raw, keywordPlaceholder) {
		raw = strings.ReplaceAll(raw, keywordPlaceholder, url.QueryEscape(kw))
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("list_api URL 非法: %w", err)
	}
	if !rc.KeywordInBody && strings.TrimSpace(rc.KeywordParam) != "" && kw != "" {
		q := u.Query()
		q.Set(strings.TrimSpace(rc.KeywordParam), kw)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// BuildListURL 暴露列表 URL 构造逻辑，供 browser_api 策略复用。
func BuildListURL(rc *site.Recipe, keyword string) (string, error) {
	return buildListURL(rc, keyword)
}

func arrayAtPath(root any, path string) ([]any, error) {
	cur := root
	for _, part := range strings.Split(strings.Trim(strings.TrimSpace(path), "."), ".") {
		if part == "" || part == "$" {
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("list_path %q 中 %q 不是对象", path, part)
		}
		next, ok := lookupCI(obj, part)
		if !ok {
			return nil, fmt.Errorf("list_path %q 不存在字段 %q", path, part)
		}
		cur = next
	}
	arr, ok := cur.([]any)
	if !ok {
		return nil, fmt.Errorf("list_path %q 指向的不是数组", path)
	}
	return arr, nil
}

func fieldMapValue(rc *site.Recipe, key string) string {
	if rc == nil || rc.FieldMap == nil {
		return ""
	}
	if v, ok := rc.FieldMap[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func firstValue(item map[string]any, paths ...string) string {
	for _, path := range paths {
		if v := fieldValue(item, path); v != "" {
			return v
		}
	}
	return ""
}

func fieldValue(item map[string]any, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	var cur any = item
	for _, token := range parseJSONPath(path) {
		var ok bool
		cur, ok = resolvePathToken(cur, token)
		if !ok {
			return ""
		}
	}
	switch v := cur.(type) {
	case nil:
		return ""
	case string:
		return cleanScalarValue(v)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	case []any:
		// 站点常把部门/城市等字典值返回成对象数组，如美团：
		//   "department": [{"code":null,"name":"Keeta",...}]
		//   "cityList"  : [{"code":null,"name":"北京市",...}]
		// 这里提取各元素的 name（或常见同义字段）并用顿号连接，
		// 而不是把 Go 的 map 结构打印出来。
		return joinNamed(v, "、")
	case map[string]any:
		if s := namedValue(v); s != "" {
			return s
		}
		return cleanScalarValue(strings.Trim(strings.TrimSpace(fmt.Sprint(v)), "[]"))
	default:
		return cleanScalarValue(strings.Trim(strings.TrimSpace(fmt.Sprint(v)), "[]"))
	}
}

type jsonPathToken struct {
	Key   string
	Index *int
}

func parseJSONPath(path string) []jsonPathToken {
	rawParts := strings.Split(strings.Trim(path, "."), ".")
	out := make([]jsonPathToken, 0, len(rawParts))
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for {
			bracket := strings.Index(part, "[")
			if bracket < 0 {
				out = append(out, jsonPathToken{Key: part})
				break
			}
			if bracket > 0 {
				out = append(out, jsonPathToken{Key: part[:bracket]})
			}
			closeBracket := strings.Index(part[bracket:], "]")
			if closeBracket <= 0 {
				out = append(out, jsonPathToken{Key: part[bracket:]})
				break
			}
			idxText := strings.TrimSpace(part[bracket+1 : bracket+closeBracket])
			idx, err := strconv.Atoi(idxText)
			if err == nil && idx >= 0 {
				out = append(out, jsonPathToken{Index: &idx})
			}
			part = part[bracket+closeBracket+1:]
			if part == "" {
				break
			}
		}
	}
	return out
}

func resolvePathToken(cur any, token jsonPathToken) (any, bool) {
	if token.Index != nil {
		arr, ok := cur.([]any)
		if !ok || *token.Index >= len(arr) {
			return nil, false
		}
		return arr[*token.Index], true
	}
	obj, ok := cur.(map[string]any)
	if !ok {
		return nil, false
	}
	return lookupCI(obj, token.Key)
}

func cleanScalarValue(v string) string {
	s := strings.TrimSpace(v)
	switch strings.ToLower(s) {
	case "", "<nil>", "nil", "null", "undefined":
		return ""
	default:
		return s
	}
}

// namedValue 从一个字典对象中提取人类可读的名称。
// 依次尝试 name / label / text / value / title 字段（不区分大小写）。
func namedValue(obj map[string]any) string {
	for _, key := range []string{"name", "label", "text", "value", "title", "cityName", "deptName"} {
		if raw, ok := lookupCI(obj, key); ok {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// joinNamed 把对象数组转换成名称串，用 sep 连接；空元素跳过并去重。
func joinNamed(items []any, sep string) string {
	parts := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, it := range items {
		var s string
		switch v := it.(type) {
		case string:
			s = strings.TrimSpace(v)
		case map[string]any:
			s = namedValue(v)
		default:
			s = ""
		}
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		parts = append(parts, s)
	}
	return strings.Join(parts, sep)
}

func lookupCI(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func fillTemplate(tpl, id string) string {
	tpl = strings.TrimSpace(tpl)
	id = strings.TrimSpace(id)
	if tpl == "" || id == "" {
		return ""
	}
	return strings.ReplaceAll(tpl, "{id}", url.QueryEscape(id))
}

// jobURLs 分别生成用户可访问的详情 URL 与内部去重身份 URL。
// 列表 API 的 fragment fallback 只能用于身份，绝不能作为可点击外链。
func jobURLs(listURL, id string, candidates ...string) (detailURL, identityURL string) {
	base := strings.TrimSpace(listURL)
	for _, cand := range candidates {
		if u := normalizeJobURL(base, cand); u != "" {
			detailURL = u
			break
		}
	}
	if strings.TrimSpace(id) == "" {
		return detailURL, firstNonEmpty(detailURL, base)
	}
	if u, err := url.Parse(base); err == nil {
		// 列表请求中的 query 往往包含会话或筛选状态（例如 _csrf）。岗位 ID
		// 才是稳定身份，因此仅保留接口 origin 与 path。
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = "job_id=" + strings.TrimSpace(id)
		return detailURL, u.String()
	}
	return detailURL, base + "#job_id=" + url.QueryEscape(strings.TrimSpace(id))
}

// canonicalJobURL 保留给已有调用和测试使用，返回内部身份 URL。
func canonicalJobURL(listURL, id string, candidates ...string) string {
	_, identityURL := jobURLs(listURL, id, candidates...)
	return identityURL
}

func normalizeJobURL(base, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.IsAbs() {
		if u.Scheme == "http" || u.Scheme == "https" {
			return u.String()
		}
		return ""
	}
	b, err := url.Parse(strings.TrimSpace(base))
	if err != nil || b.Scheme == "" || b.Host == "" {
		return ""
	}
	return b.ResolveReference(u).String()
}

func semanticContent(item map[string]any, rc *site.Recipe) string {
	keys := []string{
		fieldMapValue(rc, "description"),
		fieldMapValue(rc, "work_content"),
		fieldMapValue(rc, "responsibilities"),
		fieldMapValue(rc, "requirements"),
		"jobDescription", "description", "desc", "content", "workContent", "work_content",
		"responsibility", "responsibilities", "requirement", "requirements", "qualification", "qualifications",
	}
	parts := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, k := range keys {
		if v := fieldValue(item, k); v != "" && !seen[v] {
			seen[v] = true
			parts = append(parts, v)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n")
	}
	raw, _ := json.Marshal(item)
	return string(raw)
}

func sourceTypeForRecipe(rc *site.Recipe) string {
	text := strings.ToLower(rc.SiteKey + " " + rc.Domain + " " + rc.CompanyName)
	if strings.Contains(text, "boss") || strings.Contains(text, "zhipin") {
		return model.SourceBoss
	}
	return model.SourceOfficial
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func truncate(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}
