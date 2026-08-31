// Package security 集中定义敏感信息的判定与脱敏逻辑。
//
// 这是整个系统关于「什么算敏感」的唯一权威来源。
// 后端表单映射、Playwright Worker、日志输出都必须引用本包，
// 禁止在其他地方另写一份黑名单。
package security

import "strings"

// SensitiveKeywords 是必须留空、必须由用户手工处理的字段关键词。
// 依据产品文档第 14 节与浏览器自动化文档第 8 节。
var SensitiveKeywords = []string{
	// 证件类
	"身份证", "身份証", "证件号", "证件号码", "idcard", "id card", "id_card", "id number",
	"护照", "passport", "港澳通行证", "台胞证", "军官证", "社保卡", "社会保障号",
	"ssn", "social security",
	// 金融类
	"银行卡", "银行账号", "银行帐号", "开户行", "卡号", "bank card", "bank account",
	"credit card", "信用卡", "支付", "payment", "iban", "cvv",
	// 凭据类
	"密码", "口令", "password", "passwd", "pwd", "secret", "token", "api key", "apikey",
	// 验证类
	"验证码", "校验码", "动态码", "captcha", "verification code", "verify code",
	"短信验证", "sms code", "邮箱验证", "email code", "otp", "one-time",
	"mfa", "二次验证", "双因素", "two factor", "2fa", "authenticator",
	// 生物特征
	"人脸", "刷脸", "face id", "face recognition", "facial", "指纹", "fingerprint",
	"活体", "liveness", "声纹", "虹膜",
	// 高度隐私
	"民族", "宗教", "政治面貌", "党派", "婚姻状况", "生育", "病史", "残疾",
	"犯罪记录", "征信",
}

// IsSensitiveField 报告某个表单字段（依据 label / name / placeholder）是否敏感。
// 任一命中即视为敏感，采取「宁可多跳过，不可误填」的策略。
func IsSensitiveField(parts ...string) bool {
	for _, p := range parts {
		lower := strings.ToLower(strings.TrimSpace(p))
		if lower == "" {
			continue
		}
		for _, kw := range SensitiveKeywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}

// SensitiveInputTypes 是 HTML 层面即判定为敏感的 input type。
var SensitiveInputTypes = map[string]bool{
	"password": true,
	"file":     false, // 文件上传本身不敏感，但由业务另行控制
}

// IsSensitiveInputType 报告该 input type 是否必须跳过。
func IsSensitiveInputType(inputType string) bool {
	return SensitiveInputTypes[strings.ToLower(strings.TrimSpace(inputType))]
}

// MatchedKeyword 返回命中的第一个敏感关键词，用于向用户解释为何跳过。
// 未命中时返回空字符串。
func MatchedKeyword(parts ...string) string {
	for _, p := range parts {
		lower := strings.ToLower(strings.TrimSpace(p))
		if lower == "" {
			continue
		}
		for _, kw := range SensitiveKeywords {
			if strings.Contains(lower, kw) {
				return kw
			}
		}
	}
	return ""
}
