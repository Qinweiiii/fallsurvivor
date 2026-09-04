package ai

import (
	"strings"
	"testing"
)

// testSchema 是测试用的抽取意图：一个必填名称 + 两个可选属性。
func testSchema() ExtractSchema {
	return ExtractSchema{
		Query: "列表中的条目",
		Fields: []ExtractField{
			{Name: "title", Desc: "名称", Required: true},
			{Name: "city", Desc: "城市"},
			{Name: "url", Desc: "链接"},
		},
	}
}

/**
 * normalizeExtractResult 的行为测试。
 *
 * 这些用例锁住的是**与站点无关**的结构性规则。
 * 刻意不测「某个词该不该被当成噪声」——那属于模型的语义判断，
 * 用测试固化词表会把人肉适配重新引回来。
 */
func TestNormalizeExtractResult(t *testing.T) {
	t.Run("丢弃缺少必填字段的条目", func(t *testing.T) {
		out := &ExtractResult{
			TargetFound: true,
			Items: []ExtractedItem{
				{"title": "服务端开发工程师", "city": "北京"},
				{"title": "", "city": "上海"},   // 无标题
				{"city": "广州"},               // 完全没有标题键
				{"title": "  ", "city": "深圳"}, // 仅空白
				{"title": "算法工程师"},
			},
		}
		normalizeExtractResult(out, testSchema())

		if len(out.Items) != 2 {
			t.Fatalf("期望保留 2 条，实际 %d 条: %+v", len(out.Items), out.Items)
		}
		if out.Items[0]["title"] != "服务端开发工程师" || out.Items[1]["title"] != "算法工程师" {
			t.Errorf("保留内容不正确: %+v", out.Items)
		}
	})

	t.Run("按必填字段去重", func(t *testing.T) {
		out := &ExtractResult{
			TargetFound: true,
			Items: []ExtractedItem{
				{"title": "数据研发工程师", "city": "杭州"},
				{"title": "数据研发工程师", "city": "杭州"}, // 完全重复
				{"title": "数据研发工程师", "city": "北京"}, // 同名不同城，仍按标题去重
				{"title": "测试开发工程师", "city": "北京"},
			},
		}
		normalizeExtractResult(out, testSchema())
		if len(out.Items) != 2 {
			t.Errorf("期望去重为 2 条，实际 %d 条: %+v", len(out.Items), out.Items)
		}
	})

	t.Run("丢弃 schema 未声明的字段", func(t *testing.T) {
		// 模型有时会自行添加字段，通用层不应把它们透传给下游。
		out := &ExtractResult{
			TargetFound: true,
			Items: []ExtractedItem{
				{"title": "前端开发工程师", "city": "北京", "salary": "面议", "foo": "bar"},
			},
		}
		normalizeExtractResult(out, testSchema())
		item := out.Items[0]
		if _, ok := item["salary"]; ok {
			t.Error("未声明的 salary 字段应被丢弃")
		}
		if _, ok := item["foo"]; ok {
			t.Error("未声明的 foo 字段应被丢弃")
		}
		if item["title"] != "前端开发工程师" || item["city"] != "北京" {
			t.Errorf("声明的字段不应受影响: %+v", item)
		}
	})

	t.Run("字段值两端空白被清理", func(t *testing.T) {
		out := &ExtractResult{
			TargetFound: true,
			Items:       []ExtractedItem{{"title": "  运维工程师 ", "city": " 成都 "}},
		}
		normalizeExtractResult(out, testSchema())
		if out.Items[0]["title"] != "运维工程师" || out.Items[0]["city"] != "成都" {
			t.Errorf("空白未被清理: %+v", out.Items[0])
		}
	})

	t.Run("全部无效时不应声称找到目标", func(t *testing.T) {
		// 关键防御：否则上层会把空结果当成采集成功。
		out := &ExtractResult{
			TargetFound: true,
			Items:       []ExtractedItem{{"city": "北京"}, {"title": ""}},
		}
		normalizeExtractResult(out, testSchema())
		if out.TargetFound {
			t.Error("一条有效条目都没有时，TargetFound 必须为 false")
		}
	})

	t.Run("负数总数归零", func(t *testing.T) {
		out := &ExtractResult{
			TargetFound: true,
			TotalHint:   -3,
			Items:       []ExtractedItem{{"title": "产品经理"}},
		}
		normalizeExtractResult(out, testSchema())
		if out.TotalHint != 0 {
			t.Errorf("负数应归零，实际 %d", out.TotalHint)
		}
	})
}

/**
 * 分块的行为测试。
 *
 * 重点验证「不把一条记录切碎」这个核心承诺，
 * 以及表格跨块时表头能被带到下一块。
 */
