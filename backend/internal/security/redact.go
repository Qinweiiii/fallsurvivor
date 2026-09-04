package security

import (
	"regexp"
	"strings"
)

// 用于在文本进入 LLM 或日志之前抹掉可识别的敏感串。
var (
	// 中国大陆身份证：18 位（末位可为 X）或 15 位。
	reIDCard = regexp.MustCompile(`\b\d{17}[\dXx]\b|\b\d{15}\b`)
	// 银行卡号：13~19 位连续数字，允许空格/短横分组。
	reBankCard = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
	// 手机号：中国大陆 11 位。
	reMobile = regexp.MustCompile(`\b1[3-9]\d{9}\b`)
	// 邮箱。
	reEmail = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	// 常见 Bearer / Cookie 片段。
	reBearer = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]+`)
	reCookie = regexp.MustCompile(`(?i)(cookie|set-cookie|session[_-]?id)\s*[:=]\s*[^\s;,]+`)
)

// looksLikeBankCard 判断一串数字是否可能是真实银行卡号（Luhn 校验）。
//
// 为什么需要它：原先卡号规则是「13-19 位连续数字」，会把站点自己的
// 业务 ID 一并抹掉。实测快手 recruitSubProjectCodes=20271779425607
// 被替换成 [REDACTED_CARD]，而该 ID 正是拼详情页 URL 的关键——
// 过度脱敏直接破坏了采集能力。
//
// Luhn 是主流卡组织的强制校验规则，随机业务 ID 通过它的概率约 1/10，
// 因此用它做二次确认能在几乎不牺牲安全性的前提下大幅降低误伤。
//
// 与 worker-browser/src/common/sensitive_guard.ts 的实现保持一致，
// 修改时必须同步两处。
func looksLikeBankCard(digits string) bool {
	n := len(digits)
	if n < 13 || n > 19 {
		return false
	}
	sum := 0
	double := false
	for i := n - 1; i >= 0; i-- {
		c := digits[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// stripCardSeparators 去掉卡号常见的空格与连字符分隔符。
func stripCardSeparators(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != ' ' && c != '-' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// RedactText 抹掉文本中的证件号、银行卡、凭据等敏感串。
// 用于：写日志之前、把 JD/表单文本发给 LLM 之前。
//
// 银行卡采用「格式匹配 + Luhn 校验」两步判定，避免把业务 ID 当卡号抹掉。
// 证件号保持严格抹除——其格式特征足够明确，误伤风险低且泄漏后果更重。
//
// 注意：手机号与邮箱属于用户主动提供的求职信息，不在此处抹除，
// 需要抹除时使用 RedactPII。
func RedactText(s string) string {
	if s == "" {
		return s
	}
	s = reBearer.ReplaceAllString(s, "[REDACTED_TOKEN]")
	s = reCookie.ReplaceAllString(s, "[REDACTED_COOKIE]")
	s = reIDCard.ReplaceAllString(s, "[REDACTED_ID]")
	s = reBankCard.ReplaceAllStringFunc(s, func(match string) string {
		if looksLikeBankCard(stripCardSeparators(match)) {
			return "[REDACTED_CARD]"
		}
		return match
	})
	return s
}

// RedactPII 在 RedactText 之上进一步抹掉手机号与邮箱，用于日志输出。
func RedactPII(s string) string {
	s = RedactText(s)
	s = reMobile.ReplaceAllString(s, "[REDACTED_PHONE]")
	s = reEmail.ReplaceAllString(s, "[REDACTED_EMAIL]")
	return s
}

// MaskTail 保留末尾若干位，其余以星号替代，用于界面展示。
func MaskTail(s string, keep int) string {
	r := []rune(s)
	if keep <= 0 || len(r) <= keep {
		return strings.Repeat("*", len(r))
	}
	return strings.Repeat("*", len(r)-keep) + string(r[len(r)-keep:])
}
