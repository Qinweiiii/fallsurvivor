package search

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/site"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

/**
 * 验证-自修正闭环。
 *
 * 这是让 Agent「自己把路径摸对」而不是靠人补代码的关键环节。
 *
 * 闭环的完整链路：
 *
 *	探索（看页面 + 看真实网络请求，含请求体）
 *	  → Builder 产出配置
 *	  → **Executor 用真实请求独立试跑**
 *	  → 失败：把「错误 + 实际响应 + 上次配置」喂回模型让它自己改
 *	  → 最多 maxRefineRounds 轮
 *	  → 只有验证通过的配置才落库
 *
 * 为什么必须有这一环：
 *   没有验证时，模型产出的配置直接落库，错了也没人知道——
 *   最终暴露为「采集不到数据」，只能由人去翻日志、猜原因、改代码。
 *   有了验证，错误在探索阶段就被闭环消化掉，人不需要介入。
 *
 * 为什么上限是 3 轮：
 *   每轮都要一次 LLM 调用 + 一次真实请求。观测表明前两轮能修掉
 *   绝大多数「路径写错 / 请求体不对」的问题；继续重试收益递减，
 *   且反复请求同一接口有触发站点限流的风险。
 */

// maxRefineRounds 自修正的最大轮数（不含首次尝试）。
const maxRefineRounds = 3

// verifyKeywordFallback 验证时的兜底关键词。
//
// 用户可能没给关键词，但有些站点的列表接口在无关键词时返回空数组，
// 会被误判为「配置错误」。用一个宽泛的通用词兜底，
// 让验证聚焦于「路径与请求方式对不对」，而不是「这个词有没有岗位」。
const verifyKeywordFallback = "工程师"

/**
 * verifyAndRefine 验证候选配置，失败则驱动模型自修正。
 *
 * 返回最终通过验证的候选与验证结果。若始终未通过，
 * 返回最后一次的候选与失败结果（调用方据此决定是否降级保存）。
 *
 * 注意 browser 策略不参与本闭环：它依赖浏览器会话与登录态，
 * 无法在会话外独立复现，其正确性由探索过程本身保证。
 */
func (e *Explorer) verifyAndRefine(
	ctx context.Context,
	exec *executor.Executor,
	p DiscoveryParams,
	cand *ai.RecipeCandidate,
	observed []ai.ExploreRequestView,
) (*ai.RecipeCandidate, executor.VerifyResult, []ai.RecipeAttempt) {
	attempts := make([]ai.RecipeAttempt, 0, maxRefineRounds+1)

	keyword := strings.TrimSpace(p.Keyword)
	if keyword == "" {
		keyword = verifyKeywordFallback
	}

	current := cand
	var last executor.VerifyResult

	for round := 0; round <= maxRefineRounds; round++ {
		draft := recipeFromCandidate(p, current)
		last = exec.Verify(ctx, draft, keyword)

		if last.OK {
			if issue := recipeQualityIssue(last.FieldQuality); issue != "" {
				last.OK = false
				last.Error = issue
			} else {
				e.recordVerifySuccess(ctx, draft, last, round)
				return current, last, attempts
			}
		}
		if isHTTPForbidden(last.Error) {
			if observedOK := verifyWithObservedResponse(draft, current, observed, nil); observedOK.OK {
				if issue := recipeQualityIssue(observedOK.FieldQuality); issue != "" {
					observedOK.OK = false
					observedOK.Error = issue
				} else {
					slog.Info("探索配置需浏览器上下文复放，观测响应验证通过",
						"site_key", draft.SiteKey,
						"round", round,
						"jobs", observedOK.JobsFound,
						"sample_titles", strings.Join(observedOK.SampleTitles, " / "))
					e.recordVerifyHit(context.WithoutCancel(ctx))
					return current, observedOK, attempts
				}
			}
			// 403 通常不是字段路径问题，继续让 LLM 自修正会浪费调用。
			break
		}
		if last.NoJobs {
			if observedOK := e.verifyWithObservedKeywordlessRequest(ctx, exec, draft, current, observed, round); observedOK.OK {
				if issue := recipeQualityIssue(observedOK.FieldQuality); issue != "" {
					observedOK.OK = false
					observedOK.Error = issue
				} else {
					e.recordVerifySuccess(ctx, draft, observedOK, round)
					return current, observedOK, attempts
				}
			}
		}

		slog.Warn("探索配置验证失败，交回模型自修正",
			"site_key", draft.SiteKey,
			"round", round,
			"error", last.Error)

		attempts = append(attempts, ai.RecipeAttempt{
			Candidate:      current,
			Error:          last.Error,
			ResponseSample: last.ResponseSample,
		})

		// 最后一轮不再修正，直接返回失败。
		if round == maxRefineRounds {
			break
		}

		refined, err := e.llm.RefineRecipeCandidate(ctx, observed, p.URL, attempts)
		if err != nil {
			slog.Warn("自修正调用失败，终止修正", "error", err.Error())
			break
		}
		if strings.TrimSpace(refined.ListAPI) == "" {
			slog.Warn("自修正未给出可用 list_api，终止修正")
			break
		}
		// 模型交回完全相同的配置说明它已无新思路，继续重试只是浪费。
		if sameCandidate(current, refined) {
			slog.Warn("自修正返回了与上次完全相同的配置，终止修正")
			break
		}
		current = refined
	}

	return current, last, attempts
}

