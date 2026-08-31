package ai

import (
	"context"
	"fmt"
)

// PageType 是页面类型判定结果。
type PageType string

const (
	// PageTypeJobDetail 单个具体岗位的招聘详情页（只描述一个岗位）。
	PageTypeJobDetail PageType = "JOB_DETAIL"
	// PageTypeAggregation 聚合/列表页（一个页面含多个不同公司或不同岗位入口）。
	PageTypeAggregation PageType = "AGGREGATION"
	// PageTypeIrrelevant 无关页（登录页、官网首页、导航页、新闻等非岗位页）。
	PageTypeIrrelevant PageType = "IRRELEVANT"
)

// pageClassifyResult 是分类器的 JSON 输出结构。
type pageClassifyResult struct {
	PageType PageType `json:"page_type"`
	Reason   string   `json:"reason"`
}

const pageClassifierSystemPrompt = `你是一名招聘网页类型判定器。
任务：根据网页标题、来源 URL 与正文文本，判断该页面属于哪一类。

可选类别：
- JOB_DETAIL：单个具体岗位的招聘详情页，正文只围绕一个岗位展开（职责、要求、公司等）。
- AGGREGATION：聚合/列表页，一个页面包含多个不同公司或不同岗位的入口，例如搜索结果页、职位列表页、校招汇总页。
- IRRELEVANT：无关页，如登录页、企业官网首页、导航页、新闻、政策说明等非岗位详情页。

判定要点：
1. 若正文同时出现多个不同公司名、或大量不同岗位的标题与链接，多半是 AGGREGATION。
2. 若正文只围绕一个岗位展开，是 JOB_DETAIL。
3. 不要仅凭 URL 判断，必须以正文内容为主；URL 形态千变万化，规则无法覆盖。
4. 只输出 JSON，不要输出解释。

输出 JSON 格式：
{"page_type":"JOB_DETAIL","reason":""}`

// ClassifyPage 让模型判断页面类型（聚合页 / 具体岗位页 / 无关页）。
// content 不必很长——分类只需看正文整体形态，这里截断到 1500 字符以控制成本。
// 返回错误时调用方应保守地按岗位页继续，避免误删真实岗位。
func (c *Client) ClassifyPage(ctx context.Context, title, content, url string) (PageType, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	user := fmt.Sprintf("网页标题：%s\n来源 URL：%s\n\n网页文本：\n%s",
		title, url, truncateRunes(content, 1500))

	var out pageClassifyResult
	if err := c.completeJSON(ctx, pageClassifierSystemPrompt, user, &out); err != nil {
		return "", err
	}
	switch out.PageType {
	case PageTypeJobDetail, PageTypeAggregation, PageTypeIrrelevant:
		return out.PageType, nil
	default:
		// 未知类别保守视作岗位页，避免误删真实岗位。
		return PageTypeJobDetail, nil
	}
}
