package search

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

const (
	visualMaxSteps       = 10
	visualMinDetailRunes = 300
)

// CrawlVisibleDetails 通过当前渲染页面的快照逐步进入少量详情页。
// 它不读取网络响应、不构造职位 URL，所有前进动作都必须点击当前快照中的 ref。
func (c *SiteCrawler) CrawlVisibleDetails(
	ctx context.Context,
	taskID, siteKey, startURL, keyword, sourceType, sourceName, companyHint string,
	maxDetails int,
) ([]ai.NavJob, []source.RawJob, error) {
	if c.browser == nil || !c.browser.Health(ctx) {
		return nil, nil, browser.ErrWorkerUnavailable
	}
	if c.llm == nil || !c.llm.Enabled() {
		return nil, nil, fmt.Errorf("browser: LLM 未启用，无法驱动可视化导航")
	}
	if maxDetails <= 0 || maxDetails > 3 {
		maxDetails = 3
	}

	session, err := c.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID: taskID, SiteKey: siteKey, URL: startURL,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("browser: 打开会话失败: %w", err)
	}
	defer func() { _ = c.browser.CloseSession(context.Background(), taskID) }()
	if session.NeedsLogin || !session.LoggedIn {
		return nil, nil, fmt.Errorf("browser: 页面需要登录或安全验证，请在浏览器中完成后重试")
	}

	navs := make([]ai.NavJob, 0, maxDetails)
	raws := make([]source.RawJob, 0, maxDetails)
	history := make([]string, 0, visualMaxSteps)
	seenDetails := make(map[string]bool, maxDetails)
	startHost := hostOf(startURL)

	for step := 0; step < visualMaxSteps && len(raws) < maxDetails; step++ {
		scrape, err := c.browser.ScrapePage(ctx, taskID)
		if err != nil {
			return navs, raws, fmt.Errorf("browser: 读取当前页面失败: %w", err)
		}
		if scrape.NeedsLogin || visualAccessBlocked(scrape.CurrentURL, scrape.PageText) {
			return navs, raws, fmt.Errorf("browser: 页面需要登录或触发安全验证，已停止")
		}

		snapshot, err := c.browser.Snapshot(ctx, taskID)
		if err != nil {
			return navs, raws, fmt.Errorf("browser: 读取页面快照失败: %w", err)
		}
		decision, err := c.llm.DecideVisualJobStep(ctx, ai.VisualJobStepInput{
			Site:         siteKey,
			Keyword:      keyword,
			CurrentURL:   snapshot.CurrentURL,
			PageTitle:    snapshot.Title,
			TextSample:   snapshot.TextSample,
			CardSamples:  snapshot.CardSamples,
			Elements:     visualElements(snapshot.Elements),
			History:      history,
			DetailsTaken: len(raws),
			MaxDetails:   maxDetails,
		})
		if err != nil {
			return navs, raws, fmt.Errorf("ai: 可视化导航决策失败: %w", err)
		}

		switch ai.VisualJobAction(decision.Action) {
		case ai.VisualFinish, ai.VisualAbort:
			return navs, raws, nil
		case ai.VisualInput:
			if visualForbidden(decision.Target) || decision.TargetRef == "" {
				return navs, raws, nil
			}
			if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{
				Type: browser.NavInputRef, Ref: decision.TargetRef, Text: keyword,
			}); err != nil {
				return navs, raws, err
			}
		case ai.VisualClick:
			if visualForbidden(decision.Target) || decision.TargetRef == "" {
				return navs, raws, nil
			}
			if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavClickRef, Ref: decision.TargetRef}); err != nil {
				return navs, raws, err
			}
		case ai.VisualScroll:
			if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavScroll, Direction: "down", Amount: 700}); err != nil {
				return navs, raws, err
			}
		case ai.VisualWait:
			if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: 2}); err != nil {
				return navs, raws, err
			}
		case ai.VisualOpenDetail:
			if visualForbidden(decision.Target) || decision.TargetRef == "" {
				return navs, raws, nil
			}
			beforeURL := scrape.CurrentURL
			if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavClickRef, Ref: decision.TargetRef}); err != nil {
				return navs, raws, err
			}
			if _, err := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: 2}); err != nil {
				return navs, raws, err
			}
			detail, err := c.browser.ScrapePage(ctx, taskID)
			if err != nil {
				return navs, raws, fmt.Errorf("browser: 读取详情页失败: %w", err)
			}
			if detail.NeedsLogin || visualAccessBlocked(detail.CurrentURL, detail.PageText) {
				return navs, raws, fmt.Errorf("browser: 详情页需要登录或触发安全验证，已停止")
			}
			if hostOf(detail.CurrentURL) != startHost || detail.CurrentURL == beforeURL || seenDetails[detail.CurrentURL] || !looksLikeVisibleJobDetail(detail.PageText) {
				return navs, raws, nil
			}
			seenDetails[detail.CurrentURL] = true
			title := firstNonEmpty(decision.Target, detail.Title)
			navs = append(navs, ai.NavJob{Title: title, URL: detail.CurrentURL})
			raws = append(raws, source.RawJob{
				SourceType:  sourceType,
				SourceName:  sourceName,
				URL:         detail.CurrentURL,
				Title:       title,
				Content:     detail.PageText,
				Snippet:     visualSnippet(detail.PageText, 500),
				CompanyHint: companyHint,
				Query:       keyword,
			})
			if len(raws) < maxDetails {
				back, backErr := c.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavBack})
				if backErr != nil || !back.OK {
					return navs, raws, nil
				}
			}
		default:
			return navs, raws, nil
		}
		history = append(history, fmt.Sprintf("%d:%s %s", step+1, decision.Action, decision.Target))
		select {
		case <-ctx.Done():
			return navs, raws, nil
		case <-time.After(250 * time.Millisecond):
		}
	}
	return navs, raws, nil
}

func visualElements(elements []browser.ExploreElement) []ai.ExploreElementView {
	out := make([]ai.ExploreElementView, 0, len(elements))
	for _, element := range elements {
		if !element.Visible {
			continue
		}
		out = append(out, ai.ExploreElementView{
			Ref: element.Ref, Text: firstNonEmpty(element.Text, element.Placeholder, element.AriaLabel), Tag: element.Tag, Type: element.InputType, Href: element.Href,
		})
	}
	return out
}

func visualAccessBlocked(currentURL, pageText string) bool {
	lower := strings.ToLower(currentURL + " " + pageText)
	return strings.Contains(lower, "_security_check") || strings.Contains(lower, "captcha") || strings.Contains(lower, "验证码") || strings.Contains(lower, "请登录")
}

func visualForbidden(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, word := range []string{"登录", "注册", "验证码", "投递", "申请", "沟通", "提交", "保存", "简历", "login", "apply", "submit"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func looksLikeVisibleJobDetail(text string) bool {
	if utf8.RuneCountInString(strings.TrimSpace(text)) < visualMinDetailRunes {
		return false
	}
	hits := 0
	for _, marker := range []string{"职位描述", "岗位职责", "工作内容", "任职要求", "职位要求", "岗位要求", "职位详情"} {
		if strings.Contains(text, marker) {
			hits++
		}
	}
	return hits >= 2
}

func visualSnippet(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}
