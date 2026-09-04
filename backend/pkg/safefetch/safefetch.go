// Package safefetch 提供带 SSRF 防护的 HTTP 抓取能力。
//
// 抓取任意外部 URL（岗位详情页、官方招聘页）时必须使用本包，
// 防护措施依次为：
//  1. 协议白名单，仅允许 http / https；
//  2. 端口白名单，仅允许 80 / 443；
//  3. DNS 解析后校验全部 IP，拒绝私有网段、回环、链路本地、CGNAT、组播等；
//  4. 将校验通过的 IP 固定用于实际连接，杜绝 DNS rebinding；
//  5. 禁用自动重定向，逐跳手动校验；
//  6. 强制超时并限制响应体大小。
package safefetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// 限制常量。
const (
	maxRedirects     = 3
	maxBodyBytes     = 4 << 20 // 4 MiB
	defaultTimeout   = 20 * time.Second
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 8 * time.Second
	maxExtraHeaders  = 12
)

// 错误定义。
var (
	ErrDisabled         = errors.New("safefetch: 出网抓取已被配置关闭")
	ErrSchemeNotAllowed = errors.New("safefetch: 仅允许 http/https 协议")
	ErrPortNotAllowed   = errors.New("safefetch: 仅允许 80/443 端口")
	ErrBlockedAddress   = errors.New("safefetch: 目标地址位于禁止访问的网段")
	ErrTooManyRedirects = errors.New("safefetch: 重定向次数过多")
	ErrBodyTooLarge     = errors.New("safefetch: 响应体超出大小限制")
)

// blockedCIDRs 覆盖 IPv4/IPv6 全部需要拒绝的特殊网段。
var blockedCIDRs = func() []*net.IPNet {
	raw := []string{
		"0.0.0.0/8",          // 当前网络
		"10.0.0.0/8",         // 私有
		"100.64.0.0/10",      // CGNAT
		"127.0.0.0/8",        // 回环
		"169.254.0.0/16",     // 链路本地（含云元数据 169.254.169.254）
		"172.16.0.0/12",      // 私有
		"192.0.0.0/24",       // IETF 协议分配
		"192.0.2.0/24",       // 文档用
		"192.88.99.0/24",     // 6to4 中继
		"192.168.0.0/16",     // 私有
		"198.18.0.0/15",      // 基准测试
		"198.51.100.0/24",    // 文档用
		"203.0.113.0/24",     // 文档用
		"224.0.0.0/4",        // 组播
		"240.0.0.0/4",        // 保留
		"255.255.255.255/32", // 广播
		"::/128",             // 未指定
		"::1/128",            // 回环
		"64:ff9b::/96",       // NAT64
		"100::/64",           // Discard-Only
		"2001:db8::/32",      // 文档用
		"fc00::/7",           // 唯一本地
		"fe80::/10",          // 链路本地
		"ff00::/8",           // 组播
	}
	nets := make([]*net.IPNet, 0, len(raw))
	for _, c := range raw {
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// isBlockedIP 报告该 IP 是否禁止访问。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// IPv4-mapped IPv6 按 IPv4 规则再校验一次。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Client 是带 SSRF 防护的抓取客户端。
type Client struct {
	enabled  bool
	timeout  time.Duration
	resolver *net.Resolver
}

// New 创建客户端。enabled 为 false 时所有请求都会被拒绝。
func New(enabled bool) *Client {
	return &Client{enabled: enabled, timeout: defaultTimeout, resolver: net.DefaultResolver}
}

// Validate 只做 URL 与地址校验，不发起请求。
// 返回校验通过的 URL 与可用于连接的 IP 列表。
func (c *Client) Validate(ctx context.Context, rawURL string) (*url.URL, []net.IP, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil, fmt.Errorf("safefetch: URL 解析失败: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, nil, ErrSchemeNotAllowed
	}

	host := u.Hostname()
	if host == "" {
		return nil, nil, ErrBlockedAddress
	}

	// 端口白名单。
	port := u.Port()
	switch port {
	case "":
	case "80":
		if scheme != "http" {
			return nil, nil, ErrPortNotAllowed
		}
	case "443":
		if scheme != "https" {
			return nil, nil, ErrPortNotAllowed
		}
	default:
		return nil, nil, ErrPortNotAllowed
	}

	// 字面量 IP 直接校验。
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return nil, nil, ErrBlockedAddress
		}
		return u, []net.IP{ip}, nil
	}

	addrs, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, nil, fmt.Errorf("safefetch: DNS 解析失败: %w", err)
	}
	if len(addrs) == 0 {
		return nil, nil, ErrBlockedAddress
	}

	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		// 任一解析结果落在黑名单即整体拒绝，避免多记录绕过。
		if isBlockedIP(a.IP) {
			return nil, nil, ErrBlockedAddress
		}
		ips = append(ips, a.IP)
	}
	return u, ips, nil
}

