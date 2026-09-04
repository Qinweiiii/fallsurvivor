// Package browser 负责与 Playwright Worker 协作完成表单辅助填写。
//
// 职责划分：
//   - Go 侧：任务状态管理、字段映射决策、敏感字段拦截、事件记录；
//   - Worker 侧：仅执行「读取表单结构」与「填写指定字段」两类动作。
//
// Worker 不接受任意选择器或脚本，Go 侧也无任何触发「点击提交」的能力。
package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
)

// 错误定义。
var (
	ErrWorkerUnavailable = errors.New("browser: Playwright Worker 未运行或不可达")
	ErrWorkerRejected    = errors.New("browser: Worker 拒绝了本次请求")
)

// Client 是 Worker 的 HTTP 客户端。
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient 创建客户端。
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// SessionRequest 是开启会话的请求。
type SessionRequest struct {
	TaskID  string `json:"task_id"`
	SiteKey string `json:"site_key"`
	URL     string `json:"url"`
}

// SessionResponse 是会话状态。
type SessionResponse struct {
	TaskID string `json:"task_id"`
	// LoggedIn 报告当前是否已处于登录态。
	LoggedIn bool `json:"logged_in"`
	// NeedsLogin 为 true 时必须由用户手动登录。
	NeedsLogin bool   `json:"needs_login"`
	CurrentURL string `json:"current_url"`
	Step       string `json:"step"`
	Message    string `json:"message"`
	// Error 由 Worker 在异常时填充（HTTP 200 + error 字段）。
	// 必须检查此字段：Worker 出错时仍返回 200，若不校验会把失败误判为成功，
	// 导致后续 /page/scrape 才报出「会话不存在」这类误导性错误。
	Error string `json:"error"`
}

// ExtractResponse 是表单结构提取结果。
type ExtractResponse struct {
	TaskID     string         `json:"task_id"`
	CurrentURL string         `json:"current_url"`
	Fields     []ai.FormField `json:"fields"`
	// Blocked 为 true 表示页面存在自动化限制，必须交给用户。
	Blocked bool   `json:"blocked"`
	Message string `json:"message"`
}

// FillRequest 是填写请求。
// 只允许按 Worker 返回的 ref 定位元素，Go 侧不构造任何选择器。
type FillRequest struct {
	TaskID string     `json:"task_id"`
	Items  []FillItem `json:"items"`
}

// FillItem 是单个填写项。
type FillItem struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
}

// FillResponse 是填写结果。
type FillResponse struct {
	TaskID     string   `json:"task_id"`
	Filled     int      `json:"filled"`
	Failed     int      `json:"failed"`
	FailedRefs []string `json:"failed_refs"`
	CurrentURL string   `json:"current_url"`
	Message    string   `json:"message"`
}

// ScrapeRequest 请求 Worker 读取当前页面正文。
type ScrapeRequest struct {
	TaskID string `json:"task_id"`
}

