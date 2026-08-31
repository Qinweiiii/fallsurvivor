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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 限制常量。
const (
	maxRedirects    = 3
	maxBodyBytes    = 4 << 20 // 4 MiB
	defaultTimeout  = 20 * time.Second
	dialTimeout     = 5 * time.Second
	handshakeTimeout = 8 * time.Second
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

// Get 抓取指定 URL。每一跳重定向都会重新走完整校验流程。
func (c *Client) Get(ctx context.Context, rawURL string) (*Result, error) {
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

		res, location, err := c.doOnce(ctx, u, ips)
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

// doOnce 执行单次请求，把已校验的 IP 固定用于拨号。
// 若响应为 3xx，则通过 location 返回跳转目标而不自动跟随。
func (c *Client) doOnce(ctx context.Context, u *url.URL, ips []net.IP) (*Result, string, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "JobHuntOS/1.0 (personal job-search assistant)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

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