func TestChunkMarkdownByStructure(t *testing.T) {
	t.Run("短内容只有一块且无后续", func(t *testing.T) {
		chunks := ChunkMarkdownByStructure("# 标题\n\n一段正文", 10_000, 5, 0)
		if len(chunks) != 1 {
			t.Fatalf("期望 1 块，实际 %d 块", len(chunks))
		}
		if chunks[0].HasMore {
			t.Error("短内容不应标记 HasMore")
		}
		if chunks[0].OverlapPrefix != "" {
			t.Error("首块不应有衔接前缀")
		}
	})

	t.Run("列表项不被切断", func(t *testing.T) {
		// 构造多条列表项，用很小的上限强制分块。
		var b strings.Builder
		for i := 0; i < 40; i++ {
			b.WriteString("- 条目内容这里有一些文字用来撑长度\n")
		}
		chunks := ChunkMarkdownByStructure(b.String(), 200, 2, 0)
		if len(chunks) < 2 {
			t.Fatalf("期望被分成多块，实际 %d 块", len(chunks))
		}
		// 每块内容里的每一行，要么是完整列表项，要么是空行。
		for _, c := range chunks {
			for _, line := range strings.Split(c.Content, "\n") {
				s := strings.TrimSpace(line)
				if s == "" {
					continue
				}
				if !strings.HasPrefix(s, "-") {
					t.Errorf("出现被切断的列表行: %q", line)
				}
			}
		}
	})

	t.Run("表格跨块时补回表头", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("| 名称 | 城市 |\n")
		b.WriteString("| --- | --- |\n")
		for i := 0; i < 40; i++ {
			b.WriteString("| 某个岗位名称占位 | 北京 |\n")
		}
		chunks := ChunkMarkdownByStructure(b.String(), 200, 2, 0)
		if len(chunks) < 2 {
			t.Fatalf("期望被分成多块，实际 %d 块", len(chunks))
		}
		// 后续块若以表格行开头，衔接前缀必须含表头，
		// 否则模型看到的是一堆没有列名的单元格。
		second := chunks[1]
		if !strings.Contains(second.OverlapPrefix, "名称") ||
			!strings.Contains(second.OverlapPrefix, "---") {
			t.Errorf("第二块未补回表头，OverlapPrefix=%q", second.OverlapPrefix)
		}
	})

	t.Run("续抽返回包含起点的块", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 40; i++ {
			b.WriteString("- 条目内容这里有一些文字用来撑长度\n")
		}
		content := b.String()
		all := ChunkMarkdownByStructure(content, 200, 2, 0)
		if len(all) < 3 {
			t.Skipf("分块数不足，无法验证续抽（%d 块）", len(all))
		}
		// 从第二块的起点续抽，应当从第二块开始返回。
		resume := ChunkMarkdownByStructure(content, 200, 2, all[1].CharStart)
		if len(resume) == 0 {
			t.Fatal("续抽返回空")
		}
		if resume[0].CharEnd <= all[1].CharStart {
			t.Errorf("续抽起点不正确: 首块 CharEnd=%d，起点=%d",
				resume[0].CharEnd, all[1].CharStart)
		}
	})

	t.Run("起点越界返回空", func(t *testing.T) {
		if got := ChunkMarkdownByStructure("一点内容", 1000, 2, 999); got != nil {
			t.Errorf("越界应返回 nil，实际 %d 块", len(got))
		}
	})

	t.Run("末块偏移不超过正文长度", func(t *testing.T) {
		content := "第一行\n第二行\n第三行\n"
		chunks := ChunkMarkdownByStructure(content, 10_000, 5, 0)
		last := chunks[len(chunks)-1]
		if last.CharEnd > len(content) {
			t.Errorf("末块 CharEnd=%d 超过正文长度 %d", last.CharEnd, len(content))
		}
	})
}

func TestStripJSONBlobs(t *testing.T) {
	t.Run("删除长 JSON 行", func(t *testing.T) {
		blob := `{"state":{"user":{"id":1,"name":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}`
		in := "正常内容\n" + blob + "\n更多内容"
		got := stripJSONBlobs(in)
		if strings.Contains(got, "state") {
			t.Errorf("长 JSON 行应被删除，实际: %q", got)
		}
		if !strings.Contains(got, "正常内容") || !strings.Contains(got, "更多内容") {
			t.Errorf("正常内容不应被删除，实际: %q", got)
		}
	})

	t.Run("Markdown 链接不被误删", func(t *testing.T) {
		// 链接也以 [ 开头，只按前缀判断会误伤——必须真正解析过才丢。
		long := "[" + strings.Repeat("很长的岗位名称", 20) + "](/job/123)"
		got := stripJSONBlobs(long)
		if got != long {
			t.Errorf("Markdown 链接不应被删除，实际: %q", got)
		}
	})

	t.Run("短 JSON 保留", func(t *testing.T) {
		in := `{"a":1}`
		if got := stripJSONBlobs(in); got != in {
			t.Errorf("短 JSON 应保留，实际: %q", got)
		}
	})
}