// ScrapeResponse 是 /page/scrape 的返回。
// 仅包含已登录浏览器当前可见页面的正文，是只读结果。
type ScrapeResponse struct {
	TaskID     string `json:"task_id"`
	NeedsLogin bool   `json:"needs_login"`
	CurrentURL string `json:"current_url"`
	PageText   string `json:"page_text"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	// Error 由 Worker 在异常时填充（HTTP 200 + error 字段）。
	Error string `json:"error"`
}

// NavActionType 是 Worker 可执行的导航动作类型。
type NavActionType string

const (
	NavClick    NavActionType = "click"
	NavClickRef NavActionType = "click_ref"
	NavInputRef NavActionType = "input_ref"
	NavScroll   NavActionType = "scroll"
	NavNavigate NavActionType = "navigate"
	NavWait     NavActionType = "wait"
	NavSearch   NavActionType = "search"
	NavBack     NavActionType = "back"
)

// NavAction 是发给 Worker 的单步导航动作。
type NavAction struct {
	Type      NavActionType `json:"type"`
	Ref       string        `json:"ref,omitempty"`
	Text      string        `json:"text,omitempty"`
	Direction string        `json:"direction,omitempty"`
	Amount    int           `json:"amount,omitempty"`
	URL       string        `json:"url,omitempty"`
	Seconds   int           `json:"seconds,omitempty"`
	Keyword   string        `json:"keyword,omitempty"`
}

// NavActRequest 是导航动作请求。
type NavActRequest struct {
	TaskID string    `json:"task_id"`
	Action NavAction `json:"action"`
}

// NavActResponse 是导航动作执行结果。
type NavActResponse struct {
	TaskID     string `json:"task_id"`
	OK         bool   `json:"ok"`
	CurrentURL string `json:"current_url"`
	Message    string `json:"message"`
	// Error 由 Worker 在异常时填充（HTTP 200 + error 字段）。
	Error string `json:"error"`
}

// StatusResponse 是任务状态。
type StatusResponse struct {
	TaskID     string `json:"task_id"`
	Alive      bool   `json:"alive"`
	CurrentURL string `json:"current_url"`
	Step       string `json:"step"`
}

// JobCard 是列表页抽取出的结构化岗位卡片。
type JobCard struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	RawText string `json:"raw_text,omitempty"`
	// Department 站点侧提供的权威部门-BG 字符串（如 "腾讯金融科技 - CDG"）。
	// 一个 postId 可能对应多个部门，adapter 会按部门展开为多条独立 JobCard。
	// 该字段会被 crawler 写入 Meta["Department"]，由 pipeline 的 applySourceMeta
	// 优先采用以覆盖模型从长文本中解析的结果。
	Department string `json:"department,omitempty"`
}

// ExtractJobsResponse 是 /page/extract-jobs 的返回。
type ExtractJobsResponse struct {
	TaskID     string    `json:"task_id"`
	CurrentURL string    `json:"current_url"`
	Jobs       []JobCard `json:"jobs"`
	Count      int       `json:"count"`
	Message    string    `json:"message"`
	// Error 由 Worker 在异常时填充（HTTP 200 + error 字段）。
	Error string `json:"error"`
}

// OpenSession 打开浏览器并导航到目标页面。
func (c *Client) OpenSession(ctx context.Context, req SessionRequest) (*SessionResponse, error) {
	var out SessionResponse
	if err := c.post(ctx, "/session/open", req, &out); err != nil {
		return nil, err
	}
	// Worker 失败时返回 200 + error 字段，必须在此拦截，
	// 否则浏览器启动失败会被静默吞掉，延后到 scrape 阶段才以「会话不存在」暴露。
	if out.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrWorkerRejected, out.Error)
	}
	return &out, nil
}

// ExtractForm 读取当前页面的表单结构。
func (c *Client) ExtractForm(ctx context.Context, taskID string) (*ExtractResponse, error) {
	var out ExtractResponse
	if err := c.post(ctx, "/form/extract", map[string]string{"task_id": taskID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FillForm 填写指定字段。
func (c *Client) FillForm(ctx context.Context, req FillRequest) (*FillResponse, error) {
	var out FillResponse
	if err := c.post(ctx, "/form/fill", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Highlight 滚动到提交按钮并提示用户检查。
// 注意：Worker 只做滚动与高亮，绝不点击。
func (c *Client) Highlight(ctx context.Context, taskID string) error {
	var out map[string]any
	return c.post(ctx, "/form/highlight-submit", map[string]string{"task_id": taskID}, &out)
}

// GetStatus 查询会话状态。
func (c *Client) GetStatus(ctx context.Context, taskID string) (*StatusResponse, error) {
	var out StatusResponse
	if err := c.post(ctx, "/session/status", map[string]string{"task_id": taskID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseSession 关闭会话。
func (c *Client) CloseSession(ctx context.Context, taskID string) error {
	var out map[string]any
	return c.post(ctx, "/session/close", map[string]string{"task_id": taskID}, &out)
}

// ScrapePage 读取已登录浏览器当前页面的 JD 正文（只读，不导航不点击）。
func (c *Client) ScrapePage(ctx context.Context, taskID string) (*ScrapeResponse, error) {
	var out ScrapeResponse
	if err := c.post(ctx, "/page/scrape", ScrapeRequest{TaskID: taskID}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrWorkerRejected, out.Error)
	}
	return &out, nil
}

// NavAct 执行单步导航动作（click/scroll/navigate/wait），由 Worker 端做安全校验。
// 该接口不会触发任何「提交/投递」动作，最终投递由用户亲自完成。
func (c *Client) NavAct(ctx context.Context, taskID string, action NavAction) (*NavActResponse, error) {
	var out NavActResponse
	if err := c.post(ctx, "/nav/act", NavActRequest{TaskID: taskID, Action: action}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrWorkerRejected, out.Error)
	}
	return &out, nil
}

// ExploreSnapshot 是页面元素快照（只读）。
type ExploreSnapshot struct {
	CurrentURL  string           `json:"current_url"`
	Title       string           `json:"title"`
	Elements    []ExploreElement `json:"elements"`
	TextSample  string           `json:"text_sample"`
	CardSamples []string         `json:"card_samples"`
}

// ExploreElement 是页面上的一个可交互元素。
type ExploreElement struct {
	Ref         string `json:"ref"`
	Tag         string `json:"tag"`
	Text        string `json:"text"`
	Placeholder string `json:"placeholder"`
	AriaLabel   string `json:"aria_label"`
	InputType   string `json:"input_type"`
	Href        string `json:"href"`
	Visible     bool   `json:"visible"`
}

// NetworkRecord 是观测到的一个网络请求（已脱敏、已截断）。
//
// 请求侧字段（RequestBody / RequestContentType / RequestHeaders）是
// 「能否原样复现该接口」的关键：POST 型列表接口只看响应是推断不出来的。
// Worker 侧已按白名单过滤请求头，绝不包含 Cookie / Authorization 等凭证。
type NetworkRecord struct {
	Seq           int    `json:"seq"`
	Method        string `json:"method"`
	URL           string `json:"url"`
	Status        int    `json:"status"`
	ContentType   string `json:"content_type"`
	Size          int    `json:"size"`
	Sample        string `json:"sample"`
	FullSample    string `json:"full_sample"`
	SchemaSummary string `json:"schema_summary"`
	// RequestBody 请求体片段（GET 为空串）。
	RequestBody string `json:"request_body"`
	// RequestContentType 请求的 Content-Type，决定复现时用 JSON 还是 form。
	RequestContentType string `json:"request_content_type"`
	// RequestHeaders 请求头白名单快照（不含凭证）。
	RequestHeaders map[string]string `json:"request_headers"`
	At             int64             `json:"at"`
}

// RankedNetworkRecord 是规则打分后的网络候选。
type RankedNetworkRecord struct {
	Record  NetworkRecord `json:"record"`
	Score   int           `json:"score"`
	Reasons []string      `json:"reasons"`
}

// ObserveStart 开始观测网络请求，返回游标作为后续 Diff 的基线。
func (c *Client) ObserveStart(ctx context.Context, taskID string) (int, error) {
	var out struct {
		Cursor     int    `json:"cursor"`
		CurrentURL string `json:"current_url"`
	}
	if err := c.post(ctx, "/explore/observe-start", map[string]string{"task_id": taskID}, &out); err != nil {
		return 0, err
	}
	return out.Cursor, nil
}

// ObserveDiff 返回自 since 以来新增的网络请求与规则打分后的候选。
// topN 控制候选条数（默认 5），rankedOnly 为 true 时不返回全量请求（省流量）。
func (c *Client) ObserveDiff(ctx context.Context, taskID string, since, limit, topN int) (int, []NetworkRecord, []RankedNetworkRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if topN <= 0 {
		topN = 5
	}
	var out struct {
		Cursor      int                   `json:"cursor"`
		CurrentURL  string                `json:"current_url"`
		NewRequests []NetworkRecord       `json:"new_requests"`
		Candidates  []RankedNetworkRecord `json:"candidates"`
	}
	if err := c.post(ctx, "/explore/observe-diff", map[string]any{
		"task_id":     taskID,
		"since":       since,
		"limit":       limit,
		"top_n":       topN,
		"ranked_only": true,
	}, &out); err != nil {
		return 0, nil, nil, err
	}
	return out.Cursor, out.NewRequests, out.Candidates, nil
}

// ObserveDiffAll 返回本轮新增的完整网络记录。执行已保存的浏览器动作时，
// 调用方需要消费页面自身收到的响应，不能再另行复放接口。
func (c *Client) ObserveDiffAll(ctx context.Context, taskID string, since, limit int) ([]NetworkRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	var out struct {
		NewRequests []NetworkRecord `json:"new_requests"`
	}
	if err := c.post(ctx, "/explore/observe-diff", map[string]any{
		"task_id":     taskID,
		"since":       since,
		"limit":       limit,
		"top_n":       0,
		"ranked_only": false,
	}, &out); err != nil {
		return nil, err
	}
	return out.NewRequests, nil
}

// InspectRequest 按 seq 读取某个已观测网络请求的更大响应片段。
func (c *Client) InspectRequest(ctx context.Context, taskID string, seq int) (*NetworkRecord, error) {
	var out struct {
		Record *NetworkRecord `json:"record"`
		Error  string         `json:"error"`
	}
	if err := c.post(ctx, "/explore/inspect-request", map[string]any{
		"task_id": taskID,
		"seq":     seq,
	}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrWorkerRejected, out.Error)
	}
	if out.Record == nil {
		return nil, fmt.Errorf("请求 seq=%d 不存在或已过期", seq)
	}
	return out.Record, nil
}

// Snapshot 读取当前页面的可交互元素与文本片段（只读）。
func (c *Client) Snapshot(ctx context.Context, taskID string) (*ExploreSnapshot, error) {
	var out ExploreSnapshot
	if err := c.post(ctx, "/explore/snapshot", map[string]string{"task_id": taskID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PageMarkdown 是渲染后页面正文的 Markdown 形式。
//
// 存在意义：很多站点的列表接口带鉴权或上下文参数，脱离浏览器无法复现
// （如快手会返回 code=40014），但页面本身无需登录就渲染出了全部岗位。
// 读渲染结果因此成为比逆向接口更通用的采集路径。
type PageMarkdown struct {
	TaskID     string `json:"task_id"`
	CurrentURL string `json:"current_url"`
	Title      string `json:"title"`
	// Markdown 已脱敏并按上限截断。
	Markdown string `json:"markdown"`
	// Truncated 为 true 说明内容超长被截断，调用方可考虑分页处理。
	Truncated bool `json:"truncated"`
}

// Markdown 读取当前页面渲染后正文的 Markdown（只读）。
//
// 与 ExtractJobs 的区别：ExtractJobs 依赖 Worker 侧写好的站点适配器，
// 只覆盖已适配站点；本方法不含任何站点知识，对任意站点通用。
func (c *Client) Markdown(ctx context.Context, taskID string) (*PageMarkdown, error) {
	var out PageMarkdown
	if err := c.post(ctx, "/page/markdown", map[string]string{"task_id": taskID}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExtractJobs 从当前列表页抽取结构化岗位卡片（半固定脚本使用）。
// keyword 为可选搜索关键词，非空时由 Worker 在站点搜索框提交后抽取过滤结果。
func (c *Client) ExtractJobs(ctx context.Context, taskID, keyword string) (*ExtractJobsResponse, error) {
	var out ExtractJobsResponse
	if err := c.post(ctx, "/page/extract-jobs", map[string]string{
		"task_id": taskID,
		"keyword": keyword,
	}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrWorkerRejected, out.Error)
	}
	return &out, nil
}

// Health 探测 Worker 是否存活。
func (c *Client) Health(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// post 执行一次 POST 请求。
func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-Worker-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWorkerUnavailable, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrWorkerRejected
	}
	if resp.StatusCode != http.StatusOK {
		slog.Warn("browser: Worker 返回非 200", "status", resp.StatusCode, "path", path)
		return fmt.Errorf("browser: Worker 返回 %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
