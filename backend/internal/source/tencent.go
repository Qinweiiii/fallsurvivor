package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// ---- 腾讯招聘官方接口数据源 ----
//
// 腾讯招聘提供了公开的岗位查询 JSON 接口，无需登录、无需浏览器自动化：
//   https://careers.tencent.com/tencentcareer/api/post/Query
//
// 相比「Playwright 多步点击 + LLM 逐步决策」的方案，本数据源：
//   - 零 LLM token 消耗；
//   - 不需要用户手动登录，也不依赖浏览器；
//   - 直接返回事业群（BGName）与业务（ProductName），
//     这正是「点进岗位页最底下才看得到部门与业务」的信息；
//   - 返回近乎全量的岗位（含职责与要求正文），可直接复用后续解析与打分管线。
//
// 安全约束（与 SSRF 防护一致）：
//   - 请求域名与路径在代码中固定，绝不由外部输入拼装 host；
//   - 仅允许 https，且限定 careers.tencent.com；
//   - 关键词经 URL 编码并限制长度，响应体有大小上限，避免被用作探测载体。

// tencentAPIHost 是固定的接口域名（不接受外部输入）。
const tencentAPIHost = "https://careers.tencent.com"

// tencentAPIPath 是固定的接口路径。
const tencentAPIPath = "/tencentcareer/api/post/Query"

const (
	// tencentMaxKeywordLen 限制关键词长度，避免超长查询被滥用。
	tencentMaxKeywordLen = 64
	// tencentMaxKeywords 限制单次任务使用的关键词数量，控制请求次数。
	tencentMaxKeywords = 2
	// tencentMaxPageSize 单次请求返回的最大条数（接口上限附近取值）。
	tencentMaxPageSize = 50
	// tencentMaxBody 响应体上限：4 MiB，防止异常响应耗尽内存。
	tencentMaxBody = 4 << 20
)

// TencentSource 通过腾讯招聘官方公开接口获取岗位。
type TencentSource struct {
	client *http.Client
}

// NewTencentSource 创建腾讯官方数据源。client 为空时使用默认客户端（含超时）。
func NewTencentSource(client *http.Client) *TencentSource {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &TencentSource{client: client}
}

// Type 实现 JobSource。
func (s *TencentSource) Type() string { return model.SourceOfficial }

// Name 实现 JobSource。
func (s *TencentSource) Name() string { return "腾讯招聘官网" }

// Available 实现 JobSource。公开接口无需任何凭据，恒为可用。
func (s *TencentSource) Available() bool { return true }

// tencentPost 是接口返回的单个岗位条目。
type tencentPost struct {
	PostID           string `json:"PostId"`
	RecruitPostName  string `json:"RecruitPostName"`
	LocationName     string `json:"LocationName"`
	CountryName      string `json:"CountryName"`
	BGName           string `json:"BGName"`
	ProductName      string `json:"ProductName"`
	CategoryName     string `json:"CategoryName"`
	Responsibility   string `json:"Responsibility"`
	Requirement      string `json:"Requirement"`
	LastUpdateTime   string `json:"LastUpdateTime"`
	PostURL          string `json:"PostURL"`
	RequireWorkYears string `json:"RequireWorkYearsName"`
}

// tencentResponse 是接口的顶层响应。
type tencentResponse struct {
	Code int `json:"Code"`
	Data struct {
		Count int           `json:"Count"`
		Posts []tencentPost `json:"Posts"`
	} `json:"Data"`
}

// Search 实现 JobSource。
//
// 从检索式中提取岗位方向关键词，逐个调用官方接口并转换为 RawJob。
// 单次任务最多使用 tencentMaxKeywords 个关键词，以控制请求次数。
func (s *TencentSource) Search(ctx context.Context, q SearchQuery) ([]RawJob, error) {
	keywords := tencentKeywords(q)
	if len(keywords) == 0 {
		return nil, nil
	}

	pageSize := q.MaxResultsPerQuery
	if pageSize <= 0 || pageSize > tencentMaxPageSize {
		pageSize = tencentMaxPageSize
	}

	out := make([]RawJob, 0, pageSize*len(keywords))
	seen := make(map[string]bool)

	for _, kw := range keywords {
		if ctx.Err() != nil {
			break
		}
		posts, err := s.query(ctx, kw, pageSize)
		if err != nil {
			// 单个关键词失败不阻断整体，记录后继续。
			slog.Warn("tencent: 查询失败", "keyword", kw, "error", err.Error())
			continue
		}
		for _, p := range posts {
			if p.PostID == "" || seen[p.PostID] {
				continue
			}
			seen[p.PostID] = true
			out = append(out, p.toRawJob(kw))
		}
		slog.Debug("tencent: 查询完成", "keyword", kw, "hits", len(posts))
	}

	slog.Info("tencent: 官方接口发现岗位", "keywords", len(keywords), "count", len(out))
	return out, nil
}

