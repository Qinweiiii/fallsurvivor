// Package ai 是系统中唯一允许调用大模型的地方。
//
// 设计要点：
//   - 全部请求带超时与有限次退避重试；
//   - 强制 JSON 输出并反序列化到具体 DTO，不使用 map[string]any；
//   - 送入模型的文本一律先经过 security.RedactText 抹除证件号等敏感串；
//   - 未配置 API Key 时优雅降级（Enabled() 返回 false），业务需有纯规则兜底。
package ai

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

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/security"
)

// ErrDisabled 表示未配置 API Key。
var ErrDisabled = errors.New("ai: 未配置 LLM API Key（QWEN_API_KEY / DEEPSEEK_API_KEY），LLM 能力不可用")

// promptRunesLimit 限制单次送入模型的文本长度。设为 0 表示「不做 prompt 级截断」，
// 把所有内容原样送进 LLM（成本与上下文由模型端自己负责）。开发期主链路优先。
const promptRunesLimit = 0

// Client 封装 OpenAI 兼容的 Chat Completions（DeepSeek / 千问等）。
type Client struct {
	cfg        config.LLMConfig
	httpClient *http.Client
}

// NewClient 创建客户端。
func NewClient(cfg config.LLMConfig) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

// Enabled 报告 LLM 是否可用。
func (c *Client) Enabled() bool { return c.cfg.Enabled() }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// completeJSON 调用模型并要求返回 JSON 对象，结果写入 out。
// out 必须是指向具体结构体的指针。
func (c *Client) completeJSON(ctx context.Context, systemPrompt, userPrompt string, out any) error {
	if !c.Enabled() {
		return ErrDisabled
	}

	// 进入模型前抹除敏感串（如身份证号、银行卡号），不再截断长度。
	// 主链路优先：长 JD 完整送进 LLM，由模型端的 context window 兜底。
	userPrompt = security.RedactText(userPrompt)
	if promptRunesLimit > 0 {
		userPrompt = truncateRunes(userPrompt, promptRunesLimit)
	}

	reqBody := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature:    0.2,
		ResponseFormat: &responseFormat{Type: "json_object"},
		Stream:         false,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	callStart := time.Now()

	const maxAttempts = 3
	var lastErr error
	var lastContent string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		content, retryable, err := c.doRequest(ctx, payload)
		if err == nil {
			lastContent = content
			decoded := extractJSONObject(content)
			if err := json.Unmarshal([]byte(decoded), out); err != nil {
				// 部分 OpenAI 兼容模型会在 JSON 字符串值中直接输出双引号，
				// 例如："reasoning":"点击"岗位"按钮"。先严格解析，失败后
				// 只修复词法上不可能是字符串结束符的引号，再交回标准库验证。
				if repaired := repairUnescapedStringQuotes(decoded); repaired != decoded {
					if repairErr := json.Unmarshal([]byte(repaired), out); repairErr == nil {
						slog.Warn("ai: 已修复模型输出中的未转义字符串引号")
						RecordLLMCall(LLMCallLog{
							Timestamp:    callStart,
							Model:        c.cfg.Model,
							Kind:         callerKind(2),
							SystemPrompt: systemPrompt,
							UserPrompt:   userPrompt,
							Response:     content,
							DurationMS:   time.Since(callStart).Milliseconds(),
							Attempts:     attempt,
							Error:        "",
						})
						return nil
					}
				}
				lastErr = fmt.Errorf("ai: 模型返回无法解析为目标结构: %w", err)
				// 输出格式错误也重试一次，可能是偶发截断。
				if attempt < maxAttempts {
					sleepBackoff(ctx, attempt)
					continue
				}
				break
			}
			// 记录成功调用。
			RecordLLMCall(LLMCallLog{
				Timestamp:    callStart,
				Model:        c.cfg.Model,
				Kind:         callerKind(2),
				SystemPrompt: systemPrompt,
				UserPrompt:   userPrompt,
				Response:     content,
				DurationMS:   time.Since(callStart).Milliseconds(),
				Attempts:     attempt,
				Error:        "",
			})
			return nil
		}

		lastErr = err
		if !retryable || attempt == maxAttempts {
			break
		}
		slog.Warn("ai: 调用失败，准备重试", "attempt", attempt, "error", err.Error())
		sleepBackoff(ctx, attempt)
	}

	// 记录失败调用。
	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	RecordLLMCall(LLMCallLog{
		Timestamp:    callStart,
		Model:        c.cfg.Model,
		Kind:         callerKind(2),
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		Response:     lastContent,
		DurationMS:   time.Since(callStart).Milliseconds(),
		Attempts:     maxAttempts,
		Error:        errMsg,
	})
	return lastErr
}