// Result 是一次抓取的结果。
type Result struct {
	FinalURL    string
	StatusCode  int
	ContentType string
	Body        []byte
}

/**
 * sensitiveHeaderPattern 匹配凭证类请求头名。
 *
 * 调用方可以传入额外请求头（站点 Recipe 沉淀的渠道 / 语言等语义头），
 * 但本包会**无条件拒绝**凭证类头部。理由：
 *   1. 出网抓取不应携带任何身份凭证——一旦目标站点被劫持或发生重定向，
 *      凭证就会泄漏给第三方；
 *   2. 需要登录态才能读的接口应走浏览器会话，而不是脱离会话直连。
 * 这是纵深防御的最后一道：上游各层已过滤，此处再拦一次。
 */
var sensitiveHeaderPattern = regexp.MustCompile(
	`(?i)(cookie|auth|token|secret|password|session|csrf|signature|sign|credential|key)`)

// 保留头：这些由本包自己设置，不允许调用方覆盖，
// 否则可能被用来伪造 Host 或破坏请求体解析。
var reservedHeaders = map[string]bool{
	"host":              true,
	"content-length":    true,
	"transfer-encoding": true,
	"connection":        true,
	"upgrade":           true,
}

/**
 * applyExtraHeaders 把调用方提供的额外请求头写入请求。
 *
 * 过滤规则（任一命中即丢弃该头）：
 *   - 命中凭证类关键词；
 *   - 属于协议保留头；
 *   - 名或值为空、值过长（>200，多半是编码凭证或指纹）；
 *   - 含控制字符或换行（防 header 注入）。
 *
 * 最多接受 maxExtraHeaders 个，避免请求头被塞爆。
 */
func applyExtraHeaders(req *http.Request, extra map[string]string) {
	if len(extra) == 0 {
		return
	}
	applied := 0
	for k, v := range extra {
		name := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(v)
		if name == "" || val == "" || len(val) > 200 {
			continue
		}
		if sensitiveHeaderPattern.MatchString(name) || reservedHeaders[name] {
			continue
		}
		// Header 注入防护：名与值都不允许出现换行或控制字符。
		if strings.ContainsAny(name, "\r\n:") || strings.ContainsAny(val, "\r\n") {
			continue
		}
		req.Header.Set(name, val)
		applied++
		if applied >= maxExtraHeaders {
			return
		}
	}
}

// Get 抓取指定 URL。每一跳重定向都会重新走完整校验流程。
//
// headers 为可选的额外请求头（可为 nil）。凭证类头部会被本包无条件丢弃。
func (c *Client) Get(ctx context.Context, rawURL string, headers map[string]string) (*Result, error) {
	if !c.enabled {
		return nil, ErrDisabled
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	current := rawURL
	for hop := 0; hop <= maxRedirects; hop++ {
		u, ips, err := c.Validate(ctx, current)
		if err != nil {
			return nil, err
		}

		res, location, err := c.doOnceWithBody(ctx, u, ips, nil, nil, headers)
		if err != nil {
			return nil, err
		}
		if location == "" {
			return res, nil
		}

		// 相对跳转需要基于当前 URL 解析。
		next, err := u.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("safefetch: 重定向地址非法: %w", err)
		}
		current = next.String()
	}
	return nil, ErrTooManyRedirects
}

