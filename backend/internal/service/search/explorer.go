package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
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
//   - 所有动作经 Worker 执行，Worker 侧不执行任意 JS；
//   - navigate 仅允许 http/https 公网地址；
//   - 步数有上限，避免无限循环；
//   - 主流程只读取页面与网络响应，优先确认岗位列表路径。
type Explorer struct {
	browser *browser.Client
	llm     *ai.Client
	repo    *site.Repository

	// 多角色协作（显式 agent 拓扑，见 explorer_agents.go）。
	planner   planner
	actor     actor
	critic    critic
	guardrail guardrail
	memory    memory

	// maxSteps 单次探索的最大步数。
	maxSteps int
	// confidenceThreshold 达到该信心值即提前结束。
	confidenceThreshold int
	// topNCandidates 每步交给 LLM 的网络候选条数。
	topNCandidates int
}

// NewExplorer 构造探索器。
func NewExplorer(bc *browser.Client, llm *ai.Client, repo *site.Repository) *Explorer {
	e := &Explorer{
		browser:             bc,
		llm:                 llm,
		repo:                repo,
		maxSteps:            defaultExploreMaxSteps,
		confidenceThreshold: defaultConfidenceThreshold,
		topNCandidates:      defaultTopNCandidates,
	}
	// 角色持有回指，委托回 Explorer 既有方法，不引入新运行时行为。
	e.planner = planner{e: e}
	e.actor = actor{e: e}
	e.critic = critic{e: e}
	e.guardrail = guardrail{e: e}
	e.memory = memory{e: e}
	return e
}