// query 调用官方接口获取一页岗位。
func (s *TencentSource) query(ctx context.Context, keyword string, pageSize int) ([]tencentPost, error) {
	// 参数全部经过 URL 编码；host 与 path 固定，不接受外部输入。
	params := url.Values{}
	params.Set("timestamp", strconv.FormatInt(time.Now().UnixMilli(), 10))
	params.Set("keyword", keyword)
	params.Set("pageIndex", "1")
	params.Set("pageSize", strconv.Itoa(pageSize))
	params.Set("language", "zh-cn")
	params.Set("area", "cn")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		tencentAPIHost+tencentAPIPath+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	// 部分站点依赖 UA 与 Referer 做基础的来源校验。
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", tencentAPIHost+"/")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tencent: 接口返回 %d", resp.StatusCode)
	}

	// 响应体大小上限，防止异常响应耗尽内存。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, tencentMaxBody))
	if err != nil {
		return nil, err
	}

	var out tencentResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tencent: 响应解析失败: %w", err)
	}
	if out.Code != 200 {
		return nil, fmt.Errorf("tencent: 接口业务码 %d", out.Code)
	}
	return out.Data.Posts, nil
}

// toRawJob 把接口条目转换为统一的 RawJob。
//
// Content 拼接职责、要求与结构化属性，供后续 JD 解析与打分使用；
// 事业群/业务等信息一并写入，避免再回到页面底部逐级点击。
func (p tencentPost) toRawJob(query string) RawJob {
	var b strings.Builder
	if p.Responsibility != "" {
		b.WriteString("岗位职责：\n")
		b.WriteString(p.Responsibility)
		b.WriteString("\n\n")
	}
	if p.Requirement != "" {
		b.WriteString("岗位要求：\n")
		b.WriteString(p.Requirement)
		b.WriteString("\n\n")
	}
	// 附加结构化属性，便于解析器直接取用公司/地点/部门/业务。
	meta := []struct{ k, v string }{
		{"公司", "腾讯"},
		{"事业群", p.BGName},
		{"业务/产品", p.ProductName},
		{"职位类别", p.CategoryName},
		{"工作地点", p.LocationName},
		{"经验要求", p.RequireWorkYears},
	}
	for _, m := range meta {
		if m.v != "" {
			b.WriteString(m.k)
			b.WriteString("：")
			b.WriteString(m.v)
			b.WriteString("\n")
		}
	}

	// 摘要取职责首段，便于列表展示与快速判型。
	snippet := p.Responsibility
	if len([]rune(snippet)) > 300 {
		snippet = string([]rune(snippet)[:300])
	}

	jobURL := p.PostURL
	if jobURL == "" && p.PostID != "" {
		// 兜底构造详情页地址（域名固定）。
		jobURL = tencentAPIHost + "/jobdesc.html?postId=" + url.QueryEscape(p.PostID)
	}

	return RawJob{
		SourceType:   model.SourceOfficial,
		SourceName:   "腾讯招聘官网",
		URL:          jobURL,
		Title:        p.RecruitPostName,
		Snippet:      snippet,
		Content:      b.String(),
		CompanyHint:  "腾讯",
		LocationHint: p.LocationName,
		PublishedAt:  parseTencentTime(p.LastUpdateTime),
		Query:        query,
		// 官方接口直接给出事业群与业务线，属于权威结构化数据。
		// 交由 pipeline 覆盖模型解析结果，避免模型漏提取（此前 department/business 常为空）。
		Meta: map[string]string{
			"Company":    "腾讯",
			"Department": p.BGName,
			"Business":   p.ProductName,
			"Location":   p.LocationName,
		},
	}
}

// parseTencentTime 解析「2026年08月27日」格式的日期，失败返回 nil。
func parseTencentTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// 兼容 2026年08月27日 与 2026-08-27 两种写法。
	layouts := []string{"2006年01月02日", "2006-01-02"}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

// tencentKeywords 从检索式中提取用于官方接口的关键词。
//
// 只保留简短、明确的岗位方向词，避免把整条自然语言检索式塞进接口。
func tencentKeywords(q SearchQuery) []string {
	seen := map[string]bool{}
	out := make([]string, 0, tencentMaxKeywords)

	add := func(kw string) {
		kw = strings.TrimSpace(kw)
		if kw == "" || len([]rune(kw)) > tencentMaxKeywordLen || seen[kw] {
			return
		}
		seen[kw] = true
		out = append(out, kw)
	}

	for _, r := range extractRoleHints(q.Queries) {
		add(r)
		if len(out) >= tencentMaxKeywords {
			break
		}
	}
	return out
}
