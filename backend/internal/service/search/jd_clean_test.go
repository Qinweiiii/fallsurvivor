package search

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

func TestCleanJDText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "剔除导航与页脚噪声",
			in:   "后台开发工程师。\n首页\n登录\n注册\n负责服务端研发。\n了解更多\n版权所有\n立即申请",
			want: "后台开发工程师。\n负责服务端研发。",
		},
		{
			name: "合并被换行切断的段落",
			in:   "负责项目团队CICD流水线建设和日\n常维护；支持项目代码管理。",
			want: "负责项目团队CICD流水线建设和日常维护；支持项目代码管理。",
		},
		{
			name: "列表项保持独立行",
			in:   "岗位要求：\n1、熟悉Go语言\n2、熟悉MySQL",
			want: "岗位要求：\n1、熟悉Go语言\n2、熟悉MySQL",
		},
		{
			name: "丢弃纯分隔符号行与URL行",
			in:   "职位介绍：\n---\nhttps://example.com\n1、负责后台开发",
			want: "职位介绍：\n1、负责后台开发",
		},
		{
			name: "营销文案整行剔除",
			in:   "加入腾讯的N个理由\n关心成长\n负责AI Agent研发",
			want: "负责AI Agent研发",
		},
		{
			name: "空输入返回空",
			in:   "",
			want: "",
		},
		{
			name: "全噪声返回空",
			in:   "首页\n登录\n注册\n了解更多",
			want: "",
		},
		{
			name: "中英文相邻补空格",
			in:   "熟悉Go\n语言开发",
			want: "熟悉Go 语言开发",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CleanJDText(c.in)
			if got != c.want {
				t.Errorf("CleanJDText() =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

// 验证白名单内的键被正确写入，白名单外的键被忽略。
func TestApplySourceMetaIgnoresUnknownKeys(t *testing.T) {
	j := &model.Job{}
	applySourceMeta(j, map[string]string{
		"Department": "WXG",
		"Business":   "微信支付",
		"Company":    "腾讯",
		"Location":   "北京",
		"Evil":       "should-not-apply",
	})
	if j.Department != "WXG" {
		t.Errorf("Department = %q, want WXG", j.Department)
	}
	if j.Business != "微信支付" {
		t.Errorf("Business = %q, want 微信支付", j.Business)
	}
	if j.CompanyName != "腾讯" {
		t.Errorf("CompanyName = %q, want 腾讯", j.CompanyName)
	}
	if j.Location != "北京" {
		t.Errorf("Location = %q, want 北京", j.Location)
	}
}

// 验证空值不覆盖已有内容。
func TestApplySourceMetaSkipsEmpty(t *testing.T) {
	j := &model.Job{Department: "原有部门"}
	applySourceMeta(j, map[string]string{"Department": ""})
	if j.Department != "原有部门" {
		t.Errorf("空 Meta 不应覆盖原值，实际 %q", j.Department)
	}
	// nil map 不应 panic。
	applySourceMeta(j, nil)
	if j.Department != "原有部门" {
		t.Errorf("nil Meta 不应改动值，实际 %q", j.Department)
	}
}
