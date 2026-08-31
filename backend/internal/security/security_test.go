package security

import "testing"

func TestIsSensitiveFieldCoversRequiredCases(t *testing.T) {
	// 依据产品文档第 14 节与浏览器自动化文档第 8 节，
	// 这些字段必须一律留空并交给用户处理。
	mustBeSensitive := []string{
		"身份证号", "身份证号码", "居民身份证",
		"护照号", "Passport Number",
		"银行卡号", "Bank Card",
		"密码", "Password", "登录密码",
		"验证码", "短信验证码", "邮箱验证码", "Verification Code", "Captcha",
		"MFA 验证码", "Two Factor Code", "Authenticator",
		"人脸识别", "Face Recognition", "指纹",
		"政治面貌", "婚姻状况",
	}
	for _, label := range mustBeSensitive {
		if !IsSensitiveField(label) {
			t.Errorf("字段 %q 必须被判定为敏感", label)
		}
	}
}

func TestIsSensitiveFieldAllowsNormalFields(t *testing.T) {
	// 这些是允许自动填写的普通求职信息。
	normal := []string{
		"姓名", "手机号", "邮箱", "学校", "专业", "最高学历",
		"毕业时间", "期望城市", "项目经历", "实习经历", "自我评价",
	}
	for _, label := range normal {
		if IsSensitiveField(label) {
			t.Errorf("普通字段 %q 不应被判定为敏感", label)
		}
	}
}

func TestIsSensitiveFieldChecksAllParts(t *testing.T) {
	// label 为空但 name 命中时也必须拦下。
	if !IsSensitiveField("", "id_card_no", "") {
		t.Error("应根据 name 属性识别敏感字段")
	}
	if !IsSensitiveField("", "", "请输入短信验证码") {
		t.Error("应根据 placeholder 识别敏感字段")
	}
}

func TestIsSensitiveInputType(t *testing.T) {
	if !IsSensitiveInputType("password") {
		t.Error("password 类型必须视为敏感")
	}
	if !IsSensitiveInputType("PASSWORD") {
		t.Error("类型判断应忽略大小写")
	}
	if IsSensitiveInputType("text") {
		t.Error("text 类型不应视为敏感")
	}
}

func TestRedactTextRemovesIDAndCard(t *testing.T) {
	in := "我的身份证是 11010519900307123X，银行卡 6222021234567890123"
	out := RedactText(in)
	if contains(out, "11010519900307123X") {
		t.Errorf("身份证号未被抹除: %q", out)
	}
	if contains(out, "6222021234567890123") {
		t.Errorf("银行卡号未被抹除: %q", out)
	}
}

func TestRedactTextRemovesCredentials(t *testing.T) {
	in := "Authorization: Bearer abc123XYZ_token; Cookie: session_id=deadbeef"
	out := RedactText(in)
	if contains(out, "abc123XYZ_token") {
		t.Errorf("Bearer token 未被抹除: %q", out)
	}
	if contains(out, "deadbeef") {
		t.Errorf("Cookie 未被抹除: %q", out)
	}
}

func TestRedactPIIRemovesContactInfo(t *testing.T) {
	in := "联系我：13800138000，邮箱 me@example.com"
	out := RedactPII(in)
	if contains(out, "13800138000") {
		t.Errorf("手机号未被抹除: %q", out)
	}
	if contains(out, "me@example.com") {
		t.Errorf("邮箱未被抹除: %q", out)
	}
}

func TestRedactTextKeepsNormalContent(t *testing.T) {
	in := "岗位要求：熟悉 Go 语言，具备高并发经验"
	if got := RedactText(in); got != in {
		t.Errorf("正常文本不应被修改: %q", got)
	}
}

func TestMaskTail(t *testing.T) {
	if got := MaskTail("13800138000", 4); got != "*******8000" {
		t.Errorf("MaskTail 结果错误: %q", got)
	}
	if got := MaskTail("abc", 10); got != "***" {
		t.Errorf("保留位数超过长度时应全部掩码: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
