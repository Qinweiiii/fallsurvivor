package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

/**
 * 从渲染后的页面 Markdown 中抽取结构化条目。
 *
 * 设计对标 browser-use 的 extract_structured_data：
 *
 *	渲染后的正文 + 抽取意图(query) + 输出结构(schema) → LLM → 结构化 JSON
 *
 * 为什么需要这条路（而不是一律逆向接口）：
 *   逆向接口更快更省 token，但覆盖不全。有些站点的列表接口带鉴权或
 *   上下文参数，脱离浏览器复现就会失败；而页面本身往往无需登录就已
 *   渲染出全部内容。坚持逆向接口反而丢掉了唾手可得的数据。
 *
 * 为什么用 LLM 读而不是写规则解析：
 *   规则解析必须为每个站点写选择器或信号词，这正是「人肉适配」的根源。
 *   本项目早先用硬编码信号词猜列表卡片，实测抓出的全是筛选器面板
 *   （面板里恰好含相同词汇），真实条目一条都没识别出来。
 *
 * ★ 本文件的核心约束：**不得出现任何站点专属知识**
 *
 *   prompt 只负责一件事——「忠实转录，不编造」。
 *   「要抽什么」由调用方通过 query + schema 传入，属于数据而非代码。
 *
 *   这是刻意的架构选择。早先版本把某个站点的卡片长相
 *   （职位性质/类别/城市/日期的排列顺序）与该站点的筛选面板词汇
 *   写进了 prompt，结果模型把它当成通用模板，遇到布局不同的站点
 *   反而被带偏。领域知识一旦进入代码，就必须为每个新站点改代码。
 *
 *   判断标准：本文件中不应出现任何公司名、站点域名，
 *   也不应出现只在某类站点成立的字段排列假设。
 */

// ExtractField 描述期望抽取的一个字段。
//
// 由调用方定义而非在本包内固化：不同业务要抽的东西不同，
// 把字段清单写死在代码里就等于把领域知识焊进了通用层。
type ExtractField struct {
	// Name 字段名，将作为输出 JSON 的键。
	Name string `json:"name"`
	// Desc 字段含义说明，直接影响抽取准确度，应尽量具体。
	Desc string `json:"desc"`
	// Required 为 true 时，缺少该字段的条目会被丢弃。
	//
	// 至少应有一个必填字段作为「这条记录是否真实存在」的判据，
	// 否则模型容易把页面噪声（导航项、筛选项）也当成条目输出。
	Required bool `json:"required,omitempty"`
}

// ExtractSchema 描述一次抽取任务的意图与输出结构。
type ExtractSchema struct {
	// Query 抽取意图的自然语言描述，如「页面上的招聘岗位列表」。
	//
	// 这是最重要的参数：它替代了「把领域规则写进 prompt」，
	// 让同一套抽取器能服务不同场景。
	Query string `json:"query"`
	// Fields 期望的字段清单。
	Fields []ExtractField `json:"fields"`
	// ItemHint 可选，说明「什么算一个条目、什么不算」。
	//
	// 用于表达通用的结构性判据（例如「一个条目应同时具备名称与至少一项属性」），
	// 而**不应**用来枚举某个站点的页面词汇。
	ItemHint string `json:"item_hint,omitempty"`
}

// ExtractedItem 是抽取到的一条记录，键为 ExtractField.Name。
//
// 用 map 而非固定结构体：字段由调用方定义，通用层不应预设业务字段。
type ExtractedItem map[string]string

