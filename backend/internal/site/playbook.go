package site

import "time"

// PlaybookTactic 是跨站点复用的探索经验。
type PlaybookTactic struct {
	ID          string     `json:"id" gorm:"column:id;primaryKey;type:uuid;default:gen_random_uuid()"`
	TacticKey   string     `json:"tactic_key" gorm:"column:tactic_key;uniqueIndex;type:text;not null"`
	Title       string     `json:"title" gorm:"column:title;type:text;not null"`
	Description string     `json:"description" gorm:"column:description;type:text;not null;default:''"`
	Priority    int        `json:"priority" gorm:"column:priority;not null;default:100"`
	HitCount    int        `json:"hit_count" gorm:"column:hit_count;not null;default:0"`
	Enabled     bool       `json:"enabled" gorm:"column:enabled;not null;default:true"`
	LastUsedAt  *time.Time `json:"last_used_at" gorm:"column:last_used_at"`
	CreatedAt   time.Time  `json:"created_at" gorm:"column:created_at;not null;default:now()"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"column:updated_at;not null;default:now()"`
}

func (PlaybookTactic) TableName() string { return "exploration_playbook" }

// PresetPlaybook 是探索新招聘站点时的默认经验排序。
func PresetPlaybook() []PlaybookTactic {
	return []PlaybookTactic{
		{
			TacticKey:   "observe_initial_network",
			Title:       "先看首屏网络请求",
			Description: "进入站点后先等待并检查首屏 XHR/fetch，很多岗位列表接口会自动加载。",
			Priority:    10,
			Enabled:     true,
		},
		{
			TacticKey:   "click_campus_job_entry",
			Title:       "点击校招/职位入口",
			Description: "若首屏只有项目或导航，优先点击校园招聘、岗位、职位列表、查看职位等入口。",
			Priority:    20,
			Enabled:     true,
		},
		{
			TacticKey:   "inspect_ranked_json_schema",
			Title:       "用结构摘要判断列表接口",
			Description: "优先 inspect 高分 JSON 候选，依据 schema_summary 找岗位数组、标题、ID、城市、JD/要求字段。",
			Priority:    30,
			Enabled:     true,
		},
		{
			TacticKey: "copy_observed_request_body",
			Title:     "照抄观测到的请求体",
			Description: "POST 型列表接口必须复现请求侧信息：直接照抄该请求的 request_body 与 " +
				"request_content_type，不要自己增删字段。多加一个站点不认识的参数就可能被拒，" +
				"少一个必需参数则会返回空列表。",
			Priority: 35,
			Enabled:  true,
		},
		{
			TacticKey:   "try_keyword_query",
			Title:       "尝试关键词搜索或 URL 参数",
			Description: "找到搜索框或 URL query 时填入岗位关键词，观察新增请求里的关键词参数名；若关键词出现在请求体中，标记 keyword_in_body。",
			Priority:    40,
			Enabled:     true,
		},
		{
			TacticKey:   "identify_semantic_field_map",
			Title:       "沉淀语义字段映射",
			Description: "字段名只是线索，workContent、duty、responsibility、requirement、qualification 都要按语义映射。",
			Priority:    50,
			Enabled:     true,
		},
		{
			TacticKey:   "find_detail_template",
			Title:       "确认详情页或详情接口",
			Description: "列表接口可用后，再从 ID、URL 字段或详情请求中确认 detail_url_template/detail_api。",
			Priority:    60,
			Enabled:     true,
		},
		{
			TacticKey: "verify_then_refine",
			Title:     "配置要先跑通再沉淀",
			Description: "产出配置后会用真实请求试跑：拿到 HTTP 200 不算通过，必须真解析出带标题的岗位。" +
				"失败时按报错方向修正——list_path 报错看数组层级，参数类型报错看请求体，" +
				"0 条岗位多半是选错了数组或 title_field 写错。",
			Priority: 70,
			Enabled:  true,
		},
		{
			TacticKey:   "stop_on_login_or_antibot",
			Title:       "登录或反爬时停止",
			Description: "出现登录墙、验证码或强反爬时不要绕过，记录低 confidence 或 abort。",
			Priority:    90,
			Enabled:     true,
		},
	}
}
