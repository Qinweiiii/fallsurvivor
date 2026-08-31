package safefetch

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",      // 回环
		"0.0.0.0",        // 未指定
		"10.1.2.3",       // 私有
		"172.16.0.1",     // 私有
		"192.168.1.1",    // 私有
		"169.254.169.254", // 云元数据服务
		"100.64.0.1",     // CGNAT
		"224.0.0.1",      // 组播
		"::1",            // IPv6 回环
		"fe80::1",        // IPv6 链路本地
		"fc00::1",        // IPv6 唯一本地
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("IP %s 必须被禁止访问", s)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("公网 IP %s 不应被禁止", s)
		}
	}

	if !isBlockedIP(nil) {
		t.Error("nil IP 必须被拒绝")
	}
}

func TestIPv4MappedIPv6IsBlocked(t *testing.T) {
	// ::ffff:127.0.0.1 是 IPv4-mapped 形式，必须按 IPv4 规则拦截。
	ip := net.ParseIP("::ffff:127.0.0.1")
	if !isBlockedIP(ip) {
		t.Error("IPv4-mapped 的回环地址必须被拒绝")
	}
}

func TestValidateRejectsBadScheme(t *testing.T) {
	c := New(true)
	ctx := context.Background()

	bad := []string{
		"file:///etc/passwd",
		"ftp://example.com/x",
		"gopher://example.com",
		"data:text/plain;base64,aGk=",
		"javascript:alert(1)",
	}
	for _, u := range bad {
		if _, _, err := c.Validate(ctx, u); !errors.Is(err, ErrSchemeNotAllowed) {
			t.Errorf("URL %q 应因协议不被允许而拒绝，实际错误: %v", u, err)
		}
	}
}

func TestValidateRejectsNonStandardPort(t *testing.T) {
	c := New(true)
	ctx := context.Background()

	bad := []string{
		"http://example.com:8080/x",
		"https://example.com:3306/x",
		"http://example.com:22/x",
	}
	for _, u := range bad {
		if _, _, err := c.Validate(ctx, u); !errors.Is(err, ErrPortNotAllowed) {
			t.Errorf("URL %q 应因端口不被允许而拒绝，实际错误: %v", u, err)
		}
	}
}

func TestValidateRejectsInternalLiteralIP(t *testing.T) {
	c := New(true)
	ctx := context.Background()

	bad := []string{
		"http://127.0.0.1/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.0.1/",
		"http://[::1]/",
	}
	for _, u := range bad {
		if _, _, err := c.Validate(ctx, u); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("URL %q 应因指向内网而拒绝，实际错误: %v", u, err)
		}
	}
}

func TestDisabledClientRejectsAll(t *testing.T) {
	c := New(false)
	if _, err := c.Get(context.Background(), "https://example.com"); !errors.Is(err, ErrDisabled) {
		t.Errorf("关闭出网时应拒绝所有请求，实际错误: %v", err)
	}
}
