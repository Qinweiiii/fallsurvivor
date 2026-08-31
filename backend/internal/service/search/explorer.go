package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/google/uuid"
)

// Explorer 在未知站点上自动探索「岗位列表怎么拿」。
//
// 定位：只在站点 Recipe 未命中时才启用（Discovery Path）。
// 已知站点（腾讯、字节等）走 Fast Path，不需要探索——
// 探索是昂贵且不确定的，能避免就避免。
//
// 安全边界（与既有 browser 层一致）：
//   - 所有动作经 Worker 执行，Worker 侧拒绝点击提交/投递类元素；
//   - navigate 仅允许 http/https 公网地址；
//   - 步数有上限，避免无限循环；
//   - 全程只读页面与网络，不执行任何 JS。
type Explorer struct {
	browser *browser.Client
	llm     *ai.Client

	// maxSteps 单次探索的最大步数。
	maxSteps int
	// confidenceThreshold 达到该信心值即提前结束。
	confidenceThreshold int
	// topNCandidates 每步交给 LLM 的网络候选条数。
	topNCandidates int
}

// NewExplorer 构造探索器。
func NewExplorer(bc *browser.Client, llm *ai.Client) *Explorer {
	return &Explorer{
		browser:             bc,
		llm:                 llm,
		maxSteps:            defaultExploreMaxSteps,
		confidenceThreshold: defaultConfidenceThreshold,
		topNCandidates:      defaultTopNCandidates,
	}
}

// 探索流程的默认值。
const (
	// defaultExploreMaxSteps 单次探索最多走多少步。
	// 与既有 crawlMaxSteps(18) 保持同一量级，避免长时间占用浏览器。
	defaultExploreMaxSteps = 12
	// defaultConfidenceThreshold 信心达到该值即认为已找到，提前结束。
	defaultConfidenceThreshold = 80
	// defaultTopNCandidates 每步交给 LLM 的网络候选条数。
	defaultTopNCandidates = 5
	// exploreWaitAfterAction 每个动作后等待异步请求完成的秒数。
	exploreWaitAfterAction = 2
)

// ExploreRequest 探索请求。
type ExploreRequest struct {
	// SiteKey 站点标识（用于选择浏览器登录态目录）。
	SiteKey string
	// EntryURL 探索起点（通常是校招首页）。
	EntryURL string
	// Keyword 可选关键词，用于尝试搜索动作。
	Keyword string
}

// ExploreResult 探索结果。
type ExploreResult struct {
	// Success 是否成功产出可用配置。
	Success bool
	// Candidate 产出的采集配置候选；失败时为 nil。
	Candidate *ai.RecipeCandidate
	// Steps 实际执行的步数。
	Steps int
	// Trace 执行轨迹，便于排查与展示。
	Trace []ExploreStepTrace
	// Reason 失败原因。
	Reason string
	// DurationMS 耗时。
	DurationMS int64
}