// 探索流程的默认值。
const (
	// defaultExploreMaxSteps 单次探索最多走多少步，作为防止无界循环的安全边界。
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
	// RunID 是持久化探索记录 ID；失败时可据此直接查看完整轨迹。
	RunID string
	// Success 是否成功产出可用配置。
	Success bool
	// Candidate 产出的采集配置候选；失败时为 nil。
	Candidate *ai.RecipeCandidate
	// Observed 本次探索累计观测到的网络请求候选（含请求侧信息）。
	//
	// 保留它是为了支撑「验证失败 → 自修正」：模型重新分析时需要回看
	// 原始观测数据，否则只能凭错误信息盲改。
	Observed []ai.ExploreRequestView
	// DetailContents 是首次探索实际打开并通过 JD 质量检查的详情页正文，
	// key 为已验证的真实详情 URL。它只来自浏览器可见页面，不会触发接口复放。
	DetailContents map[string]string
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
	TargetRef  string `json:"target_ref,omitempty"`
	Target     string `json:"target"`
	Reasoning  string `json:"reasoning"`
	Result     string `json:"result"`
	Confidence int    `json:"confidence"`
	// CurrentURL 是动作完成后的页面地址；保存 Recipe 时用于确定下次的入口页。
	CurrentURL string `json:"current_url,omitempty"`
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
	defer e.recordExploreRun(context.WithoutCancel(ctx), siteKey, req, result)

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

	// Observer 是打开页面后才启动的，主动重载一次避免错过首屏列表接口。
	if _, navErr := e.browser.NavAct(ctx, taskID, browser.NavAction{
		Type: browser.NavNavigate,
		URL:  session.CurrentURL,
	}); navErr != nil {
		slog.Warn("探索重载页面失败", "error", navErr.Error())
	}
	// 首屏请求可能就是列表接口，先观测一次。搜索必须等页面快照提供了
	// 精确输入框 ref 后再执行，不能在这里用 CSS 全页扫描猜目标元素。
	time.Sleep(exploreWaitAfterAction * time.Second)
	observeCursor, _, candidates, err := e.browser.ObserveDiff(ctx, taskID, baseline, 50, e.topNCandidates)
	if err != nil {
		slog.Warn("首屏观测失败", "error", err.Error())
	}

	// 记录全部观测到的候选，供最终 Builder 使用。
	allCandidates := make([]ai.ExploreRequestView, 0, 32)
	allCandidates = append(allCandidates, toRequestViews(candidates)...)
	keywordAttempted := false
	lastResult := "已进入站点首页"
	playbook := e.memory.Recall(ctx)
	var decision *ai.ExploreDecision
	modelFinished := false
	lastFingerprint := ""
	stagnantSteps := 0
	inspectedSeqs := map[int]bool{}

	for step := 1; step <= e.maxSteps; step++ {
		// 观测当前页面。
		snap, snapErr := e.browser.Snapshot(ctx, taskID)
		if snapErr != nil {
			slog.Warn("读取页面快照失败", "step", step, "error", snapErr.Error())
		}

		fingerprint := pageFingerprint(snap, session.CurrentURL)
		if fingerprint != "" && fingerprint == lastFingerprint {
			stagnantSteps++
		} else {
			stagnantSteps = 0
			lastFingerprint = fingerprint
		}

		obs := ai.ExploreObservation{
			Step:             step,
			CurrentURL:       currentURLOr(snap, session.CurrentURL),
			PageTitle:        titleOr(snap),
			TextSample:       textSampleOr(snap),
			CardSamples:      cardSamplesOr(snap),
			Playbook:         playbook,
			Elements:         toElementViews(snap),
			NewRequests:      toRequestViews(candidates),
			LastActionResult: lastResult,
			ActionHistory:    recentActionHistory(result.Trace, 6),
			ProgressHint:     e.critic.ProgressHint(stagnantSteps, lastResult),
		}

		// 模型决策。
		decision, err = e.planner.Decide(ctx, obs)
		if err != nil {
			result.Reason = "LLM 决策失败: " + err.Error()
			break
		}

		// Guardrail：执行前拦截 / 改写高风险与无意义决策（重复 inspect、越界导航等）。
		proceed, override, guardReason := e.guardrail.Check(
			step, decision, req.Keyword, keywordAttempted, obs,
			candidates, inspectedSeqs, allCandidates,
		)
		if override != nil {
			decision = override
		}
		if !proceed {
			result.Trace = append(result.Trace, ExploreStepTrace{
				Step:       step,
				Action:     decision.Action,
				Target:     decision.Target,
				Reasoning:  guardReason,
				Result:     guardReason,
				Confidence: decision.Confidence,
			})
			lastResult = guardReason
			continue
		}

		trace := ExploreStepTrace{
			Step:        step,
			Action:      decision.Action,
			TargetRef:   decision.TargetRef,
			Target:      decision.Target,
			Reasoning:   decision.Reasoning,
			Confidence:  decision.Confidence,
			NewRequests: len(candidates),
		}

		// 每步都落日志：探索是耗时数十秒的多步流程，
		// 失败时若只有最终结论，根本判断不出是「没走到列表页」
		// 还是「走到了但没认出接口」——两者的修法完全不同。
		slog.Info("探索步骤",
			"site_key", siteKey,
			"step", step,
			"action", decision.Action,
			"target_ref", decision.TargetRef,
			"target", truncateForLog(decision.Target, 120),
			"confidence", decision.Confidence,
			"new_requests", len(candidates),
			"reasoning", truncateForLog(decision.Reasoning, 100))

		// 结束条件。
		if decision.Action == string(ai.ExploreFinish) {
			trace.Result = "判定已找到数据接口"
			result.Trace = append(result.Trace, trace)
			result.Steps = step
			modelFinished = true
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
		if decision.Action == string(ai.ExploreInspect) {
			seq := inspectSeq(decision.Target, decision.TargetRef)
			if seq > 0 && inspectedSeqs[seq] {
				trace.Result = fmt.Sprintf("拒绝重复 inspect 请求 %d；该请求已读取且没有新候选，请选择其他页面动作", seq)
				result.Trace = append(result.Trace, trace)
				lastResult = trace.Result
				continue
			}
		}

		// 执行动作。
		execRes := e.actor.Act(ctx, taskID, decision, req.Keyword)
		execMsg, execOK := execRes.Message, execRes.OK
		trace.Result = execMsg
		trace.CurrentURL = execRes.CurrentURL
		lastResult = execMsg
		if execOK && actionUsesKeyword(decision, req.Keyword) {
			keywordAttempted = true
		}
		if execOK && decision.Action == string(ai.ExploreInspect) {
			if seq := inspectSeq(decision.Target, decision.TargetRef); seq > 0 {
				inspectedSeqs[seq] = true
			}
		}
		result.Trace = append(result.Trace, trace)
		if execRes.Inspected != nil {
			allCandidates = mergeRequestViews(allCandidates, []ai.ExploreRequestView{*execRes.Inspected})
			candidates = mergeRankedRequests(candidates, *execRes.Inspected)
		}

		// 动作后等待异步请求完成，再观测新增请求与页面状态。
		time.Sleep(exploreWaitAfterAction * time.Second)
		observeCursor, _, candidates, err = e.browser.ObserveDiff(ctx, taskID, observeCursor, 50, e.topNCandidates)
		if err != nil {
			slog.Warn("观测新增请求失败", "step", step, "error", err.Error())
			candidates = nil
		}
		afterSnap, afterSnapErr := e.browser.Snapshot(ctx, taskID)
		stateChanged := afterSnapErr == nil && actionChangedPage(snap, afterSnap)
		if execOK && !stateChanged && len(candidates) == 0 && decision.Action != string(ai.ExploreInspect) {
			execOK = false
			execMsg += "；未观测到页面或候选数据变化"
			lastResult = execMsg
		}
		trace.NewRequests = len(candidates)
		trace.Result = execMsg
		// 打印本步新观测到的候选接口，便于判断导航是否真的走到了列表页。
		// 只打 URL 与打分，不打响应内容（可能含个人信息）。
		for _, c := range candidates {
			slog.Debug("探索观测候选",
				"step", step,
				"method", c.Record.Method,
				"url", truncateForLog(c.Record.URL, 160),
				"status", c.Record.Status,
				"score", c.Score,
				"has_request_body", c.Record.RequestBody != "")
		}
		if n := len(result.Trace); n > 0 {
			result.Trace[n-1].NewRequests = len(candidates)
			result.Trace[n-1].Result = execMsg
		}
		// 累计候选，去重后供 Builder 使用。
		allCandidates = mergeRequestViews(allCandidates, toRequestViews(candidates))

		if !execOK {
			// 连续失败会让模型自行调整，这里只记录不中断。
			slog.Debug("探索动作执行失败", "step", step, "action", decision.Action, "msg", execMsg)
		}

		if step == e.maxSteps {
			result.Steps = step
		}
	}

	// 步数上限只是安全边界，不是“已找到接口”的替代判断。只有模型明确
	// finish 后才允许生成并沉淀 Recipe，避免规则路径意外产出错误配置。
	if !modelFinished {
		if result.Reason == "" {
			result.Reason = fmt.Sprintf("达到探索步数上限（%d），模型未确认岗位列表路径", e.maxSteps)
		}
		result.DurationMS = time.Since(start).Milliseconds()
		return result, nil
	}

	// 由观测数据产出配置候选。
	// 无论后续成败都带上观测数据：自修正阶段要靠它回看原始请求。
	result.Observed = allCandidates
	if len(allCandidates) == 0 {
		result.Reason = joinReason("未观测到任何可用的数据请求", loginWarn)
		result.DurationMS = time.Since(start).Milliseconds()
		return result, nil
	}

	// 同一页面可能并行出现埋点、默认列表和搜索结果等多种请求。用户给了
	// 关键词时，配置只能基于实际携带该关键词的强列表信号生成，避免模型
	// 从混合候选中误选默认列表请求。
	candidateInputs := recipeCandidateInputs(allCandidates, req.Keyword)
	candidate, err := e.llm.BuildRecipeCandidate(ctx, candidateInputs, req.EntryURL)
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
	if strings.TrimSpace(candidate.ListPath) == "" || strings.TrimSpace(candidate.TitleField) == "" {
		result.Reason = joinReason("已识别列表接口，但缺少 list_path 或 title_field，暂不保存为可执行 Recipe", loginWarn)
		result.DurationMS = time.Since(start).Milliseconds()
		return result, nil
	}
	// Builder 能从请求模式中推测详情地址，但推测不能写入 Recipe。详情路径
	// 必须由当前浏览器实际打开的页面反推，避免把幻觉 URL 带入 Fast Path。
	candidate.DetailAPI = ""
	candidate.DetailURLTemplate = ""
	if candidateNeedsDetail(candidate) {
		detail := e.discoverDetailPage(ctx, taskID, candidate, allCandidates)
		if detail != nil {
			candidate.IDField = detail.Identifier.Field
			if candidate.FieldMap == nil {
				candidate.FieldMap = map[string]string{}
			}
			candidate.FieldMap["id"] = detail.Identifier.Field
			candidate.DetailURLTemplate = detail.Template
			result.DetailContents = map[string]string{detail.URL: detail.Content}
			slog.Info("探索确认岗位详情页模板", "site_key", siteKey, "template", truncateForLog(detail.Template, 160))
		}
	}

	result.Success = true
	result.Candidate = candidate
	result.DurationMS = time.Since(start).Milliseconds()
	e.memory.Learn(context.WithoutCancel(ctx), candidate, allCandidates)

	slog.Info("站点探索完成",
		"site_key", siteKey,
		"steps", result.Steps,
		"list_api", candidate.ListAPI,
		"confidence", candidate.Confidence,
		"duration_ms", result.DurationMS)
	return result, nil
}

// recordExploreRun 在所有正常探索返回路径上保存结果，避免失败只剩一句最终错误。
func (e *Explorer) recordExploreRun(ctx context.Context, siteKey string, req ExploreRequest, result *ExploreResult) {
	if e.repo == nil || result == nil {
		return
	}
	trace, err := json.Marshal(result.Trace)
	if err != nil {
		slog.Warn("序列化探索轨迹失败", "site_key", siteKey, "error", err.Error())
		return
	}
	status := "failed"
	if result.Success {
		status = "success"
	}
	run := &site.ExplorationRun{
		SiteKey:    siteKey,
		Keyword:    req.Keyword,
		EntryURL:   req.EntryURL,
		Status:     status,
		Reason:     result.Reason,
		Trace:      trace,
		DurationMS: result.DurationMS,
	}
	if err := e.repo.RecordExplorationRun(ctx, run); err != nil {
		slog.Warn("保存探索运行记录失败", "site_key", siteKey, "error", err.Error())
		return
	}
	result.RunID = run.ID
}

type discoveredDetailPage struct {
	Identifier executor.JobIdentifier
	Template   string
	URL        string
	Content    string
}

// discoverDetailPage 从一次真实点击得到详情页，并将 URL 中的列表字段精确反推为模板。
// 列表没有 JD 时，详情页不是优化项，而是 Recipe 可用性的必要条件。
func (e *Explorer) discoverDetailPage(
	ctx context.Context,
	taskID string,
	cand *ai.RecipeCandidate,
	observed []ai.ExploreRequestView,
) *discoveredDetailPage {
	if cand == nil {
		return nil
	}
	draft := &site.Recipe{
		ListAPI:    cand.ListAPI,
		ListPath:   cand.ListPath,
		IDField:    cand.IDField,
		TitleField: cand.TitleField,
		FieldMap:   jsonMapFromStrings(cand.FieldMap),
	}
	var identifiers []executor.JobIdentifier
	for _, req := range observed {
		if !sameObservedEndpoint(cand.ListAPI, req.URL) {
			continue
		}
		body := firstNonEmpty(req.FullSample, req.Sample)
		if body == "" {
			continue
		}
		parsed, err := executor.APIJobIdentifiers(draft, []byte(body))
		if err == nil && len(parsed) > 0 {
			identifiers = parsed
			break
		}
	}
	if len(identifiers) == 0 {
		return nil
	}
	snap, err := e.browser.Snapshot(ctx, taskID)
	if err != nil {
		return nil
	}

	// 有些 SPA 职位卡片不是 <a>，只有点击后才改变路由。此处最多请求模型
	// 选择一次代表岗位，且只接受 click，避免列表已确认后重新进入无界探索。
	decision, err := e.planner.Decide(ctx, ai.ExploreObservation{
		Step:             0,
		CurrentURL:       snap.CurrentURL,
		PageTitle:        snap.Title,
		TextSample:       snap.TextSample,
		CardSamples:      snap.CardSamples,
		Elements:         toElementViews(snap),
		NewRequests:      observed,
		LastActionResult: "岗位列表已确认；请只点击一条普通岗位卡片或岗位标题以确认真实详情页 URL。不要搜索、翻页或检查接口。",
	})
	if err != nil || decision.Action != string(ai.ExploreClick) || strings.TrimSpace(decision.TargetRef) == "" {
		return nil
	}
	if result := e.actor.Act(ctx, taskID, decision, ""); !result.OK {
		return nil
	}
	if _, err := e.browser.NavAct(ctx, taskID, browser.NavAction{Type: browser.NavWait, Seconds: exploreWaitAfterAction}); err != nil {
		slog.Debug("详情页等待失败", "error", err.Error())
	}
	page, err := e.browser.ScrapePage(ctx, taskID)
	if err != nil || page == nil || page.NeedsLogin {
		return nil
	}
	content, quality := DetectDescriptionQuality(page.PageText)
	if quality == DescQualityEmpty {
		return nil
	}
	identifier, template, ok := detailTemplateFromURL(page.CurrentURL, identifiers)
	if !ok {
		return nil
	}
	return &discoveredDetailPage{Identifier: identifier, Template: template, URL: page.CurrentURL, Content: content}
}

func candidateNeedsDetail(cand *ai.RecipeCandidate) bool {
	if cand == nil {
		return false
	}
	for _, key := range []string{"description", "work_content", "responsibilities", "requirements"} {
		if strings.TrimSpace(cand.FieldMap[key]) != "" {
			return false
		}
	}
	return true
}

func detailTemplateFromURL(rawURL string, identifiers []executor.JobIdentifier) (executor.JobIdentifier, string, bool) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return executor.JobIdentifier{}, "", false
	}
	values := map[string]bool{}
	for _, value := range u.Query() {
		for _, item := range value {
			values[item] = true
		}
	}
	for _, segment := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
		if decoded, err := url.PathUnescape(segment); err == nil && decoded != "" {
			values[decoded] = true
		}
	}
	for _, id := range identifiers {
		if id.Value == "" || !values[id.Value] {
			continue
		}
		escaped := url.QueryEscape(id.Value)
		if strings.Contains(rawURL, escaped) {
			return id, strings.Replace(rawURL, escaped, "{id}", 1), true
		}
		if strings.Contains(rawURL, id.Value) {
			return id, strings.Replace(rawURL, id.Value, "{id}", 1), true
		}
	}
	return executor.JobIdentifier{}, "", false
}

