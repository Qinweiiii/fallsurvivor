package executor

import "testing"

/**
 * businessError 的行为测试。
 *
 * 这些用例锁住一个具体教训：快手 open/positions/simple 缺参数时
 * 返回 HTTP 200 + {"code":40014,"message":"parameter is incorrect"}。
 * 早先版本只看 HTTP 状态，把它当成功，于是错误退化成
 * 「解析不出岗位」，自修正连续三轮都在改数组路径而非补参数。
 *
 * 因此这里既要验证「能认出业务错误」，也要验证「不误判正常响应」——
 * 误判的代价（能用的配置被判失败）比漏判更大。
 */
func TestBusinessError(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		want    string // 空串表示不应判为业务错误
	}{
		{
			name:    "快手缺参数：HTTP 200 但业务码非 0",
			payload: map[string]any{"code": float64(40014), "message": "parameter is incorrect", "result": nil},
			want:    "code=40014 parameter is incorrect",
		},
		{
			name:    "code 为 0 视为成功",
			payload: map[string]any{"code": float64(0), "data": map[string]any{}},
			want:    "",
		},
		{
			name:    "字符串成功码不误判",
			payload: map[string]any{"code": "0", "data": map[string]any{}},
			want:    "",
		},
		{
			name:    "success=false 判为失败",
			payload: map[string]any{"success": false, "msg": "无权限访问"},
			want:    "success=false 无权限访问",
		},
		{
			name:    "success=true 不误判",
			payload: map[string]any{"success": true, "data": []any{}},
			want:    "",
		},
		{
			name:    "顶层为数组时不存在业务码包装",
			payload: []any{map[string]any{"title": "后端工程师"}},
			want:    "",
		},
		{
			name:    "无状态字段的正常响应不误判",
			payload: map[string]any{"data": map[string]any{"list": []any{}}},
			want:    "",
		},
		{
			name:    "整数码不应被格式化成浮点",
			payload: map[string]any{"errno": float64(500), "errmsg": "internal"},
			want:    "errno=500 internal",
		},
		{
			name: "status 字段为 200 视为成功（HTTP 语义复用）",
			// 部分接口把 HTTP 语义搬进响应体，200 是成功而非错误码。
			payload: map[string]any{"status": float64(200), "data": []any{}},
			want:    "",
		},
		{
			name:    "美团 status=1 且消息为成功时不误判",
			payload: map[string]any{"status": float64(1), "msg": "成功", "data": []any{}},
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := businessError(tc.payload)
			if got != tc.want {
				t.Errorf("businessError() = %q，期望 %q", got, tc.want)
			}
		})
	}
}
