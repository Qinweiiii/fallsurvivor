package search

import (
	"context"
	"strconv"
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/browser"
)

// ─────────────────────────────────────────────────────────────────────────────
// 探索 Agent 的多角色协作拓扑（显式化）
//
// 原先的探索循环是过程式写法，能力都在但没命名。这里把每个角色抽成独立
// 结构并约束职责，方便阅读、单测与对外说明 agent 架构。它们的实现全部
// 委托回 Explorer 已有的方法，不引入新的运行时行为。
//
//   Planner  ── 观测 → 决策下一步动作；输出截断时精简观测并重试（Reflection）。
//   Actor    ── 执行 Planner 决策的单步动作（点击 / 输入 / 搜索 / inspect …）。
//   Critic   ── 评估是否值得继续：停滞检测、信心阈值、关键词探针建议。
//   Guardrail─ 拦截高风险 / 无意义决策（abort / finish、重复 inspect、越界导航）。
//   Memory   ── 长期记忆：Recall 读取 Playbook 经验，Learn 沉淀本次命中。
//
// 这与 LangGraph 的 "Planner / Actor / Critic / Guardrail + Memory" 范式对齐，
// 但实现仍留在 Go 内——无需换语言即可具备完整的 agent 协作语义。
// ─────────────────────────────────────────────────────────────────────────────

// planner 负责"观测→决策"，并内置输出截断时的 Reflection 重试。
type planner struct{ e *Explorer }

// Decide 让模型基于观测决策下一步动作；若输出被截断则精简观测重试一次。
func (p planner) Decide(ctx context.Context, obs ai.ExploreObservation) (*ai.ExploreDecision, error) {
	return p.e.decideWithRetry(ctx, obs)
}

// actor 负责"执行单步动作"。
type actor struct{ e *Explorer }

// Act 执行单个决策，返回执行结果与是否成功。
func (a actor) Act(ctx context.Context, taskID string, d *ai.ExploreDecision, keyword string) exploreActionResult {
	return a.e.executeAction(ctx, taskID, d, keyword)
}

// critic 负责"进度评估"，决定是否应继续以及如何调整策略。
type critic struct{ e *Explorer }

// ProgressHint 基于停滞步数与上一步结果给出推进提示（停滞 / 动作失败等）。
func (c critic) ProgressHint(stagnantSteps int, lastResult string) string {
	return progressHint(stagnantSteps, lastResult)
}

// guardrail 负责"执行前拦截"，是唯一会改写或拒绝决策的角色。
type guardrail struct{ e *Explorer }

// Check 在执行前对决策做安全与有效性审查。返回：
//   - proceed:  是否允许按（可能的改写后）决策继续执行；
//   - override: 若需改写决策（如重复 inspect 改查下一候选 / 改搜索），返回新决策；
//   - reason:   拦截或改写的原因，用于轨迹记录。
//
// 安全边界（与既有约束一致，不新增任何越权能力）：
//   - navigate 仅允许 http/https（由 Worker 侧强制，这里只做语义提示）；
//   - 重复 inspect 同一请求序号 → 拒绝并要求换动作，防死循环；
//   - 尚未验证关键词搜索且页面有搜索框时，优先改写决策去试搜索路径。
func (g guardrail) Check(
	step int,
	d *ai.ExploreDecision,
	reqKeyword string,
	keywordAttempted bool,
	obs ai.ExploreObservation,
	candidates []browser.RankedNetworkRecord,
	inspected map[int]bool,
	allCandidates []ai.ExploreRequestView,
) (proceed bool, override *ai.ExploreDecision, reason string) {
	// 关键词探针：优先验证"搜索路径"能否拿到携带关键词的列表接口。
	if shouldProbeKeyword(reqKeyword, obs, allCandidates, keywordAttempted) &&
		shouldUseKeywordProbe(d, g.e.confidenceThreshold) {
		return true, &ai.ExploreDecision{
			Action:     string(ai.ExploreSearch),
			TargetRef:  searchInputRef(obs.Elements, ""),
			Target:     strings.TrimSpace(reqKeyword),
			Confidence: 45,
			Reasoning:  "先验证关键词搜索路径",
		}, ""
	}

	// 模型自身的搜索决策也要用当前页面语义补齐搜索框 ref。
	if d.Action == string(ai.ExploreSearch) {
		d.TargetRef = searchInputRef(obs.Elements, d.TargetRef)
	}

	// 重复 inspect：已读过的请求序号不再重复，避免死循环。
	if d.Action == string(ai.ExploreInspect) {
		if seq := inspectSeq(d.Target, d.TargetRef); seq > 0 && inspected[seq] {
			if next := firstUninspectedRequest(toRequestViews(candidates), inspected); next > 0 {
				return true, &ai.ExploreDecision{
					Action:    string(ai.ExploreInspect),
					Target:    strconv.Itoa(next),
					Reasoning: "避免重复 inspect，改查下一个候选请求",
				}, "改写：跳到未检查的候选请求"
			}
			if shouldProbeKeyword(reqKeyword, obs, allCandidates, keywordAttempted) {
				return true, &ai.ExploreDecision{
					Action:     string(ai.ExploreSearch),
					TargetRef:  searchInputRef(obs.Elements, ""),
					Target:     strings.TrimSpace(reqKeyword),
					Confidence: 45,
					Reasoning:  "候选请求已检查过，改用关键词搜索触发新请求",
				}, "改写：改关键词搜索"
			}
			return false, &ai.ExploreDecision{
				Action:    string(ai.ExploreInspect),
				Target:    strconv.Itoa(seq),
				Reasoning: "重复 inspect 已拒绝，请根据历史选择新的页面动作",
			}, "拦截：重复 inspect 同一请求"
		}
	}
	return true, nil, ""
}

// memory 负责"长期记忆"：Playbook 经验的召回与沉淀。
type memory struct{ e *Explorer }

// Recall 读取长期记忆（Playbook 经验），注入到每一步观测中。
func (m memory) Recall(ctx context.Context) []ai.ExploreTacticView {
	return m.e.loadPlaybook(ctx)
}

// Learn 把本次探索命中的经验沉淀回长期记忆。
func (m memory) Learn(ctx context.Context, cand *ai.RecipeCandidate, reqs []ai.ExploreRequestView) {
	m.e.recordPlaybookHits(ctx, cand, reqs)
}