// ExtractResult 是一次抽取的结果。
type ExtractResult struct {
	// Items 抽取到的条目。
	Items []ExtractedItem `json:"items"`
	// TotalHint 页面显示的条目总数（如「共 264 个」）。
	//
	// 用于判断是否需要翻页：抽到 10 条但总数 264，说明只拿到首屏。
	TotalHint int `json:"total_hint,omitempty"`
	// TargetFound 当前页面是否确实包含目标列表。
	//
	// 为 false 说明这只是首页/筛选页/详情页，调用方应继续导航，
	// 而不是把结果当成采集产出。
	TargetFound bool `json:"target_found"`
	// Note 模型的补充说明（如「本页仅渲染部分条目，需翻页」）。
	Note string `json:"note,omitempty"`
	// Truncated 正文被分块且还有后续内容。
	Truncated bool `json:"truncated,omitempty"`
	// NextStartChar 续抽起点；Truncated 为 true 时有效。
	NextStartChar int `json:"next_start_char,omitempty"`
}

/**
 * extractSystemPrompt 是抽取用的系统提示。
 *
 * 刻意保持「薄」：只约束忠实性与输出格式，不含任何领域或站点知识。
 * 对标 browser-use 的做法——它的 extract prompt 同样只有几行，
 * 「要什么」全部由 query 与 schema 承载。
 *
 * 唯一保留的领域中立提示是「Markdown 由 DOM 逐节点转换而来，
 * 同一条目的字段可能分散在连续多行」——这是转换方式的客观事实，
 * 对任何站点都成立，不是对某个站点布局的假设。
 */
const extractSystemPrompt = `你是网页信息抽取专家。给定一个网页**渲染后**的 Markdown 正文、一段抽取意图和期望的字段清单，请抽取出符合意图的条目。

关于输入正文的客观说明（有助于正确断句）：
- 正文由 DOM 逐节点转换而来，因此**同一个条目的各字段常分散在连续的多行中**，而不是排在同一行；
- 链接形如 [文案](地址)，按钮形如 <button>文案</button>；
- 空行大致对应块级容器边界，可作为条目分隔的参考，但不完全可靠。

抽取要求：
- 只抽取正文中**真实存在**的内容，严禁编造、推断或用你的既有知识补全；
- 某字段在正文中找不到时留空字符串，不要猜测；
- 标记为必填的字段若缺失，则该条目不应输出；
- 同一条目只输出一次（正文可能因组件嵌套出现重复文本）；
- 不要把页面框架元素（导航菜单、筛选选项、分页控件、宣传语、按钮文案）当作条目；
- 若提供了 already_collected，跳过其中已列出的条目，不要重复输出；
- total_hint：若正文中出现条目总数（如「共 N 条」「(N)」），填该数字，否则填 0；
- target_found：确实抽到了符合意图的条目时为 true；若当前页面不是目标列表页，为 false；
- note：如发现「本页只渲染了部分条目、需翻页或滚动」等情况，简要说明。

只输出 JSON，不要输出任何解释文字。

输出格式（items 中每个对象的键为给定的字段名）：
{"items":[{"字段名":"值"}],"total_hint":0,"target_found":false,"note":""}`

// extractMaxChunkChars 单块正文的字符上限。
//
// 取值权衡：太小会导致同一列表被切成多块、需要多次调用；
// 太大则可能超出模型上下文。6 万字符对绝大多数列表页够用，
// 超长页面由分块 + 续抽机制覆盖。
const extractMaxChunkChars = 60_000

// extractOverlapLines 块间衔接行数，保证跨块条目不丢失上下文。
const extractOverlapLines = 5

/**
 * ExtractFromMarkdown 从页面正文中抽取结构化条目。
 *
 * startFromChar 用于续抽：上次返回 Truncated=true 时，
 * 传入返回的 NextStartChar 即可继续处理后续内容。
 *
 * alreadyCollected 是已采集条目的标识（通常是名称或链接），
 * 会传给模型用于跨页去重。这比事后在代码里比对更有效——
 * 从源头避免模型重复输出，省下的是 token 而非 CPU。
 */