func (e *Explorer) loadPlaybook(ctx context.Context) []ai.ExploreTacticView {
	if e.repo == nil {
		return nil
	}
	tactics, err := e.repo.ListPlaybook(ctx, 8)
	if err != nil {
		slog.Warn("读取探索 Playbook 失败", "error", err.Error())
		return nil
	}
	out := make([]ai.ExploreTacticView, 0, len(tactics))
	for _, t := range tactics {
		out = append(out, ai.ExploreTacticView{
			Key:         t.TacticKey,
			Title:       t.Title,
			Description: t.Description,
			HitCount:    t.HitCount,
		})
	}
	return out
}

func (e *Explorer) recordPlaybookHits(ctx context.Context, cand *ai.RecipeCandidate, reqs []ai.ExploreRequestView) {
	if e.repo == nil || cand == nil {
		return
	}
	keys := inferPlaybookHits(cand, reqs)
	if len(keys) == 0 {
		return
	}
	if err := e.repo.RecordPlaybookHits(ctx, keys); err != nil {
		slog.Warn("记录探索 Playbook 命中失败", "error", err.Error())
	}
}

// truncateForLog 按字符（而非字节）截断日志字段，避免中文被切成乱码。
func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func inferPlaybookHits(cand *ai.RecipeCandidate, reqs []ai.ExploreRequestView) []string {
	keys := []string{"observe_initial_network", "inspect_ranked_json_schema"}
	if strings.TrimSpace(cand.KeywordParam) != "" || strings.Contains(cand.ListAPI, "?") {
		keys = append(keys, "try_keyword_query")
	}
	if len(cand.FieldMap) > 0 {
		keys = append(keys, "identify_semantic_field_map")
	}
	if strings.TrimSpace(cand.DetailAPI) != "" || strings.TrimSpace(cand.DetailURLTemplate) != "" {
		keys = append(keys, "find_detail_template")
	}
	// 请求体被成功沉淀，说明「照抄观测请求」这条经验起了作用。
	if strings.TrimSpace(cand.RequestBody) != "" {
		keys = append(keys, "copy_observed_request_body")
	}
	for _, r := range reqs {
		if strings.TrimSpace(r.SchemaSummary) != "" {
			keys = append(keys, "inspect_ranked_json_schema")
			break
		}
	}
	return dedupStrings(keys, 8)
}