func isHTTPForbidden(err string) bool {
	return strings.Contains(strings.ToLower(err), "http 403") || strings.Contains(strings.ToLower(err), "forbidden")
}

func verifyWithObservedResponse(
	draft *site.Recipe,
	cand *ai.RecipeCandidate,
	observed []ai.ExploreRequestView,
	detailContents map[string]string,
) executor.VerifyResult {
	jobs, err := jobsFromObservedResponse(draft, cand, observed, detailContents)
	if err != nil || len(jobs) == 0 {
		return executor.VerifyResult{}
	}
	titles := make([]string, 0, 3)
	for i := 0; i < len(jobs) && i < 3; i++ {
		titles = append(titles, jobs[i].Title)
	}
	return executor.VerifyResult{
		OK:           true,
		JobsFound:    len(jobs),
		SampleTitles: titles,
		FieldQuality: executor.BuildFieldQuality(jobs),
		BrowserBound: true,
	}
}

// jobsFromObservedResponse 只消费页面已经收到的响应，不发起任何额外网络请求。
func jobsFromObservedResponse(
	draft *site.Recipe,
	cand *ai.RecipeCandidate,
	observed []ai.ExploreRequestView,
	detailContents map[string]string,
) ([]source.RawJob, error) {
	if draft == nil || cand == nil {
		return nil, fmt.Errorf("探索候选为空")
	}
	for _, r := range observed {
		if !sameObservedEndpoint(cand.ListAPI, r.URL) {
			continue
		}
		body := strings.TrimSpace(r.FullSample)
		if body == "" {
			body = strings.TrimSpace(r.Sample)
		}
		if body == "" {
			continue
		}
		jobs, err := executor.ParseAPIJobs(draft, r.URL, []byte(body))
		if err != nil || len(jobs) == 0 {
			continue
		}
		return mergeDetailContents(jobs, detailContents), nil
	}
	return nil, fmt.Errorf("未找到可解析的岗位列表响应")
}

func mergeDetailContents(jobs []source.RawJob, detailContents map[string]string) []source.RawJob {
	if len(detailContents) == 0 {
		return jobs
	}
	for i := range jobs {
		content := strings.TrimSpace(detailContents[jobs[i].URL])
		if content == "" {
			continue
		}
		jobs[i].Content = content
		jobs[i].Snippet = truncateForLog(content, 500)
	}
	return jobs
}

