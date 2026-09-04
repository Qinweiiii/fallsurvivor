package ai

import (
	"context"
	"encoding/json"
	"strings"
)

// VisualJobAction 是可视化岗位导航允许的最小动作集。
// 它刻意不含 navigate / inspect：只允许操作当前渲染页面中的快照元素。
type VisualJobAction string

const (
	VisualInput      VisualJobAction = "input"
	VisualClick      VisualJobAction = "click"
	VisualOpenDetail VisualJobAction = "open_detail"
	VisualScroll     VisualJobAction = "scroll"
	VisualWait       VisualJobAction = "wait"
	VisualFinish     VisualJobAction = "finish"
	VisualAbort      VisualJobAction = "abort"
)

type VisualJobStepInput struct {
	Site         string               `json:"site"`
	Keyword      string               `json:"keyword"`
	CurrentURL   string               `json:"current_url"`
	PageTitle    string               `json:"page_title"`
	TextSample   string               `json:"text_sample"`
	CardSamples  []string             `json:"card_samples,omitempty"`
	Elements     []ExploreElementView `json:"elements"`
	History      []string             `json:"history,omitempty"`
	DetailsTaken int                  `json:"details_taken"`
	MaxDetails   int                  `json:"max_details"`
}

type VisualJobDecision struct {
	Action    string `json:"action"`
	TargetRef string `json:"target_ref,omitempty"`
	Target    string `json:"target,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

const visualJobSystemPrompt = `你负责在已登录招聘网站的当前可见页面中寻找少量岗位详情。

只能使用以下动作：
- input：向当前快照的输入框输入 keyword；必须提供 target_ref。
- click：点击当前快照中的搜索、筛选或列表入口；必须提供 target_ref。
- open_detail：点击一个岗位卡片或岗位标题进入详情；必须提供 target_ref，target 填岗位标题。
- scroll：向下滚动加载更多可见卡片。
- wait：等待页面渲染。
- finish：已获取足够详情或没有更多安全可点的岗位。
- abort：遇到登录页、安全验证、验证码或页面异常时停止。

严格规则：
1. 只能使用 observation.elements 中本轮给出的 ref，不能编造选择器或 URL。
2. 不得使用 inspect、网络请求、开发者工具、URL 拼装或直接访问深层职位链接。
3. 不得点击登录、注册、验证码、投递、申请、沟通、提交、保存等元素。
4. 先完成搜索，再在列表中每次只打开一个岗位详情；details_taken 达到 max_details 后必须 finish。
5. target/ref 不确定时宁可 finish，不要猜测或重复点击。

只输出 JSON，字段顺序必须如下：
{"action":"open_detail","target_ref":"el:7","target":"岗位标题","reasoning":"不超过30字"}`

// DecideVisualJobStep 仅基于渲染页面的快照选择下一次可见交互。
func (c *Client) DecideVisualJobStep(ctx context.Context, in VisualJobStepInput) (*VisualJobDecision, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out VisualJobDecision
	if err := c.completeJSON(ctx, visualJobSystemPrompt, string(payload), &out); err != nil {
		return nil, err
	}
	out.Action = strings.ToLower(strings.TrimSpace(out.Action))
	out.TargetRef = strings.TrimSpace(out.TargetRef)
	out.Target = strings.TrimSpace(out.Target)
	out.Reasoning = strings.TrimSpace(out.Reasoning)
	switch VisualJobAction(out.Action) {
	case VisualInput, VisualClick, VisualOpenDetail, VisualScroll, VisualWait, VisualFinish, VisualAbort:
	default:
		out.Action = string(VisualAbort)
	}
	if (out.Action == string(VisualInput) || out.Action == string(VisualClick) || out.Action == string(VisualOpenDetail)) && out.TargetRef == "" {
		out.Action = string(VisualAbort)
	}
	return &out, nil
}