func strongestJobListSignal(reqs []ai.ExploreRequestView, keyword string) *ai.ExploreRequestView {
	var best *ai.ExploreRequestView
	for i := range reqs {
		r := &reqs[i]
		if !hasStrongJobListSignal(*r, keyword) {
			continue
		}
		if best == nil || r.Score > best.Score {
			best = r
		}
	}
	return best
}

func recipeCandidateInputs(reqs []ai.ExploreRequestView, keyword string) []ai.ExploreRequestView {
	if strings.TrimSpace(keyword) == "" {
		return reqs
	}
	if strong := strongestJobListSignal(reqs, keyword); strong != nil {
		return []ai.ExploreRequestView{*strong}
	}
	return reqs
}

func hasStrongJobListSignal(r ai.ExploreRequestView, keyword string) bool {
	if r.Status < 200 || r.Status >= 300 {
		return false
	}
	text := strings.ToLower(r.Sample + "\n" + r.FullSample + "\n" + r.SchemaSummary)
	if !looksLikeJSONJobArray(text) {
		return false
	}
	if !hasJobTitleSignal(text) || !hasJobContentSignal(text) {
		return false
	}
	if !keywordObservedInRequest(r, keyword) {
		return false
	}
	return r.Score >= 80 || (hasPaginationSignal(r) && strings.TrimSpace(r.RequestBody) != "")
}

