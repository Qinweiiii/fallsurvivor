package security

import "testing"

/**
 * 脱敏的银行卡判定测试。
 *
 * 锁住一个具体教训：原先规则是「13-19 位连续数字」即当卡号抹掉，
 * 结果把快手的 recruitSubProjectCodes=20271779425607 也抹了，
 * 而这个 ID 是拼详情页 URL 的关键——过度脱敏破坏了采集能力。
 *
 * 因此两个方向都必须守住：
 *   1. 真卡号（通过 Luhn）仍要抹掉——这是安全底线，不能为便利让步；
 *   2. 业务 ID（不通过 Luhn）必须原样保留。
 */
func TestRedactTextBankCard(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "真实卡号（Luhn 通过）必须抹掉",
			// 4111111111111111 是各支付网关公开的测试卡号，通过 Luhn。
			in:   "卡号 4111111111111111 请核对",
			want: "卡号 [REDACTED_CARD] 请核对",
		},
		{
			name: "带空格分隔的真实卡号也要抹掉",
			in:   "4111 1111 1111 1111",
			want: "[REDACTED_CARD]",
		},
		{
			name: "快手业务 ID 不应被误抹",
			in:   "#/campus/jobs?recruitSubProjectCodes=20271779425607",
			want: "#/campus/jobs?recruitSubProjectCodes=20271779425607",
		},
		{
			name: "13 位业务 ID 不应被误抹",
			in:   "postId=1234567890123",
			want: "postId=1234567890123",
		},
		{
			name: "18 位身份证仍严格抹除",
			in:   "身份证 11010519491231002X",
			want: "身份证 [REDACTED_ID]",
		},
		{
			name: "Bearer 令牌仍抹除",
			in:   "Authorization: Bearer abc.def-123",
			want: "Authorization: [REDACTED_TOKEN]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactText(tc.in); got != tc.want {
				t.Errorf("RedactText(%q) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestLooksLikeBankCard 单独覆盖 Luhn 判定的边界。
func TestLooksLikeBankCard(t *testing.T) {
	cases := []struct {
		digits string
		want   bool
	}{
		{"4111111111111111", true},  // 标准测试卡号
		{"5500005555555559", true},  // 另一组公开测试卡号
		{"20271779425607", false},   // 快手业务 ID（14 位，不过 Luhn）
		{"1234567890123", false},    // 随机 13 位
		{"411111111111111", false},  // 位数合法但 Luhn 不通过
		{"123456789012", false},     // 12 位，短于下限
		{"12345678901234567890", false}, // 20 位，超过上限
	}

	for _, tc := range cases {
		if got := looksLikeBankCard(tc.digits); got != tc.want {
			t.Errorf("looksLikeBankCard(%q) = %v，期望 %v", tc.digits, got, tc.want)
		}
	}
}