// PostJSON 以 application/json 提交请求体。
//
// 与 Get / PostForm 一样逐跳做 SSRF 校验与 IP 固定，不自动跟随重定向。
//
// body 的来源：站点 Recipe 中由 Exploration Agent 从**真实观测请求**沉淀的
// 请求体，而非运行时用户输入拼接。执行器只负责原样复现，
// 因此本包只校验其为合法 JSON，不再解析内容。
//
// body 为 nil 或空时发送 "{}"，避免接口因缺少对象而报错。
// 重定向时按标准语义降级为 GET，不把请求体带到新地址。
func (c *Client) PostJSON(ctx context.Context, rawURL string, body []byte, headers map[string]string) (*Result, error) {
	if !c.enabled {
		return nil, ErrDisabled
	}
	payload := bytes.TrimSpace(body)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	// 提前校验合法性，避免把畸形 JSON 发到网络。
	if !json.Valid(payload) {
		return nil, fmt.Errorf("safefetch: POST 请求体不是合法 JSON")
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	current := rawURL
	for hop := 0; hop <= maxRedirects; hop++ {
		u, ips, err := c.Validate(ctx, current)
		if err != nil {
			return nil, err
		}

		// 仅首跳携带请求体；重定向后降级为 GET。
		var hopBody []byte
		if hop == 0 {
			hopBody = payload
		}
		res, location, err := c.doOnceWithBody(ctx, u, ips, url.Values{}, hopBody, headers)
		if err != nil {
			return nil, err
		}
		if location == "" {
			return res, nil
		}

		next, err := u.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("safefetch: 重定向地址非法: %w", err)
		}
		current = next.String()
	}
	return nil, ErrTooManyRedirects
}

// PostForm 以 application/x-www-form-urlencoded 提交表单。
//
// 与 Get 一样逐跳做 SSRF 校验与 IP 固定，不自动跟随重定向。
//
// form 的键必须来自调用方白名单（如站点 Recipe 沉淀的请求体字段），
// 本函数不做参数过滤——调用方负责保证键名可信。
// 值会经 url.Values.Encode 转义，不会破坏请求结构。
//
// 注意：重定向时按标准做法改为 GET（303/302 语义），
// 不把表单体带到新地址，避免把数据泄漏到跳转目标。
func (c *Client) PostForm(ctx context.Context, rawURL string, form url.Values, headers map[string]string) (*Result, error) {
	if !c.enabled {
		return nil, ErrDisabled
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	current := rawURL
	for hop := 0; hop <= maxRedirects; hop++ {
		u, ips, err := c.Validate(ctx, current)
		if err != nil {
			return nil, err
		}

		// 仅首跳携带表单；重定向后降级为 GET。
		var body url.Values
		if hop == 0 {
			body = form
		}
		res, location, err := c.doOnceWithBody(ctx, u, ips, body, nil, headers)
		if err != nil {
			return nil, err
		}
		if location == "" {
			return res, nil
		}

		next, err := u.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("safefetch: 重定向地址非法: %w", err)
		}
		current = next.String()
	}
	return nil, ErrTooManyRedirects
}

// doOnceWithBody 执行单次请求，把已校验的 IP 固定用于拨号。
//   - jsonBody 非空 → POST + application/json（优先级最高）；
//   - 否则 form 非空 → POST + application/x-www-form-urlencoded；
//   - 否则 → GET。
//
// extraHeaders 中的凭证类与保留头会被丢弃（见 applyExtraHeaders）。
//
// 若响应为 3xx，则通过 location 返回跳转目标而不自动跟随。
func (c *Client) doOnceWithBody(
	ctx context.Context,
	u *url.URL,
	ips []net.IP,
	form url.Values,
	jsonBody []byte,
	extraHeaders map[string]string,
) (*Result, string, error) {
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := &net.Dialer{Timeout: dialTimeout}
			var lastErr error
			for _, ip := range ips {
				// 再次确认，防止调用方传入未校验地址。
				if isBlockedIP(ip) {
					lastErr = ErrBlockedAddress
					continue
				}
				conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = ErrBlockedAddress
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout: handshakeTimeout,
		DisableKeepAlives:   true,
		ForceAttemptHTTP2:   false,
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		// 禁用自动重定向，由调用方逐跳校验。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	// 优先级：jsonBody > form > GET。
	var reqBody io.Reader
	method := http.MethodGet
	contentType := ""
	switch {
	case len(jsonBody) > 0:
		method = http.MethodPost
		contentType = "application/json"
		reqBody = bytes.NewReader(jsonBody)
	case len(form) > 0:
		method = http.MethodPost
		contentType = "application/x-www-form-urlencoded"
		reqBody = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "JobHuntOS/1.0 (personal job-search assistant)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	// 调用方的额外头先应用，Content-Type 随后覆盖——
	// 请求体类型由本包按实际 body 决定，不允许被外部头改写。
	applyExtraHeaders(req, extraHeaders)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if loc := resp.Header.Get("Location"); loc != "" {
			return nil, loc, nil
		}
	}

	limited := io.LimitReader(resp.Body, maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxBodyBytes {
		return nil, "", ErrBodyTooLarge
	}

	return &Result{
		FinalURL:    resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
	}, "", nil
}
