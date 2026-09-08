package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client 是探索 Agent 微服务的 HTTP 客户端。
// 构造时不建立连接；只有调用 Explore 时才真正发请求，因此即使 Python 服务未启动，
// Go 后端也能正常启动（开关关闭时根本不会走到这里）。
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New 构造客户端。token 与浏览器 Worker 共用（BROWSER_WORKER_TOKEN），实现双向校验。
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 5 * time.Minute}, // 探索含多步浏览器动作，给足超时
	}
}

// Explore 触发一次站点探索，返回已验证的 RecipeCandidate、探索轨迹与 Python 侧验证结论。
// 失败（含 Python 侧判定未产出可用配置）时返回 error，error 文本可直接透出给前端原因。
func (c *Client) Explore(ctx context.Context, req ExploreRequest) (*ExploreOutcome, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/explore", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Worker-Token", c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("调用探索 Agent 服务失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("探索 Agent 服务令牌校验失败（请检查 BROWSER_WORKER_TOKEN 是否一致）")
	}
	var out exploreResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析探索 Agent 响应失败: %w", err)
	}
	if out.Status != "success" || out.Recipe == nil {
		return nil, fmt.Errorf("探索 Agent 未产出可用配置: %s", out.Reason)
	}
	return &ExploreOutcome{
		Candidate:    out.Recipe,
		Trace:        out.Trace,
		Verified:     out.Verified,
		JobsFound:    out.VerifyResult.JobsFound,
		SampleTitles: out.VerifyResult.SampleTitles,
	}, nil
}
