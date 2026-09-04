package search

import "github.com/eddiel/fallsurvivor/backend/internal/ai"

/**
 * 招聘岗位的抽取意图定义。
 *
 * 为什么放在 search 包而不是 ai 包：
 *   ai 包提供的是**通用**的「从页面正文抽结构化条目」能力，
 *   不应知道「岗位有哪些字段」。把岗位字段清单放在业务层，
 *   通用层就能同时服务其他抽取场景（如公告、宣讲会日程），
 *   而不需要修改。
 *
 *   更重要的是：这里是**唯一**允许表达招聘领域知识的地方。
 *   一旦这类知识渗进 ai 包或 executor，就会变成「为每个新站点
 *   改通用代码」的开端。
 *
 * 注意字段描述里刻意不提任何具体公司或站点的页面词汇——
 * 描述的是**语义**（这个字段是什么意思），而非**长相**
 * （某个站点把它排在第几行）。这样换任意站点都成立。
 */

// jobExtractSchema 返回岗位列表的抽取意图。
//
// title 是唯一必填项：它既是「这条记录真实存在」的判据，
// 也是跨页去重的标识。若允许无标题条目通过，页面上的筛选项、
// 分页控件都可能被当成岗位。
func jobExtractSchema() ai.ExtractSchema {
	return ai.ExtractSchema{
		Query: "页面上招聘岗位列表中的每一个岗位",
		ItemHint: "一个有效岗位应当具备明确的职位名称，" +
			"并且至少还能看到工作地点、岗位类别、招聘性质、发布时间、详情链接中的任意一项。" +
			"仅有孤立短词、没有任何附属信息的内容通常是页面的筛选选项或导航项，不是岗位。",
		Fields: []ai.ExtractField{
			{
				Name:     "title",
				Desc:     "职位名称。不要把「急招」「热招」「NEW」这类招聘状态标记拼进名称里",
				Required: true,
			},
			{Name: "url", Desc: "该岗位详情页的链接地址，正文中的原文即可（相对路径也可以）。找不到就留空"},
			{Name: "location", Desc: "工作地点或城市，可能包含多个城市"},
			{Name: "category", Desc: "岗位类别或职能方向，如技术类、产品类及其细分方向"},
			{Name: "job_nature", Desc: "招聘性质，如全职、实习、校招、社招等"},
			{Name: "department", Desc: "所属部门、事业群或业务线"},
			{Name: "published_at", Desc: "发布时间或更新时间，保留页面上的原始写法，不要转换格式"},
			{Name: "description", Desc: "岗位描述、职责或要求的片段。列表页通常没有，找不到就留空"},
		},
	}
}

// 字段名常量：供转换为内部岗位结构时引用，避免散落的字符串字面量。
const (
	jobFieldTitle       = "title"
	jobFieldURL         = "url"
	jobFieldLocation    = "location"
	jobFieldCategory    = "category"
	jobFieldNature      = "job_nature"
	jobFieldDepartment  = "department"
	jobFieldPublishedAt = "published_at"
	jobFieldDesc        = "description"
)