func looksLikeJSONJobArray(text string) bool {
	return strings.Contains(text, "array[") ||
		strings.Contains(text, `"list"`) ||
		strings.Contains(text, `"items"`) ||
		strings.Contains(text, `"records"`) ||
		strings.Contains(text, `"positionlist"`) ||
		strings.Contains(text, `"jobs"`)
}

func hasJobTitleSignal(text string) bool {
	for _, token := range []string{"positionname", "positiontitle", "jobname", "jobtitle", "title"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func hasJobContentSignal(text string) bool {
	for _, token := range []string{
		"workcontent", "qualification", "requirement", "responsibilit",
		"jobduty", "workduty", "description", "duty",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func hasPaginationSignal(r ai.ExploreRequestView) bool {
	text := strings.ToLower(r.URL + "\n" + r.RequestBody + "\n" + r.SchemaSummary)
	for _, token := range []string{"page", "pageno", "pageindex", "pagesize", "limit", "offset", "total"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func keywordObservedInRequest(r ai.ExploreRequestView, keyword string) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return true
	}
	return containsFold(r.URL, keyword) || containsFold(r.RequestBody, keyword)
}

func observedKeywordRequest(reqs []ai.ExploreRequestView, keyword string) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return true
	}
	for _, r := range reqs {
		if keywordObservedInRequest(r, keyword) {
			return true
		}
	}
	return false
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

type exploreActionResult struct {
	Message    string
	OK         bool
	CurrentURL string
	Inspected  *ai.ExploreRequestView
}

// executeAction 执行模型决策的单个动作，返回执行结果与是否成功。
func (e *Explorer) executeAction(ctx context.Context, taskID string, d *ai.ExploreDecision, keyword string) exploreActionResult {
	switch ai.ExploreActionType(d.Action) {
	case ai.ExploreClick:
		if d.TargetRef != "" {
			resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
				Type: browser.NavClickRef,
				Ref:  d.TargetRef,
			})
			if err != nil {
				return exploreActionResult{Message: "点击失败: " + err.Error()}
			}
			return exploreActionResult{Message: resp.Message, OK: resp.OK, CurrentURL: resp.CurrentURL}
		}
		if d.Target == "" {
			return exploreActionResult{Message: "click 缺少 target_ref 或目标文案"}
		}
		// 兼容旧模型输出；新探索应优先使用 target_ref。
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavClick,
			Text: d.Target,
		})
		if err != nil {
			return exploreActionResult{Message: "点击失败: " + err.Error()}
		}
		return exploreActionResult{Message: resp.Message, OK: resp.OK, CurrentURL: resp.CurrentURL}

	case ai.ExploreInput:
		if d.TargetRef == "" {
			return exploreActionResult{Message: "input 缺少 target_ref"}
		}
		text := firstNonEmpty(d.Target, keyword)
		if text == "" {
			return exploreActionResult{Message: "input 缺少输入文本"}
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavInputRef,
			Ref:  d.TargetRef,
			Text: text,
		})
		if err != nil {
			return exploreActionResult{Message: "输入失败: " + err.Error()}
		}
		return exploreActionResult{Message: resp.Message, OK: resp.OK, CurrentURL: resp.CurrentURL}

	case ai.ExploreScroll:
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavScroll,
		})
		if err != nil {
			return exploreActionResult{Message: "滚动失败: " + err.Error()}
		}
		return exploreActionResult{Message: resp.Message, OK: resp.OK, CurrentURL: resp.CurrentURL}

	case ai.ExploreNavigate:
		if d.Target == "" {
			return exploreActionResult{Message: "navigate 缺少目标 URL"}
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavNavigate,
			URL:  d.Target,
		})
		if err != nil {
			return exploreActionResult{Message: "导航失败: " + err.Error()}
		}
		return exploreActionResult{Message: resp.Message, OK: resp.OK, CurrentURL: resp.CurrentURL}

	case ai.ExploreWait:
		secs := 2
		if n, err := strconv.Atoi(d.Target); err == nil && n > 0 && n <= 10 {
			secs = n
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type: browser.NavWait, Seconds: secs,
		})
		if err != nil {
			return exploreActionResult{Message: "等待失败: " + err.Error()}
		}
		return exploreActionResult{Message: resp.Message, OK: resp.OK}

	case ai.ExploreSearch:
		searchKeyword := preferredSearchKeyword(keyword, d.Target)
		if searchKeyword == "" {
			return exploreActionResult{Message: "search 缺少关键词"}
		}
		if strings.TrimSpace(d.TargetRef) == "" {
			return exploreActionResult{Message: "search 缺少当前页面搜索框 target_ref"}
		}
		resp, err := e.browser.NavAct(ctx, taskID, browser.NavAction{
			Type:    browser.NavSearch,
			Ref:     d.TargetRef,
			Keyword: searchKeyword,
		})
		if err != nil {
			return exploreActionResult{Message: "搜索失败: " + err.Error()}
		}
		return exploreActionResult{Message: resp.Message, OK: resp.OK, CurrentURL: resp.CurrentURL}

	case ai.ExploreInspect:
		seq := inspectSeq(d.Target, d.TargetRef)
		if seq <= 0 {
			return exploreActionResult{Message: "inspect 缺少有效请求 seq"}
		}
		record, err := e.browser.InspectRequest(ctx, taskID, seq)
		if err != nil {
			return exploreActionResult{Message: "inspect 失败: " + err.Error()}
		}
		view := requestViewFromRecord(record, 1000, []string{"已 inspect，包含更完整响应片段"})
		return exploreActionResult{
			Message:   fmt.Sprintf("已读取请求 %d 的完整响应片段（%d 字符）", seq, len([]rune(view.FullSample))),
			OK:        true,
			Inspected: &view,
		}

	default:
		return exploreActionResult{Message: "未知动作: " + d.Action}
	}
}

