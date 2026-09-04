package main

// 多站点抽取回归工具。
//
// 存在意义有两个，都针对具体教训：
//
//  1. **不必反复开浏览器**。页面正文抓一次存成文件，之后调 prompt、
//     调分块、调清洗规则都只读文件，零浏览器开销。
//
//  2. **防止只对单个站点有效**。一次跑完多个站点并列出对比表，
//     任何改动若只让某一家变好、其他家变差，立刻就能看出来。
//     只盯着一个站点调，必然滑向为它定制。
//
// 用法：
//
//	# 单个文件
//	go run ./cmd/mdextract /tmp/ks_md.json
//
//	# 目录下所有 *.json 一起跑（推荐：多站点回归）
//	go run ./cmd/mdextract ./testdata/pages/
//
// 输入文件为 Worker /page/markdown 的响应（含 markdown / current_url 字段）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/config"
	"github.com/eddiel/fallsurvivor/backend/internal/ai"
)

// workerMarkdown 对应 Worker /page/markdown 的响应结构。
type workerMarkdown struct {
	CurrentURL string `json:"current_url"`
	Title      string `json:"title"`
	Markdown   string `json:"markdown"`
	Truncated  bool   `json:"truncated"`
}

// siteResult 是单个站点的抽取结果，用于最终横向对比。
type siteResult struct {
	name      string
	url       string
	mdChars   int
	items     int
	totalHint int
	found     bool
	truncated bool
	// fieldFill 各字段的填充率（该字段非空的条目占比）。
	//
	// 这是判断「抽取质量」的关键指标：只看条数会漏掉
	// 「抽到 20 条但全都只有标题」这类退化。
	fieldFill map[string]float64
	elapsed   time.Duration
	err       string
	note      string
	samples   []string
}

// jobSchema 是本工具使用的抽取意图。
//
// 与 search 包中的定义保持一致；此处独立一份是为了让工具
// 不依赖业务包（避免为了跑个诊断把整条服务依赖链拖进来）。
func jobSchema() ai.ExtractSchema {
	return ai.ExtractSchema{
		Query: "页面上招聘岗位列表中的每一个岗位",
		ItemHint: "一个有效岗位应当具备明确的职位名称，" +
			"并且至少还能看到工作地点、岗位类别、招聘性质、发布时间、详情链接中的任意一项。" +
			"仅有孤立短词、没有任何附属信息的内容通常是页面的筛选选项或导航项，不是岗位。",
		Fields: []ai.ExtractField{
			{Name: "title", Desc: "职位名称。不要把「急招」「热招」「NEW」这类招聘状态标记拼进名称里", Required: true},
			{Name: "url", Desc: "该岗位详情页的链接地址，正文中的原文即可（相对路径也可以）。找不到就留空"},
			{Name: "location", Desc: "工作地点或城市，可能包含多个城市"},
			{Name: "category", Desc: "岗位类别或职能方向，如技术类、产品类及其细分方向"},
			{Name: "job_nature", Desc: "招聘性质，如全职、实习、校招、社招等"},
			{Name: "department", Desc: "所属部门、事业群或业务线"},
			{Name: "published_at", Desc: "发布时间或更新时间，保留页面上的原始写法，不要转换格式"},
			{Name: "description", Desc: "岗位描述、职责或要求的片段。列表页通常没有，找不到就留空"},
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: go run ./cmd/mdextract <markdown.json | 目录>")
		os.Exit(2)
	}
	target := os.Args[1]

	files, err := collectFiles(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "未找到任何 .json 文件: %s\n", target)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}
	llm := ai.NewClient(cfg.LLM)
	if !llm.Enabled() {
		fmt.Fprintln(os.Stderr, "LLM 未启用，请检查 API Key 配置")
		os.Exit(1)
	}

	schema := jobSchema()
	results := make([]siteResult, 0, len(files))

	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		fmt.Printf("\n======== %s ========\n", name)
		res := runOne(llm, schema, name, path)
		results = append(results, res)

		if res.err != "" {
			fmt.Printf("失败: %s\n", res.err)
			continue
		}
		fmt.Printf("页面: %s\n", res.url)
		fmt.Printf("正文 %d 字符 | 抽到 %d 条 | 页面总数提示 %d | 目标页 %v | 还有后续 %v | 耗时 %s\n",
			res.mdChars, res.items, res.totalHint, res.found, res.truncated,
			res.elapsed.Round(time.Millisecond))
		if res.note != "" {
			fmt.Printf("模型备注: %s\n", res.note)
		}
		for _, s := range res.samples {
			fmt.Printf("  %s\n", s)
		}
	}

	printComparison(results, schema)
}