// ExploreStepTrace 单步执行轨迹。
type ExploreStepTrace struct {
	Step       int    `json:"step"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	Reasoning  string `json:"reasoning"`
	Result     string `json:"result"`
	Confidence int    `json:"confidence"`
	// NewRequests 该步新增的网络请求数。
	NewRequests int `json:"new_requests"`
}

// Explore 在指定站点上探索岗位列表的获取方式。
//
// 流程：
//  1. 打开会话并登录态检查（未登录直接失败，不做任何自动化）；
//  2. 循环：模型观测 → 决策 → 执行 → 观测新增请求；
//  3. 模型判定 finish 或达到步数上限后，由 Builder 产出配置候选。
func (e *Explorer) Explore(ctx context.Context, req ExploreRequest) (*ExploreResult, error) {
	start := time.Now()
	result := &ExploreResult{Trace: make([]ExploreStepTrace, 0, e.maxSteps)}

	if e.llm == nil || !e.llm.Enabled() {
		return nil, fmt.Errorf("探索需要启用 LLM")
	}
	if strings.TrimSpace(req.EntryURL) == "" {
		return nil, fmt.Errorf("探索起点 URL 不能为空")
	}

	siteKey := strings.TrimSpace(req.SiteKey)
	if siteKey == "" {
		siteKey = "generic"
	}
	taskID := uuid.NewString()

	// 打开会话：需要登录态才能看到真实岗位数据。
	session, err := e.browser.OpenSession(ctx, browser.SessionRequest{
		TaskID:  taskID,
		SiteKey: siteKey,
		URL:     req.EntryURL,
	})
	if err != nil {
		return nil, fmt.Errorf("打开浏览器会话失败: %w", err)
	}
	defer func() {
		if closeErr := e.browser.CloseSession(context.Background(), taskID); closeErr != nil {
			slog.Warn("关闭探索会话失败", "task_id", taskID, "error", closeErr.Error())
		}
	}()

	// 登录态处理：探索是只读观察，未登录时【不直接放弃】。
	//
	// 原因：很多校招站点的岗位列表接口是公开的，未登录也能观察到数据流；
	// 且探索的价值恰恰在于「看清站点结构」，登录态缺失不应成为阻断条件。
	// 这里只记录警告——若最终拿不到有效数据，模型会通过 abort 或低 confidence 体现。
	// 真正需要登录才能完整验证的场景（如写入 Recipe），由上层决定是否要求。
	loginWarn := ""
	if session.NeedsLogin || !session.LoggedIn {
		loginWarn = "未检测到登录态，本次探索可能只能观测到公开数据"
		slog.Warn("探索会话未登录，仍继续观察", "site_key", siteKey)
	}

	// 开始观测网络请求。
	baseline, err := e.browser.ObserveStart(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("启动网络观测失败: %w", err)
	}

	// 首屏加载往往已经触发了列表接口，先观测一次。
	time.Sleep(exploreWaitAfterAction * time.Second)
	_, candidates, err := e.browser.ObserveDiff(ctx, taskID, baseline, 50, e.topNCandidates)
	if err != nil {
		slog.Warn("首屏观测失败", "error", err.Error())
	}

	// 记录全部观测到的候选，供最终 Builder 使用。
	allCandidates := make([]ai.ExploreRequestView, 0, 32)
	allCandidates = append(allCandidates, toRequestViews(candidates)...)

	lastResult := "已进入站点首页"
	var decision *ai.ExploreDecision

	for step := 1; step <= e.maxSteps; step++ {
		// 观测当前页面。
		snap, snapErr := e.browser.Snapshot(ctx, taskID)
		if snapErr != nil {
			slog.Warn("读取页面快照失败", "step", step, "error", snapErr.Error())
		}

		obs := ai.ExploreObservation{
			Step:             step,
			CurrentURL:       currentURLOr(snap, session.CurrentURL),
			PageTitle:        titleOr(snap),
			TextSample:       textSampleOr(snap),
			CardSamples:      cardSamplesOr(snap),
			Elements:         toElementViews(snap),
			NewRequests:      toRequestViews(candidates),
			LastActionResult: lastResult,
		}

		// 模型决策。
		decision, err = e.decideWithRetry(ctx, obs)
		if err != nil {
			result.Reason = "LLM 决策失败: " + err.Error()
			break
		}

		trace := ExploreStepTrace{
			Step:        step,
			Action:      decision.Action,
			Target:      decision.Target,
			Reasoning:   decision.Reasoning,
			Confidence:  decision.Confidence,
			NewRequests: len(candidates),
		}

		// 结束条件。
		if decision.Action == string(ai.ExploreFinish) {
			trace.Result = "判定已找到数据接口"
			result.Trace = append(result.Trace, trace)
			result.Steps = step
			break
		}
		if decision.Action == string(ai.ExploreAbort) {
			trace.Result = "放弃探索"
			result.Trace = append(result.Trace, trace)
			result.Steps = step
			result.Reason = "模型判定该站点无法自动探索：" + decision.Reasoning
			result.DurationMS = time.Since(start).Milliseconds()
			return result, nil
		}

		// 执行动作。
		execMsg, execOK := e.executeAction(ctx, taskID, decision, req.Keyword)
		trace.Result = execMsg
		lastResult = execMsg
		result.Trace = append(result.Trace, trace)

		// 动作后等待异步请求完成，再观测新增请求。
		time.Sleep(exploreWaitAfterAction * time.Second)
		_, candidates, err = e.browser.ObserveDiff(ctx, taskID, baseline, 50, e.topNCandidates)
		if err != nil {
			slog.Warn("观测新增请求失败", "step", step, "error", err.Error())
			candidates = nil
		}
		trace.NewRequests = len(candidates)
		if n := len(result.Trace); n > 0 {
			result.Trace[n-1].NewRequests = len(candidates)
		}
		// 累计候选，去重后供 Builder 使用。
		allCandidates = mergeRequestViews(allCandidates, toRequestViews(candidates))

		if !execOK {
			// 连续失败会让模型自行调整，这里只记录不中断。
			slog.Debug("探索动作执行失败", "step", step, "action", decision.Action, "msg", execMsg)
		}

		// 信心足够高也提前结束。
		if decision.Confidence >= e.confidenceThreshold && len(candidates) > 0 {
			result.Steps = step
			break
		}
		if step == e.maxSteps {
			result.Steps = step
		}
	}

	// 由观测数据产出配置候选。
	if len(allCandidates) == 0 {
		result.Reason = joinReason("未观测到任何可用的数据请求", loginWarn)
		result.DurationMS = time.Since(start).Milliseconds()
		return result, nil
	}

	candidate, err := e.llm.BuildRecipeCandidate(ctx, allCandidates, req.EntryURL)
	if err != nil {
		result.Reason = joinReason("生成采集配置失败: "+err.Error(), loginWarn)
		result.DurationMS = time.Since(start).Milliseconds()
		return result, nil
	}
	if strings.TrimSpace(candidate.ListAPI) == "" {
		result.Reason = joinReason("未能从观测数据中识别出列表接口", loginWarn)
		result.DurationMS = time.Since(start).Milliseconds()
		return result, nil
	}

	result.Success = true
	result.Candidate = candidate
	result.DurationMS = time.Since(start).Milliseconds()

	slog.Info("站点探索完成",
		"site_key", siteKey,
		"steps", result.Steps,
		"list_api", candidate.ListAPI,
		"confidence", candidate.Confidence,
		"duration_ms", result.DurationMS)
	return result, nil
}

// decideWithRetry 让模型决策下一步动作，输出被截断时自动重试一次。
//
// 重试策略：截断通常是因为观测内容太长（元素多、文本长），
// 导致模型推理占用了过多输出额度。因此重试时**精简观测**：
//   - 砍掉页面文本样本（模型主要靠元素文案与网络请求判断）；
//   - 元素数量减半；
//   - 网络候选只保留分数最高的 3 条，并缩短 sample。
//
// 只重试一次：连续两次截断说明该步输入确实过重，继续重试收益递减。
func (e *Explorer) decideWithRetry(ctx context.Context, obs ai.ExploreObservation) (*ai.ExploreDecision, error) {
	d, err := e.llm.DecideExploreStep(ctx, obs)
	if err == nil {
		return d, nil
	}

	var truncErr *ai.ExploreTruncatedError
	if !errors.As(err, &truncErr) {
		// 非截断类错误（网络、鉴权等）不重试。
		return nil, err
	}

	slog.Warn("模型输出被截断，精简观测后重试", "step", obs.Step, "reasoning", truncErr.Reasoning)

	slim := obs
	slim.TextSample = ""
	slim.CardSamples = nil
	if len(slim.Elements) > 20 {
		slim.Elements = slim.Elements[:20]
	}
	if len(slim.NewRequests) > 3 {
		slim.NewRequests = slim.NewRequests[:3]
	}
	// 缩短响应样本，进一步压缩上下文。
	for i := range slim.NewRequests {
		if len(slim.NewRequests[i].Sample) > 150 {
			slim.NewRequests[i].Sample = slim.NewRequests[i].Sample[:150]
		}
	}

	d, retryErr := e.llm.DecideExploreStep(ctx, slim)
	if retryErr != nil {
		return nil, retryErr
	}
	return d, nil
}

// executeAction 执行模型决策的单个动作，返回执行结果与是否成功。
func (e *Explorer) executeAction(ctx context.Context, taskID string, d *ai.ExploreDecision, keyword string) (string, bool) {
	switch ai.ExploreActionType(d.Action) {
	case ai.ExploreClick:
		if d.Target == "" {
			return "click 缺少目标文案", false
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavClick,
			Text: d.Target,
		})
		if err != nil {
			return "点击失败: " + err.Error(), false
		}
		return resp.Message, resp.OK

	case ai.ExploreScroll:
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavScroll,
		})
		if err != nil {
			return "滚动失败: " + err.Error(), false
		}
		return resp.Message, resp.OK

	case ai.ExploreNavigate:
		if d.Target == "" {
			return "navigate 缺少目标 URL", false
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavNavigate,
			URL:  d.Target,
		})
		if err != nil {
			return "导航失败: " + err.Error(), false
		}
		return resp.Message, resp.OK

	case ai.ExploreWait:
		secs := 2
		if n, err := strconv.Atoi(d.Target); err == nil && n > 0 && n <= 10 {
			secs = n
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavWait, Seconds: secs,
		})
		if err != nil {
			return "等待失败: " + err.Error(), false
		}
		return resp.Message, resp.OK

	case ai.ExploreInspect:
		// inspect 只是标记感兴趣的请求，实际内容已在 candidates.sample 中；
		// 这里不需要额外动作，返回提示让模型继续。
		return "已标记请求 " + d.Target + "，其内容已在候选列表中", true

	default:
		return "未知动作: " + d.Action, false
	}
}

// toRequestViews 把 Worker 返回的候选转为模型视图。
func toRequestViews(cands []browser.RankedNetworkRecord) []ai.ExploreRequestView {
	out := make([]ai.ExploreRequestView, 0, len(cands))
	for _, c := range cands {
		out = append(out, ai.ExploreRequestView{
			Seq:     c.Record.Seq,
			Method:  c.Record.Method,
			URL:     c.Record.URL,
			Status:  c.Record.Status,
			Size:    c.Record.Size,
			Sample:  c.Record.Sample,
			Score:   c.Score,
			Reasons: c.Reasons,
		})
	}
	return out
}

// toElementViews 把页面快照元素转为模型视图（只保留文案与 ref）。
func toElementViews(snap *browser.ExploreSnapshot) []ai.ExploreElementView {
	if snap == nil {
		return nil
	}
	out := make([]ai.ExploreElementView, 0, len(snap.Elements))
	for _, el := range snap.Elements {
		text := strings.TrimSpace(el.Text)
		if text == "" {
			text = strings.TrimSpace(el.Placeholder)
		}
		if text == "" {
			text = strings.TrimSpace(el.AriaLabel)
		}
		if text == "" {
			continue
		}
		out = append(out, ai.ExploreElementView{
			Ref:  el.Ref,
			Text: text,
			Tag:  el.Tag,
			Type: el.InputType,
		})
	}
	return out
}

// mergeRequestViews 合并候选视图，按 seq 去重。
func mergeRequestViews(base, add []ai.ExploreRequestView) []ai.ExploreRequestView {
	seen := make(map[int]bool, len(base))
	for _, r := range base {
		seen[r.Seq] = true
	}
	for _, r := range add {
		if !seen[r.Seq] {
			base = append(base, r)
			seen[r.Seq] = true
		}
	}
	return base
}

// currentURLOr 安全取当前 URL。
func currentURLOr(snap *browser.ExploreSnapshot, fallback string) string {
	if snap != nil && snap.CurrentURL != "" {
		return snap.CurrentURL
	}
	return fallback
}

// titleOr 安全取页面标题。
func titleOr(snap *browser.ExploreSnapshot) string {
	if snap == nil {
		return ""
	}
	return snap.Title
}

// textSampleOr 安全取页面文本片段。
func textSampleOr(snap *browser.ExploreSnapshot) string {
	if snap == nil {
		return ""
	}
	return snap.TextSample
}

// cardSamplesOr 安全取卡片样本。
func cardSamplesOr(snap *browser.ExploreSnapshot) []string {
	if snap == nil {
		return nil
	}
	return snap.CardSamples
}

// joinReason 组合主原因与附加提示（如登录态警告）。
func joinReason(main, extra string) string {
	if extra == "" {
		return main
	}
	return main + "（" + extra + "）"
}

// 占位引用：site 包用于探索成功后写入 Recipe（由 discovery 层调用）。
var _ = site.SourceExploration
