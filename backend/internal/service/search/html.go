package search

import (
	"regexp"
	"strings"
)

var (
	// 移除 script / style / noscript 等不可见内容。
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|noscript|iframe|svg)[^>]*>.*?</(script|style|noscript|iframe|svg)>`)
	// 移除 HTML 注释。
	reComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	// 块级标签转换为换行。
	reBlockTag = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6]|section|article|ul|ol|table|thead|tbody)[^>]*>`)
	// 其余标签直接移除。
	reAnyTag = regexp.MustCompile(`<[^>]+>`)
	// 连续空行压缩。
	reMultiNewline = regexp.MustCompile(`\n{3,}`)
)

// htmlEntities 是常见 HTML 实体的解码表。
var htmlEntities = map[string]string{
	"&nbsp;": " ", "&amp;": "&", "&lt;": "<", "&gt;": ">",
	"&quot;": `"`, "&#39;": "'", "&apos;": "'", "&hellip;": "...",
	"&mdash;": "—", "&ndash;": "-", "&middot;": "·", "&#x27;": "'",
	"&#x2F;": "/", "&#160;": " ",
}

// StripHTML 把 HTML 文本转换为纯文本。
//
// 说明：此处只用于「把抓取到的页面转成可供 LLM 阅读的文本」，
// 不用于向浏览器输出内容，因此不承担 XSS 防护职责。
// 向前端输出富文本时必须使用前端的自动转义机制。
func StripHTML(html string) string {
	if html == "" {
		return ""
	}
	s := reScriptStyle.ReplaceAllString(html, " ")
	s = reComment.ReplaceAllString(s, " ")
	s = reBlockTag.ReplaceAllString(s, "\n")
	s = reAnyTag.ReplaceAllString(s, " ")

	for entity, replacement := range htmlEntities {
		s = strings.ReplaceAll(s, entity, replacement)
	}

	// 逐行清理首尾空白。
	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if t := strings.TrimSpace(line); t != "" {
			cleaned = append(cleaned, t)
		}
	}
	s = strings.Join(cleaned, "\n")
	return reMultiNewline.ReplaceAllString(s, "\n\n")
}