func (c *Client) ExtractFromMarkdown(
	ctx context.Context,
	markdown string,
	schema ExtractSchema,
	pageURL string,
	startFromChar int,
	alreadyCollected []string,
) (*ExtractResult, error) {
	md := strings.TrimSpace(markdown)
	if md == "" {
		return nil, fmt.Errorf("页面正文为空，无法抽取")
	}
	if strings.TrimSpace(schema.Query) == "" {
		return nil, fmt.Errorf("抽取意图(query)不能为空")
	}
	if len(schema.Fields) == 0 {
		return nil, fmt.Errorf("字段清单不能为空")
	}

	// 先去掉内联 JSON 大块：它挤占上下文却不含目标信息。
	md = stripJSONBlobs(md)

	chunks := ChunkMarkdownByStructure(md, extractMaxChunkChars, extractOverlapLines, startFromChar)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("续抽起点 %d 已超出正文长度 %d", startFromChar, len(md))
	}
	chunk := chunks[0]

	// 去重标识只取最近若干条：全量传回会让 prompt 无限膨胀，
	// 而重复通常发生在相邻页之间。
	if len(alreadyCollected) > 120 {
		alreadyCollected = alreadyCollected[len(alreadyCollected)-120:]
	}

	payload, err := json.Marshal(map[string]any{
		"page_url":          pageURL,
		"query":             schema.Query,
		"fields":            schema.Fields,
		"item_hint":         schema.ItemHint,
		"already_collected": alreadyCollected,
		"content":           chunk.FullContent(),
	})
	if err != nil {
		return nil, err
	}

	var out ExtractResult
	if err := c.completeJSON(ctx, extractSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}

	normalizeExtractResult(&out, schema)
	out.Truncated = chunk.HasMore
	if chunk.HasMore {
		out.NextStartChar = chunk.CharEnd
	}
	return &out, nil
}

/**
 * normalizeExtractResult 清洗模型输出。
 *
 * 只做**与站点无关**的确定性清洗：
 *   1. 去掉未在 schema 中声明的字段（模型偶尔会自行添加）；
 *   2. 丢弃缺少必填字段的条目；
 *   3. 按必填字段组合去重；
 *   4. 一条都没有时强制 TargetFound=false。
 *
 * 刻意不做「噪声词过滤」：那需要维护一张页面词表，
 * 而词表必然是从某几个站点观察来的，换站点就失效——
 * 正是要消除的人肉适配模式。判断「是不是目标条目」交给模型，
 * 代码只保证结构合法。
 */
func normalizeExtractResult(out *ExtractResult, schema ExtractSchema) {
	if out.TotalHint < 0 {
		out.TotalHint = 0
	}
	out.Note = strings.TrimSpace(out.Note)

	allowed := make(map[string]bool, len(schema.Fields))
	var required []string
	for _, f := range schema.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			continue
		}
		allowed[name] = true
		if f.Required {
			required = append(required, name)
		}
	}

	seen := make(map[string]bool, len(out.Items))
	kept := make([]ExtractedItem, 0, len(out.Items))

	for _, item := range out.Items {
		clean := make(ExtractedItem, len(item))
		for k, v := range item {
			key := strings.TrimSpace(k)
			if !allowed[key] {
				continue // 丢弃 schema 未声明的字段
			}
			clean[key] = strings.TrimSpace(v)
		}

		// 必填字段缺失即视为无效条目。
		valid := true
		for _, r := range required {
			if clean[r] == "" {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}

		// 用必填字段组合做去重键；没有必填字段时退化为整条比较。
		var keyParts []string
		if len(required) > 0 {
			for _, r := range required {
				keyParts = append(keyParts, strings.ToLower(clean[r]))
			}
		} else {
			for name := range allowed {
				keyParts = append(keyParts, strings.ToLower(clean[name]))
			}
		}
		key := strings.Join(keyParts, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		kept = append(kept, clean)
	}

	out.Items = kept
	// 一条都没抽到就不该声称找到了目标，
	// 否则上层会把空结果当成功并落库一条跑不通的配置。
	if len(kept) == 0 {
		out.TargetFound = false
	}
}
