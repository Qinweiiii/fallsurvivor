package ai

import (
	"context"
	"strings"
)

// ParsedJob 是 JD 解析结果。字段全部为具体类型，便于校验。
//
// 重要约定：模型无法确认的字段必须留空，禁止推测或编造。
type ParsedJob struct {
	CompanyName      string   `json:"company_name"`
	Department       string   `json:"department"`
	Business         string   `json:"business"`
	Title            string   `json:"title"`
	Locations        []string `json:"locations"`
	JobType          string   `json:"job_type"`
	GraduationYear   int      `json:"graduation_year"`
	Responsibilities []string `json:"responsibilities"`
	Requirements     []string `json:"requirements"`
	Languages        []string `json:"languages"`
	TechnicalStack   []string `json:"technical_stack"`
	// Deadline 为 ISO 8601 日期字符串，未知则为空串。
	Deadline string `json:"deadline"`
	// IsCampusRecruit 标记是否为校园招聘，用于过滤社招噪声。
	IsCampusRecruit bool `json:"is_campus_recruit"`
	// Confidence 是模型对本次解析可信度的自评（0~1）。
	Confidence float64 `json:"confidence"`
}

const jdParserSystemPrompt = `你是一名严谨的招聘信息结构化解析器。
任务：从给定的网页文本中**尽可能精确地**抽取岗位信息，输出结构化 JSON。

## 核心原则
在忠实原文的前提下**最大化提取**：文本中只要出现了某个字段的信息，就必须提取出来，
不要因为"不确定"就留空。只有文本中**完全没有提及**该字段时才留空。
严禁推测、严禁编造、严禁把常识或外部知识补充进结果。

## 逐字段提取指引（务必逐项检查文本）
- company_name：公司全称。从版权信息、页眉页脚、URL 域名、正文首句推断，如 "腾讯""字节跳动"。
- department：部门 / 事业群 / 所属组织。**仅当正文中明确出现具体部门名称**时才填，
  否则**留空**——切勿从 JSON / 列表中随机挑一个填入（一个岗位可能横跨数十个部门，
  系统已会从来源 Meta 覆盖此字段，模型猜错反而浪费 token 且制造错误）。
  例：若正文出现 "CSIG / 腾讯云-数据库" 才填 "腾讯云"（业务），不要填"CSIG"；
  另外，请根据对应公司的公开组织架构确认对提供的json内容进行合理的推断，有些岗位的json内容可能是一个大部门包several小部门的结构，
  并且可能是多个大部门包含着自己的多个小部门，请合理地归纳好结构，如果存在多个小部门属于多个大部门的结构，请用 "|" 进行分割，
  例：“CDG · 腾讯金融科技 | CDG · 腾讯营销 | CSIG · 腾讯云-技术与产品方向 | CSIG · 元宝 | CSIG · CodeBuddy系列智能体产品”。 
- business：业务 / 产品 / 项目线。例如 "微信支付""腾讯云""元宝""腾讯视频""QQ音乐"。
  同上，仅在正文明确提到时填入；一个岗位可能对应多个业务，**只填第一个**或留空。
- title：岗位名称。**只取岗位名本身**，去掉公司名、部门前缀、城市后缀。
  例如原文 "腾讯-WXG-后端开发工程师（北京）" 应提取为 "后端开发工程师"。
- locations：工作城市数组，如 ["深圳","北京"]。只取城市名，不带区县。
- job_type：只能是 "校园招聘" / "实习" / "社会招聘" / "" 之一。
- graduation_year：仅当文本明确提到届次（如 "2027届"）时填四位年份，否则填 0。
- responsibilities：岗位职责，拆分为多条数组，每条一个职责点，保留原文表述。
- requirements：任职要求，同上拆分为数组。
- languages：正文提到的编程语言，如 ["Go","Python","Java"]。
- technical_stack：技术框架/中间件/工具，如 ["Kubernetes","MySQL","Kafka","Redis"]。
- deadline：截止日期（ISO 8601，如 "2026-10-31"）。未提及则必须填 ""。
- is_campus_recruit：是否为校园招聘（含校招/应届/ Campus 等表述则为 true）。
- confidence：本次抽取的整体可信度自评，0 到 1。

## 输出要求
只输出 JSON，不要输出任何解释、注释或 markdown 代码块标记。

输出 JSON 格式：
{"company_name":"","department":"","business":"","title":"","locations":[],"job_type":"","graduation_year":0,"responsibilities":[],"requirements":[],"languages":[],"technical_stack":[],"deadline":"","is_campus_recruit":false,"confidence":0}`

// ParseJD 从网页文本中抽取结构化岗位信息。
//
// hint 是已知的线索（例如搜索结果标题），用于帮助模型定位，可为空。
func (c *Client) ParseJD(ctx context.Context, pageText, sourceURL, hint string) (*ParsedJob, error) {
	var sb strings.Builder
	if hint != "" {
		sb.WriteString("已知线索（可能不准确，仅供参考）：")
		sb.WriteString(hint)
		sb.WriteString("\n\n")
	}
	if sourceURL != "" {
		sb.WriteString("来源 URL：")
		sb.WriteString(sourceURL)
		sb.WriteString("\n\n")
	}
	sb.WriteString("网页文本：\n")
	sb.WriteString(pageText)

	var out ParsedJob
	if err := c.completeJSON(ctx, jdParserSystemPrompt, sb.String(), &out); err != nil {
		return nil, err
	}

	// 对模型输出做确定性清洗，不信任其边界。
	out.CompanyName = strings.TrimSpace(out.CompanyName)
	out.Title = strings.TrimSpace(out.Title)
	out.Department = strings.TrimSpace(out.Department)
	out.Business = strings.TrimSpace(out.Business)
	out.Locations = dedupNonEmpty(out.Locations, 10)
	out.Responsibilities = dedupNonEmpty(out.Responsibilities, 30)
	out.Requirements = dedupNonEmpty(out.Requirements, 30)
	out.Languages = dedupNonEmpty(out.Languages, 15)
	out.TechnicalStack = dedupNonEmpty(out.TechnicalStack, 30)

	if out.GraduationYear != 0 && (out.GraduationYear < 2000 || out.GraduationYear > 2100) {
		out.GraduationYear = 0
	}
	if out.Confidence < 0 {
		out.Confidence = 0
	}
	if out.Confidence > 1 {
		out.Confidence = 1
	}
	switch out.JobType {
	case "校园招聘", "实习", "社会招聘", "":
	default:
		out.JobType = ""
	}

	return &out, nil
}