// collectFiles 支持传单个文件或目录。
func collectFiles(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("路径不可用: %w", err)
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(target, e.Name()))
	}
	sort.Strings(files) // 固定顺序，便于跨次对比
	return files, nil
}

// runOne 对单个页面文件执行抽取。
func runOne(llm *ai.Client, schema ai.ExtractSchema, name, path string) siteResult {
	res := siteResult{name: name, fieldFill: map[string]float64{}}

	raw, err := os.ReadFile(path)
	if err != nil {
		res.err = fmt.Sprintf("读取失败: %v", err)
		return res
	}
	var page workerMarkdown
	if err := json.Unmarshal(raw, &page); err != nil {
		res.err = fmt.Sprintf("解析失败: %v", err)
		return res
	}
	if strings.TrimSpace(page.Markdown) == "" {
		res.err = "markdown 字段为空"
		return res
	}
	res.url = page.CurrentURL
	res.mdChars = len(page.Markdown)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	start := time.Now()
	out, err := llm.ExtractFromMarkdown(ctx, page.Markdown, schema, page.CurrentURL, 0, nil)
	res.elapsed = time.Since(start)
	if err != nil {
		res.err = fmt.Sprintf("抽取失败: %v", err)
		return res
	}

	res.items = len(out.Items)
	res.totalHint = out.TotalHint
	res.found = out.TargetFound
	res.truncated = out.Truncated
	res.note = out.Note

	// 统计各字段填充率。
	if len(out.Items) > 0 {
		for _, f := range schema.Fields {
			filled := 0
			for _, item := range out.Items {
				if strings.TrimSpace(item[f.Name]) != "" {
					filled++
				}
			}
			res.fieldFill[f.Name] = float64(filled) / float64(len(out.Items))
		}
	}

	// 取前 3 条作为人工抽查样本。
	for i, item := range out.Items {
		if i >= 3 {
			break
		}
		res.samples = append(res.samples, fmt.Sprintf("%d. %s | %s | %s | %s",
			i+1, item["title"], dash(item["location"]), dash(item["category"]), dash(item["published_at"])))
	}
	return res
}

/**
 * printComparison 输出跨站点对比表。
 *
 * 这张表是验收依据：改动之后每一列都要看，
 * 只有全部站点都不退化才算改对。若某家变好、另一家变差，
 * 说明改动带上了站点特异性，应当重新设计。
 */
func printComparison(results []siteResult, schema ai.ExtractSchema) {
	fmt.Printf("\n\n======== 多站点对比 ========\n")
	fmt.Printf("%-14s %8s %7s %7s %7s  %s\n", "站点", "正文字符", "抽到", "总数", "目标页", "字段填充率")
	fmt.Println(strings.Repeat("-", 92))

	okCount := 0
	for _, r := range results {
		if r.err != "" {
			fmt.Printf("%-14s %8s %7s %7s %7s  %s\n", r.name, "-", "-", "-", "-", "失败: "+r.err)
			continue
		}
		if r.found && r.items > 0 {
			okCount++
		}

		// 只展示关键字段的填充率，避免表格过宽。
		var fills []string
		for _, f := range schema.Fields {
			switch f.Name {
			case "title", "url", "location", "category":
				fills = append(fills, fmt.Sprintf("%s=%.0f%%", f.Name, r.fieldFill[f.Name]*100))
			}
		}
		fmt.Printf("%-14s %8d %7d %7d %7v  %s\n",
			r.name, r.mdChars, r.items, r.totalHint, r.found, strings.Join(fills, " "))
	}

	fmt.Println(strings.Repeat("-", 92))
	fmt.Printf("成功站点: %d / %d\n", okCount, len(results))
	if okCount < len(results) {
		fmt.Println("提示: 有站点未抽到条目。请检查是通用逻辑缺陷，")
		fmt.Println("      而不要为个别站点添加专属规则——那会让问题在下一个站点重现。")
	}
}

// dash 把空字符串显示为短横线，便于快速看出缺失字段。
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
