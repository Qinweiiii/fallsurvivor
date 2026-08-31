package ai

import (
	"context"
	"fmt"
)

// NavActionType 是导航动作类型。
type NavActionType string

const (
	NavClick    NavActionType = "click"
	NavScroll   NavActionType = "scroll"
	NavNavigate NavActionType = "navigate"
	NavWait     NavActionType = "wait"
	// NavStop 不是 Worker 执行的动作，而是告诉编排器「已无更多可探索，停止」。
	NavStop NavActionType = "stop"
)

// NavJob 是导航过程中在页面上发现的岗位入口。
type NavJob struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	Department string `json:"department"`
}

// NavAction 是模型给出的下一步操作。
type NavAction struct {
	Type      NavActionType `json:"type"`
	Text      string        `json:"text,omitempty"`      // click 目标文案
	Direction string        `json:"direction,omitempty"` // scroll: down/up
	Amount    int           `json:"amount,omitempty"`    // scroll 像素
	URL       string        `json:"url,omitempty"`       // navigate 目标
	Seconds   int           `json:"seconds,omitempty"`   // wait 秒
}

// NavStepInput 是单步导航的输入。
type NavStepInput struct {
	Site     string   `json:"site"`
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	PageText string   `json:"page_text"`
	History  []string `json:"history"`
}

// NavStepResult 是模型单步导航的输出。
type NavStepResult struct {
	Jobs   []NavJob  `json:"jobs"`
	Action NavAction `json:"action"`
	Reason string    `json:"reason"`
}

const navSystemPrompt = `你是一名招聘网站导航助手。目标：在已登录的招聘站点上，一步步发现「具体岗位详情页」的入口（标题 + URL + 所属部门/事业群）。

规则：
1. 每次只输出一步动作。动作类型：
   - click：点击页面上某个可见文字（如某个岗位分类、某个部门标签、某个「查看详情」入口）。text 用页面上真实出现的文字（可包含子串）。
   - scroll：向下滚动以加载更多岗位或部门列表（direction 默认 "down"）。
   - navigate：直接跳转到某个岗位详情 URL（仅当页面上明确出现该 URL 时）。
   - wait：等待页面渲染（seconds 通常 1~2）。
   - stop：已无可探索内容，或已到达具体岗位详情页，或陷入循环，停止。
2. 当当前页面就是「具体岗位详情页」（只描述一个岗位的职责/要求）时，把 job 的 url 填为当前页 URL，并 stop。
3. 当页面出现多个岗位/部门入口时，记录 jobs（每个入口的 title/url/department），然后选择其中一个尚未访问的入口 click 进入。
4. 禁止任何「提交/投递/申请」动作；你只是在探索与发现岗位入口。
5. 只输出 JSON，不要解释。

输出格式：
{"jobs":[{"title":"","url":"","department":""}],"action":{"type":"click","text":"后台开发"},"reason":""}`

// NavigateStep 让模型根据当前页面内容决策下一步导航动作，并提取已可见的岗位入口。
// pageText 较长时会被截断以控制成本。模型调用失败时调用方应保守停止。
func (c *Client) NavigateStep(ctx context.Context, in NavStepInput) (*NavStepResult, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	history := ""
	for i, h := range in.History {
		history += fmt.Sprintf("%d. %s\n", i+1, h)
	}
	user := fmt.Sprintf("招聘站点：%s\n当前页面 URL：%s\n页面标题：%s\n\n页面正文（已截断）：\n%s\n\n已执行过的动作：\n%s",
		in.Site, in.URL, in.Title, truncateRunes(in.PageText, 6000), history)

	var out NavStepResult
	if err := c.completeJSON(ctx, navSystemPrompt, user, &out); err != nil {
		return nil, err
	}
	// 兜底：模型未给出动作时视为停止，避免编排器空转。
	if out.Action.Type == "" {
		out.Action.Type = NavStop
	}
	return &out, nil
}