// repairUnescapedStringQuotes 修复 JSON 字符串值中模型漏写反斜杠的双引号。
// 只把“不可能结束当前字符串”的引号改写成 \"；真正的 JSON 语法仍由
// json.Unmarshal 复验，调用方绝不因修复器本身而接受不合法的结构。
func repairUnescapedStringQuotes(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false
	stringIsKey := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch != '"' || isEscapedQuote(raw, i) {
			out.WriteByte(ch)
			continue
		}

		if !inString {
			inString = true
			stringIsKey = jsonStringStartsKey(raw, i)
			out.WriteByte(ch)
			continue
		}

		next := nextNonSpaceByte(raw, i+1)
		closes := (stringIsKey && next == ':') || (!stringIsKey && (next == 0 || next == ',' || next == '}' || next == ']'))
		if closes {
			inString = false
			out.WriteByte(ch)
			continue
		}
		out.WriteString(`\"`)
	}
	return out.String()
}

func isEscapedQuote(s string, index int) bool {
	slashes := 0
	for i := index - 1; i >= 0 && s[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 1
}

func jsonStringStartsKey(s string, quote int) bool {
	for i := quote - 1; i >= 0; i-- {
		switch s[i] {
		case ' ', '\n', '\r', '\t':
			continue
		case '{', ',':
			return true
		default:
			return false
		}
	}
	return false
}

func nextNonSpaceByte(s string, start int) byte {
	for i := start; i < len(s); i++ {
		switch s[i] {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return s[i]
		}
	}
	return 0
}

// doRequest 执行单次 HTTP 请求，返回内容与是否可重试。
func (c *Client) doRequest(ctx context.Context, payload []byte) (content string, retryable bool, err error) {
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 网络错误可重试。
		return "", true, fmt.Errorf("ai: 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", true, err
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		// 注意：错误信息不含 API Key。
		return "", true, fmt.Errorf("ai: 上游返回 %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return "", false, fmt.Errorf("ai: 上游返回 %d: %s", resp.StatusCode, sanitizeUpstreamError(body))
	}

	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", true, fmt.Errorf("ai: 响应解析失败: %w", err)
	}
	if parsed.Error != nil {
		return "", false, fmt.Errorf("ai: 上游错误: %s", parsed.Error.Type)
	}
	if len(parsed.Choices) == 0 {
		return "", true, errors.New("ai: 上游未返回任何结果")
	}

	slog.Debug("ai: 调用完成", "total_tokens", parsed.Usage.TotalTokens)
	return parsed.Choices[0].Message.Content, false, nil
}

func sanitizeUpstreamError(body []byte) string {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return "空响应"
	}
	if len(msg) > 500 {
		msg = msg[:500] + "...[截断]"
	}
	return security.RedactText(msg)
}

// sleepBackoff 指数退避等待。
func sleepBackoff(ctx context.Context, attempt int) {
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// extractJSONObject 从可能包含 Markdown 代码块的文本中提取 JSON 对象。
func extractJSONObject(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}
	// 去掉 ```json ... ``` 包裹。
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return "{}"
}

// truncateRunes 按字符数截断，避免切断多字节字符。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\n...[内容过长已截断]"
}