func preferredSearchKeyword(userKeyword, modelTarget string) string {
	if k := strings.TrimSpace(userKeyword); k != "" {
		return k
	}
	return strings.TrimSpace(modelTarget)
}

func firstPositiveInt(values ...string) int {
	for _, value := range values {
		n := positiveIntInString(value)
		if n > 0 {
			return n
		}
	}
	return 0
}

func inspectSeq(target, targetRef string) int {
	if n := explicitInspectSeq(target); n > 0 {
		return n
	}
	if n := explicitInspectSeq(targetRef); n > 0 {
		return n
	}
	return 0
}

func firstUninspectedRequest(candidates []ai.ExploreRequestView, inspected map[int]bool) int {
	for _, c := range candidates {
		seq := c.Seq
		if seq > 0 && !inspected[seq] {
			return seq
		}
	}
	return 0
}

func explicitInspectSeq(value string) int {
	s := strings.ToLower(strings.TrimSpace(value))
	if s == "" {
		return 0
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	if strings.HasPrefix(s, "seq:") || strings.HasPrefix(s, "seq=") {
		return positiveIntInString(s)
	}
	if strings.Contains(s, "seq ") || strings.Contains(s, "seq=") || strings.Contains(s, "seq:") {
		return positiveIntInString(s)
	}
	return 0
}

func positiveIntInString(value string) int {
	start := -1
	for i, r := range value {
		if r >= '0' && r <= '9' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			n, _ := strconv.Atoi(value[start:i])
			return n
		}
	}
	if start >= 0 {
		n, _ := strconv.Atoi(value[start:])
		return n
	}
	return 0
}