func sameObservedEndpoint(a, b string) bool {
	au, errA := url.Parse(strings.TrimSpace(a))
	bu, errB := url.Parse(strings.TrimSpace(b))
	if errA == nil && errB == nil && au.Host != "" && bu.Host != "" {
		return strings.EqualFold(au.Host, bu.Host) && au.Path == bu.Path
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// verifyWithObservedKeywordlessRequest 在“用户关键词 0 命中”时复验 Recipe 本身。
//
// 有些站点的岗位接口和解析配置完全正确，但用户给的关键词当前没有岗位。
// 这不应该阻止 recipe 沉淀；否则每个冷门关键词都会被误判成站点探索失败。
// 因此这里只在观测数据明确显示同一个列表接口曾返回非空结果时，
// 用空关键词复放一次原始请求体，验证“路径与字段是否可解析”。
func (e *Explorer) verifyWithObservedKeywordlessRequest(
	ctx context.Context,
	exec *executor.Executor,
	draft *site.Recipe,
	cand *ai.RecipeCandidate,
	observed []ai.ExploreRequestView,
	round int,
) executor.VerifyResult {
	if !hasObservedNonEmptyListResponse(cand, observed) {
		return executor.VerifyResult{}
	}
	res := exec.Verify(ctx, draft, "")
	if !res.OK {
		return res
	}
	slog.Info("探索配置用原始请求验证通过，用户关键词当前无结果",
		"site_key", draft.SiteKey,
		"round", round,
		"jobs", res.JobsFound,
		"sample_titles", strings.Join(res.SampleTitles, " / "))
	e.recordVerifyHit(context.WithoutCancel(ctx))
	return res
}

func hasObservedNonEmptyListResponse(cand *ai.RecipeCandidate, observed []ai.ExploreRequestView) bool {
	if cand == nil {
		return false
	}
	wantURL := strings.TrimSpace(cand.ListAPI)
	if wantURL == "" {
		return false
	}
	for _, r := range observed {
		if strings.TrimSpace(r.URL) != wantURL {
			continue
		}
		text := strings.TrimSpace(r.Sample + "\n" + r.SchemaSummary)
		if text == "" {
			continue
		}
		if strings.Contains(text, strings.TrimSpace(cand.ListPath)+": array[0]") {
			continue
		}
		if strings.Contains(text, strings.TrimSpace(cand.ListPath)+": array[") ||
			strings.Contains(text, strings.TrimSpace(cand.TitleField)) {
			return true
		}
	}
	return false
}

func (e *Explorer) recordVerifySuccess(ctx context.Context, draft *site.Recipe, res executor.VerifyResult, round int) {
	slog.Info("探索配置验证通过",
		"site_key", draft.SiteKey,
		"round", round,
		"jobs", res.JobsFound,
		"sample_titles", strings.Join(res.SampleTitles, " / "))
	// 记录「先跑通再沉淀」这条经验的命中，让它在后续探索中排得更前。
	// 用 WithoutCancel：经验记录不该因主流程结束而丢失。
	e.recordVerifyHit(context.WithoutCancel(ctx))
}

func recipeQualityIssue(fields []executor.FieldQuality) string {
	// 列表发现阶段只验证“是否真的发现了一组岗位”。职责、地点和组织字段
	// 在不同站点的列表页经常不存在，不能把它们当作 Recipe 或岗位入库门槛；
	// 它们只是字段质量诊断与后续详情补全的输入。
	required := map[string]bool{"title": true}
	byField := make(map[string]executor.FieldQuality, len(fields))
	for _, f := range fields {
		byField[f.Field] = f
	}

	missing := make([]string, 0, len(required))
	for name := range required {
		q, ok := byField[name]
		if !ok || !q.OK || q.Hit == 0 {
			total := q.Total
			missing = append(missing, name+" 0/"+strconv.Itoa(total))
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "字段质量不足：" + strings.Join(missing, "；") + "。请回到观测响应，修正 field_map 中的核心字段路径"
}

/**
 * recordVerifyHit 记录「验证-自修正」经验的一次命中。
 *
 * 失败只记日志：经验统计是优化排序的辅助信号，不该影响主流程。
 */
func (e *Explorer) recordVerifyHit(ctx context.Context) {
	if e.repo == nil {
		return
	}
	if err := e.repo.RecordPlaybookHits(ctx, []string{"verify_then_refine"}); err != nil {
		slog.Warn("记录验证经验命中失败", "error", err.Error())
	}
}

/**
 * sameCandidate 判断两份候选在「怎么发请求 + 怎么解析」上是否完全一致。
 *
 * 只比较影响执行结果的字段——notes / confidence 变了不算变化。
 * 这个判断用来提前跳出自修正循环：模型原地打转时不该继续消耗调用。
 */
func sameCandidate(a, b *ai.RecipeCandidate) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ListAPI == b.ListAPI &&
		a.Method == b.Method &&
		a.RequestBody == b.RequestBody &&
		a.RequestContentType == b.RequestContentType &&
		a.ListPath == b.ListPath &&
		a.TitleField == b.TitleField &&
		a.IDField == b.IDField &&
		a.KeywordParam == b.KeywordParam &&
		a.KeywordInBody == b.KeywordInBody
}

/**
 * recipeFromCandidate 把模型候选转成可执行 Recipe（未落库的草稿）。
 *
 * 验证与保存共用同一份转换逻辑，保证「验证时跑的」和「落库后跑的」
 * 是同一个配置——否则验证就失去意义了。
 */
func recipeFromCandidate(p DiscoveryParams, cand *ai.RecipeCandidate) *site.Recipe {
	siteKey, domain := siteKeyAndDomain(p)
	return &site.Recipe{
		SiteKey:            siteKey,
		CompanyName:        defaultCompanyName(p.Company, siteKey),
		Domain:             domain,
		CampusURL:          p.URL,
		StrategyType:       site.StrategyAPI,
		ListAPI:            cand.ListAPI,
		DetailAPI:          cand.DetailAPI,
		DetailURLTemplate:  cand.DetailURLTemplate,
		Method:             cand.Method,
		RequestBody:        cand.RequestBody,
		RequestContentType: cand.RequestContentType,
		RequestHeaders:     jsonMapFromStrings(cand.RequestHeaders),
		KeywordInBody:      cand.KeywordInBody,
		IDField:            cand.IDField,
		TitleField:         cand.TitleField,
		ListPath:           cand.ListPath,
		KeywordParam:       cand.KeywordParam,
		FieldMap:           jsonMapFromStrings(cand.FieldMap),
		Enabled:            true,
		MaxJobsPerSearch:   defaultMaxJobsPerSearch,
		MaxDetailFetches:   0,
		Source:             site.SourceExploration,
		VerifyStatus:       site.VerifyUnverified,
	}
}

// defaultMaxJobsPerSearch 探索产出的 Recipe 默认单次采集条数。
const defaultMaxJobsPerSearch = 20

/**
 * applyVerifyResult 把验证结论写进 Recipe 的健康度字段。
 *
 * 验证通过 → verified + 清零失败计数；
 * 验证失败 → 保持 unverified 并记录原因（调用方通常不会落库，
 * 但降级保存时这些信息是排查依据）。
 */
func applyVerifyResult(rc *site.Recipe, res executor.VerifyResult) {
	if res.OK {
		now := time.Now().UTC()
		rc.VerifyStatus = site.VerifyVerified
		rc.VerifiedAt = &now
		rc.VerifiedJobs = res.JobsFound
		rc.ConsecutiveFailures = 0
		rc.LastError = ""
		return
	}
	rc.VerifyStatus = site.VerifyUnverified
	rc.VerifiedJobs = 0
	rc.LastError = res.Error
}

// 编译期占位：确保 model.JSONMap 在本文件的转换链路上仍被引用。
var _ = model.JSONMap{}
