package source

import (
	"net/url"
	"strings"
	"time"
)

// extractHost 返回 URL 的主机名。
func extractHost(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// dateLayouts 是常见的时间格式，按尝试顺序排列。
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"2006/01/02",
	"2006年01月02日",
	"01/02/2006",
	"Jan 2, 2006",
	"2 Jan 2006",
}

// parseLooseDate 尽力解析各种日期格式，失败返回 nil。
// 绝不猜测：解析不出来就是未知。
func parseLooseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.UTC()
			// 排除明显异常的时间。
			if u.Year() < 2000 || u.Year() > 2100 {
				return nil
			}
			return &u
		}
	}
	return nil
}
