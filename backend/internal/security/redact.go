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

// RedactText 抹掉文本中的证件号、银行卡、凭据等敏感串。
// 用于：写日志之前、把 JD/表单文本发给 LLM 之前。
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
	s = reBankCard.ReplaceAllString(s, "[REDACTED_CARD]")
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