// toRequestViews 把 Worker 返回的候选转为模型视图。
//
// 必须完整带上请求侧字段（RequestBody / RequestContentType / RequestHeaders）：
// 少了它们，模型就只能"看见响应、猜不出请求"，
// 最终只能靠人工在代码里补站点特判分支——这正是要消除的模式。
func toRequestViews(cands []browser.RankedNetworkRecord) []ai.ExploreRequestView {
	out := make([]ai.ExploreRequestView, 0, len(cands))
	for _, c := range cands {
		out = append(out, requestViewFromRecord(&c.Record, c.Score, c.Reasons))
	}
	return out
}

func requestViewFromRecord(record *browser.NetworkRecord, score int, reasons []string) ai.ExploreRequestView {
	if record == nil {
		return ai.ExploreRequestView{}
	}
	return ai.ExploreRequestView{
		Seq:                record.Seq,
		Method:             record.Method,
		URL:                record.URL,
		Status:             record.Status,
		Size:               record.Size,
		Sample:             record.Sample,
		FullSample:         record.FullSample,
		SchemaSummary:      record.SchemaSummary,
		RequestBody:        record.RequestBody,
		RequestContentType: record.RequestContentType,
		RequestHeaders:     record.RequestHeaders,
		Score:              score,
		Reasons:            reasons,
	}
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
			Href: el.Href,
		})
	}
	return out
}

// searchInputRef 从当前快照挑选一个可编辑的搜索输入框。它只做页面语义排序，
// 不保存任何站点选择器；最终执行仍由 Worker 用这个瞬时 ref 精确定位。
func searchInputRef(elements []ai.ExploreElementView, requested string) string {
	requested = strings.TrimSpace(requested)
	for _, el := range elements {
		if el.Ref == requested && isSearchInputElement(el) {
			return el.Ref
		}
	}
	bestRef, bestScore := "", 0
	for _, el := range elements {
		if !isSearchInputElement(el) {
			continue
		}
		if score := searchInputScore(el.Text); score > bestScore {
			bestRef, bestScore = el.Ref, score
		}
	}
	if bestRef != "" {
		return bestRef
	}
	for _, el := range elements {
		if isSearchInputElement(el) {
			return el.Ref
		}
	}
	return ""
}

func isSearchInputElement(el ai.ExploreElementView) bool {
	tag := strings.ToLower(strings.TrimSpace(el.Tag))
	typ := strings.ToLower(strings.TrimSpace(el.Type))
	if tag == "textarea" {
		return true
	}
	if tag != "input" {
		return false
	}
	return typ == "" || typ == "text" || typ == "search"
}

func searchInputScore(text string) int {
	text = strings.ToLower(strings.TrimSpace(text))
	score := 0
	for _, token := range []string{"岗位", "职位", "keyword", "position", "job"} {
		if strings.Contains(text, token) {
			score += 2
		}
	}
	for _, token := range []string{"搜索", "search"} {
		if strings.Contains(text, token) {
			score++
		}
	}
	return score
}

func recentActionHistory(trace []ExploreStepTrace, limit int) []string {
	if limit <= 0 || len(trace) == 0 {
		return nil
	}
	start := len(trace) - limit
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, len(trace)-start)
	for _, item := range trace[start:] {
		target := firstNonEmpty(item.TargetRef, item.Target, "无目标")
		out = append(out, fmt.Sprintf("第%d步 %s(%s)：%s", item.Step, item.Action, target, truncateForLog(item.Result, 180)))
	}
	return out
}

func pageFingerprint(snap *browser.ExploreSnapshot, fallbackURL string) string {
	if snap == nil {
		return ""
	}
	text := truncateForLog(snap.TextSample, 600)
	return fmt.Sprintf("%s|%s|%d|%d|%s",
		currentURLOr(snap, fallbackURL),
		titleOr(snap),
		len(snap.Elements),
		len(snap.CardSamples),
		text,
	)
}

// actionChangedPage 判断动作是否改变了可见页面状态。输入框本身的写值不算成功，
// 搜索/点击必须带来列表、页面或 URL 的可观察变化。
func actionChangedPage(before, after *browser.ExploreSnapshot) bool {
	if before == nil || after == nil {
		return false
	}
	return pageFingerprint(before, "") != pageFingerprint(after, "")
}

