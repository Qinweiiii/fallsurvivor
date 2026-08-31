package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

// TavilySource 通过 Tavily 做全网补充搜索。
type TavilySource struct {
	cfg    config.TavilyConfig
	client *http.Client
	// domainHint 是希望优先命中的域名列表。
	domainHint []string
}

// NewTavilySource 创建 Tavily 来源。
func NewTavilySource(cfg config.TavilyConfig) *TavilySource {
	return &TavilySource{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

// Type 实现 JobSource。
func (s *TavilySource) Type() string { return model.SourceTavily }

// Name 实现 JobSource。
func (s *TavilySource) Name() string { return "Tavily 全网搜索" }

// Available 实现 JobSource。
func (s *TavilySource) Available() bool { return s.cfg.Enabled() }

type tavilyRequest struct {
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth"`
	MaxResults     int      `json:"max_results"`
	IncludeAnswer  bool     `json:"include_answer"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	ExcludeDomains []string `json:"exclude_domains,omitempty"`
}

type tavilyResponse struct {
	Results []struct {
		Title         string  `json:"title"`
		URL           string  `json:"url"`
		Content       string  `json:"content"`
		RawContent    string  `json:"raw_content"`
		Score         float64 `json:"score"`
		PublishedDate string  `json:"published_date"`
	} `json:"results"`
}

// excludedDomains 排除明显无价值的聚合站与内容农场。
var excludedDomains = []string{
	"zhihu.com", "csdn.net", "jianshu.com", "cnblogs.com",
	"baike.baidu.com", "douban.com", "weibo.com", "bilibili.com",
	"xiaohongshu.com", "toutiao.com",
}

// Search 实现 JobSource。多条检索式并发执行，单条失败不影响其他。
func (s *TavilySource) Search(ctx context.Context, q SearchQuery) ([]RawJob, error) {
	if !s.Available() {
		return nil, fmt.Errorf("tavily: 未配置 TAVILY_API_KEY")
	}
	if len(q.Queries) == 0 {
		return nil, nil
	}

	maxResults := q.MaxResultsPerQuery
	if maxResults <= 0 || maxResults > 20 {
		maxResults = 10
	}

	type bucket struct {
		jobs []RawJob
		err  error
	}
	buckets := make([]bucket, len(q.Queries))

	g, gctx := errgroup.WithContext(ctx)
	// 限制并发，避免触发上游频率限制。
	g.SetLimit(4)

	for i, query := range q.Queries {
		i, query := i, query
		g.Go(func() error {
			jobs, err := s.searchOne(gctx, query, maxResults)
			buckets[i] = bucket{jobs: jobs, err: err}
			// 单条失败不中断整体。
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var (
		out      []RawJob
		failures int
	)
	for _, b := range buckets {
		if b.err != nil {
			failures++
			slog.Warn("tavily: 单条检索失败", "error", b.err.Error())
			continue
		}
		out = append(out, b.jobs...)
	}

	if failures == len(q.Queries) {
		return nil, fmt.Errorf("tavily: 全部 %d 条检索均失败", failures)
	}
	return out, nil
}

// searchOne 执行单条检索，带一次重试。
func (s *TavilySource) searchOne(ctx context.Context, query string, maxResults int) ([]RawJob, error) {
	body, err := json.Marshal(tavilyRequest{
		Query:          query,
		SearchDepth:    "advanced",
		MaxResults:     maxResults,
		IncludeAnswer:  false,
		ExcludeDomains: excludedDomains,
	})
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(s.cfg.BaseURL, "/") + "/search"

	const maxAttempts = 2
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxAttempts {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("tavily: 上游返回 %d", resp.StatusCode)
			if attempt < maxAttempts {
				time.Sleep(time.Duration(attempt*2) * time.Second)
				continue
			}
			return nil, lastErr
		}
		if resp.StatusCode != http.StatusOK {
			// 不回显响应体，避免泄漏上游细节。
			return nil, fmt.Errorf("tavily: 上游返回 %d", resp.StatusCode)
		}

		var parsed tavilyResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("tavily: 响应解析失败: %w", err)
		}

		out := make([]RawJob, 0, len(parsed.Results))
		for _, r := range parsed.Results {
			if strings.TrimSpace(r.URL) == "" {
				continue
			}
			job := RawJob{
				SourceType: s.detectSourceType(r.URL),
				SourceName: s.detectSourceName(r.URL),
				URL:        strings.TrimSpace(r.URL),
				Title:      strings.TrimSpace(r.Title),
				Snippet:    strings.TrimSpace(r.Content),
				Content:    strings.TrimSpace(r.RawContent),
				Score:      r.Score,
				Query:      query,
			}
			if t := parseLooseDate(r.PublishedDate); t != nil {
				job.PublishedAt = t
			}
			out = append(out, job)
		}
		return out, nil
	}
	return nil, lastErr
}

// bossDomains 用于识别 BOSS 直聘链接。
var bossDomains = []string{"zhipin.com", "bosszhipin.com"}

// detectSourceType 依据域名判断来源归属。
// 命中 BOSS 域名的结果归为 BOSS 来源，便于统计与后续适配。
func (s *TavilySource) detectSourceType(rawURL string) string {
	lower := strings.ToLower(rawURL)
	for _, d := range bossDomains {
		if strings.Contains(lower, d) {
			return model.SourceBoss
		}
	}
	return model.SourceTavily
}

// detectSourceName 从 URL 中提取可读来源名。
func (s *TavilySource) detectSourceName(rawURL string) string {
	host := extractHost(rawURL)
	if host == "" {
		return "Tavily 搜索结果"
	}
	lower := strings.ToLower(host)
	for _, d := range bossDomains {
		if strings.Contains(lower, d) {
			return "BOSS直聘"
		}
	}
	return host
}
