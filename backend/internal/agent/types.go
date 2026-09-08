// Package agent 是 Go 后端调用 Python 探索 Agent 微服务（agent-explorer）的客户端。
//
// 它对应 docs/AGENT_PYTHON_DESIGN.md 的 Option B：把「探索未知校招站点」这层从 Go 后端
// 独立成 Python 服务（LangGraph 多角色 + browser-use 控浏览器）。本包只负责 HTTP 调用与
// 结果映射，不引入任何与探索算法相关的逻辑，从而保持后端业务逻辑不动。
//
// 双向校验：调用时携带与浏览器 Worker 相同的 X-Worker-Token（见 server.ts 的 token 校验）。
package agent

import "github.com/eddiel/fallsurvivor/backend/internal/ai"

// ExploreRequest 对应 Python 服务 POST /explore 的请求体。
// 字段名使用 snake_case，与 agent-explorer/app/schemas/recipe.py 的 ExploreRequest 对齐。
type ExploreRequest struct {
	SiteKey string `json:"site"`
	BaseURL string `json:"base_url"`
	Keyword string `json:"keyword"`
	Save    bool   `json:"save"`
}

// TraceStep 是 Python 返回的探索轨迹单步，含角色信息（guardrail/planner/actor/critic/memory）。
type TraceStep struct {
	Step       int    `json:"step"`
	Role       string `json:"role"`
	Action     string `json:"action"`
	Target     string `json:"target"`
	Reasoning  string `json:"reasoning"`
	Result     string `json:"result"`
	Confidence int    `json:"confidence"`
}

// exploreResponse 是 Python 服务 /explore 的响应。
// recipe 直接反序列化为 ai.RecipeCandidate：其 JSON tag（snake_case）与 Python 端 pydantic
// 字段名完全一致，因此逐字段对齐、无需额外映射。
// verified / verify_result 由 Python 侧 critic 计算并回传，用于把 verified_jobs 写成真实数字，
// 而不是像早期那样在 Go 侧硬编码 0（见 agent_bridge.go）。
type exploreResponse struct {
	Status      string              `json:"status"`
	Recipe      *ai.RecipeCandidate `json:"recipe"`
	Trace       []TraceStep         `json:"trace"`
	Reason      string              `json:"reason"`
	Verified    bool                `json:"verified"`
	VerifyResult pyVerifyResult     `json:"verify_result"`
}

// pyVerifyResult 是 Python 侧 critic 回传的验证结论（对应 agent-explorer/app/graph/verify.py）。
// 只取 Go 落库需要的字段，其余（如 error）忽略。
type pyVerifyResult struct {
	OK          bool     `json:"ok"`
	JobsFound   int      `json:"jobs_found"`
	SampleTitles []string `json:"sample_titles"`
	Error       string   `json:"error"`
}

// ExploreOutcome 是 Explore 的完整结果：除候选配置外还带回 Python 侧验证结论
// （jobs_found / sample_titles），供 Go 落库时把 verified_jobs 写成真实数字，
// 而非此前那样硬编码 0、要等首次执行再由 MarkSuccess 回填。
type ExploreOutcome struct {
	Candidate    *ai.RecipeCandidate
	Trace        []TraceStep
	Verified     bool
	JobsFound    int
	SampleTitles []string
}