func progressHint(stagnantSteps int, lastResult string) string {
	hints := make([]string, 0, 2)
	if stagnantSteps >= 2 {
		hints = append(hints, "页面状态已连续多步没有变化；不要重复同一点击/等待，应尝试其他入口、搜索、滚动或 inspect 网络候选。")
	}
	if strings.Contains(lastResult, "失败") || strings.Contains(lastResult, "不存在或已过期") || strings.Contains(lastResult, "未找到") {
		hints = append(hints, "上一步动作没有成功；如果使用了过期 ref，需要先重新根据当前 elements 选择新的 target_ref。")
	}
	return strings.Join(hints, " ")
}

func shouldUseKeywordProbe(d *ai.ExploreDecision, confidenceThreshold int) bool {
	if d == nil {
		return false
	}
	switch d.Action {
	case string(ai.ExploreFinish), string(ai.ExploreWait), string(ai.ExploreScroll), string(ai.ExploreInspect):
		return true
	default:
		return d.Confidence >= confidenceThreshold
	}
}

func shouldProbeKeyword(keyword string, obs ai.ExploreObservation, reqs []ai.ExploreRequestView, attempted bool) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || attempted {
		return false
	}
	if !hasSearchInputElement(obs.Elements) {
		return false
	}
	if containsFold(obs.CurrentURL, keyword) {
		return false
	}
	for _, r := range reqs {
		if containsFold(r.URL, keyword) || containsFold(r.RequestBody, keyword) {
			return false
		}
	}
	for _, r := range obs.NewRequests {
		if containsFold(r.URL, keyword) || containsFold(r.RequestBody, keyword) {
			return false
		}
	}
	return true
}

func actionUsesKeyword(d *ai.ExploreDecision, keyword string) bool {
	if d == nil || strings.TrimSpace(keyword) == "" {
		return false
	}
	switch ai.ExploreActionType(d.Action) {
	case ai.ExploreSearch:
		return true
	default:
		return false
	}
}

func hasSearchInputElement(elements []ai.ExploreElementView) bool {
	for _, el := range elements {
		if isSearchInputElement(el) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	s = strings.TrimSpace(s)
	sub = strings.TrimSpace(sub)
	if s == "" || sub == "" {
		return false
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

// mergeRequestViews 合并候选视图，按 seq 去重。
func mergeRequestViews(base, add []ai.ExploreRequestView) []ai.ExploreRequestView {
	seen := make(map[int]int, len(base))
	for i, r := range base {
		seen[r.Seq] = i
	}
	for _, r := range add {
		if idx, ok := seen[r.Seq]; ok {
			base[idx] = richerRequestView(base[idx], r)
			continue
		}
		base = append(base, r)
		seen[r.Seq] = len(base) - 1
	}
	return base
}

func richerRequestView(a, b ai.ExploreRequestView) ai.ExploreRequestView {
	out := a
	if len([]rune(b.FullSample)) > len([]rune(out.FullSample)) {
		out.FullSample = b.FullSample
	}
	if len([]rune(b.Sample)) > len([]rune(out.Sample)) {
		out.Sample = b.Sample
	}
	if len([]rune(b.SchemaSummary)) > len([]rune(out.SchemaSummary)) {
		out.SchemaSummary = b.SchemaSummary
	}
	if b.Score > out.Score {
		out.Score = b.Score
	}
	if len(b.Reasons) > len(out.Reasons) {
		out.Reasons = b.Reasons
	}
	if b.RequestBody != "" {
		out.RequestBody = b.RequestBody
	}
	if b.RequestContentType != "" {
		out.RequestContentType = b.RequestContentType
	}
	if len(b.RequestHeaders) > 0 {
		out.RequestHeaders = b.RequestHeaders
	}
	return out
}

func mergeRankedRequests(base []browser.RankedNetworkRecord, view ai.ExploreRequestView) []browser.RankedNetworkRecord {
	record := browser.NetworkRecord{
		Seq:                view.Seq,
		Method:             view.Method,
		URL:                view.URL,
		Status:             view.Status,
		Size:               view.Size,
		Sample:             view.Sample,
		FullSample:         view.FullSample,
		SchemaSummary:      view.SchemaSummary,
		RequestBody:        view.RequestBody,
		RequestContentType: view.RequestContentType,
		RequestHeaders:     view.RequestHeaders,
	}
	for i, r := range base {
		if r.Record.Seq != view.Seq {
			continue
		}
		base[i] = browser.RankedNetworkRecord{
			Record:  record,
			Score:   view.Score,
			Reasons: view.Reasons,
		}
		return base
	}
	return append(base, browser.RankedNetworkRecord{
		Record:  record,
		Score:   view.Score,
		Reasons: view.Reasons,
	})
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
