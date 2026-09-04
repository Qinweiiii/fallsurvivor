package ai

import (
	"encoding/json"
	"testing"
)

func TestMapFieldsByRuleSkipsSensitive(t *testing.T) {
	fields := []FormField{
		{Ref: "r1", Label: "姓名", Type: "text"},
		{Ref: "r2", Label: "身份证号码", Type: "text"},
		{Ref: "r3", Label: "短信验证码", Type: "text"},
		{Ref: "r4", Label: "登录密码", Type: "password"},
		{Ref: "r5", Label: "人脸识别", Type: "file"},
	}
	values := map[string]string{"basic.name": "张三"}

	resolved, remaining := MapFieldsByRule(fields, values)
	if len(remaining) != 0 {
		t.Errorf("所有字段都应被规则处理，剩余 %d 条", len(remaining))
	}

	byRef := map[string]FieldMapping{}
	for _, m := range resolved {
		byRef[m.Ref] = m
	}

	// 姓名应被填写
	if byRef["r1"].Action != ActionFill || byRef["r1"].Value != "张三" {
		t.Errorf("姓名字段应被自动填写，实际 %+v", byRef["r1"])
	}

	// 所有敏感字段必须 SKIP 且标记 sensitive
	for _, ref := range []string{"r2", "r3", "r4", "r5"} {
		m := byRef[ref]
		if m.Action != ActionSkip {
			t.Errorf("字段 %s 必须跳过，实际 action=%s", ref, m.Action)
		}
		if !m.IsSensitive {
			t.Errorf("字段 %s 必须标记为敏感", ref)
		}
		if m.Value != "" {
			t.Errorf("字段 %s 绝不能带有值", ref)
		}
		if m.Source != "" {
			t.Errorf("字段 %s 不应有映射来源", ref)
		}
	}
}

func TestMapFieldsByRuleSkipsWhenNoValue(t *testing.T) {
	fields := []FormField{{Ref: "r1", Label: "学校", Type: "text"}}
	// 用户未填写学校信息
	resolved, _ := MapFieldsByRule(fields, map[string]string{})

	if len(resolved) != 1 {
		t.Fatalf("期望 1 条结果，实际 %d", len(resolved))
	}
	if resolved[0].Action != ActionSkip || resolved[0].Reason != ReasonNoMapping {
		t.Errorf("缺少可用值时应跳过并标记原因，实际 %+v", resolved[0])
	}
}

func TestMapFieldsByRuleLeavesUnknownToLLM(t *testing.T) {
	fields := []FormField{
		{Ref: "r1", Label: "姓名", Type: "text"},
		{Ref: "r2", Label: "你最想解决的技术难题是什么", Type: "textarea"},
	}
	resolved, remaining := MapFieldsByRule(fields, map[string]string{"basic.name": "张三"})

	if len(resolved) != 1 {
		t.Errorf("规则应只解析出 1 条，实际 %d", len(resolved))
	}
	if len(remaining) != 1 || remaining[0].Ref != "r2" {
		t.Errorf("无法匹配的字段应交给 LLM，实际 %+v", remaining)
	}
}

func TestMapFieldsByRuleRespectsMaxLength(t *testing.T) {
	fields := []FormField{{Ref: "r1", Label: "自我评价", Type: "text", MaxLength: 5}}
	values := map[string]string{"basic.self_intro": "这是一段很长的自我评价内容"}

	resolved, _ := MapFieldsByRule(fields, values)
	if got := []rune(resolved[0].Value); len(got) != 5 {
		t.Errorf("值应被裁剪到 5 个字符，实际 %d 个", len(got))
	}
}

func TestPoliticalStatusNeverMapped(t *testing.T) {
	// 政治面貌属于高度隐私信息，规则表明确要求不填。
	fields := []FormField{{Ref: "r1", Label: "政治面貌", Type: "select"}}
	resolved, _ := MapFieldsByRule(fields, map[string]string{})

	if resolved[0].Action != ActionSkip {
		t.Error("政治面貌字段必须跳过")
	}
	if !resolved[0].IsSensitive {
		t.Error("政治面貌字段应标记为敏感")
	}
}

func TestAllowedSourcesIsClosedSet(t *testing.T) {
	// 确保 LLM 白名单与规则表使用的路径一致，防止出现无法取值的映射。
	for _, rule := range ruleMap {
		if rule.source == "" {
			continue
		}
		if !allowedSources[rule.source] {
			t.Errorf("规则表中的路径 %q 未出现在 LLM 白名单中", rule.source)
		}
	}
}

func TestExtractJSONObject(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`:                 `{"a":1}`,
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"这是结果：{\"a\":1} 以上":       `{"a":1}`,
		"没有 JSON":                 `{}`,
		"":                        `{}`,
	}
	for in, want := range cases {
		if got := extractJSONObject(in); got != want {
			t.Errorf("extractJSONObject(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestRepairUnescapedStringQuotes(t *testing.T) {
	raw := `{"action":"click","reasoning":"点击"岗位投递"按钮"}`
	repaired := repairUnescapedStringQuotes(raw)
	want := `{"action":"click","reasoning":"点击\"岗位投递\"按钮"}`
	if repaired != want {
		t.Fatalf("repairUnescapedStringQuotes() = %q, want %q", repaired, want)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(repaired), &decoded); err != nil {
		t.Fatalf("repaired JSON must parse: %v", err)
	}
	if decoded["reasoning"] != `点击"岗位投递"按钮` {
		t.Fatalf("reasoning = %q", decoded["reasoning"])
	}
}

func TestRepairUnescapedStringQuotesKeepsValidJSON(t *testing.T) {
	raw := `{"action":"click","reasoning":"点击\"岗位投递\"按钮"}`
	if got := repairUnescapedStringQuotes(raw); got != raw {
		t.Fatalf("valid JSON changed: %q", got)
	}
}

func TestTruncateRunesDoesNotBreakMultibyte(t *testing.T) {
	in := "这是一段中文文本"
	out := truncateRunes(in, 4)
	// 截断后前 4 个字符应完整
	if len([]rune(out)) < 4 {
		t.Errorf("截断结果异常: %q", out)
	}
	for _, r := range out {
		if r == '\uFFFD' {
			t.Error("出现乱码，说明按字节截断了多字节字符")
		}
	}
}
