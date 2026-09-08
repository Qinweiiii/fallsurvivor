package search

import (
	"context"
	"fmt"

	"github.com/eddiel/fallsurvivor/backend/internal/agent"
	"github.com/eddiel/fallsurvivor/backend/internal/executor"
)

// exploreViaPython 把探索交给 Python Agent 微服务（Option B）。
//
// 该服务的探索循环（guardrail→planner→actor→critic→memory）在 production 模式下
// 已自带"真实请求验证"，因此这里直接当作已验证（Verified=true），与 Go 侧
// verifyWithObservedResponse 的语义一致；离线/占位候选则交由下一次真实搜索（Fast Path）复核，
// 与"不落一条跑不通的配置"的约束不冲突（验证失败不落库的逻辑仍在 saveExploredRecipe 的健康度里）。
func (d *DiscoveryService) exploreViaPython(ctx context.Context, p DiscoveryParams, saveAsRecipe bool) (*ExploreSiteResult, error) {
	outcome, err := d.agentClient.Explore(ctx, agent.ExploreRequest{
		SiteKey: p.SiteKey,
		BaseURL: p.URL,
		Keyword: p.Keyword,
	})
	if err != nil {
		return &ExploreSiteResult{Success: false, Reason: err.Error()}, nil
	}

	out := &ExploreSiteResult{
		RunID:        "python-agent",
		Success:      true,
		Candidate:    outcome.Candidate,
		Trace:        mapAgentTrace(outcome.Trace),
		Verified:     outcome.Verified,
		RefineRounds: 0,
	}
	if saveAsRecipe && d.registry != nil {
		// 用 Python 侧真实验证结论（jobs_found / sample_titles）落库，
		// 而非早期硬编码 JobsFound:0。Python Agent 在 production 模式下通过真实浏览器
		// 观测页面 XHR 验证，因此 BrowserBound=true → 落库时默认 browser_observed，
		// 并由 saveExploredRecipe 在落库前探测 deeppath 决定是否升级为 api。
		verify := executor.VerifyResult{
			OK:           outcome.Verified,
			JobsFound:    outcome.JobsFound,
			SampleTitles: outcome.SampleTitles,
			BrowserBound: true,
		}
		rc, serr := saveExploredRecipe(ctx, d.registry, p, outcome.Candidate, verify,
			browserObservedEntryURL(p.URL, out.Trace), browserPlanFromTrace(out.Trace),
			d.executor.ProbeDirectFetch)
		if serr != nil {
			out.Reason = "探索成功但保存失败: " + serr.Error()
			return out, nil
		}
		out.Recipe = rc
		out.Saved = true
		out.VerifiedJobs = outcome.JobsFound
		out.VerifySampleTitles = outcome.SampleTitles
	}
	return out, nil
}

// mapAgentTrace 把 Python 返回的轨迹（含 role）映射为后端统一的 ExploreStepTrace。
// role 信息并入 Reasoning 前缀（如 "[critic] 继续探索"），避免丢失多角色审计线索。
func mapAgentTrace(in []agent.TraceStep) []ExploreStepTrace {
	out := make([]ExploreStepTrace, 0, len(in))
	for _, t := range in {
		out = append(out, ExploreStepTrace{
			Step:       t.Step,
			Action:     t.Action,
			Target:     t.Target,
			Reasoning:  fmt.Sprintf("[%s] %s", t.Role, t.Reasoning),
			Result:     t.Result,
			Confidence: t.Confidence,
		})
	}
	return out
}
