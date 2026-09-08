"""Actor 节点：执行 Planner 决策的单步动作，回传观测/结果。

- 离线 extract：对 markdown 做启发式，产出 schema 合法的占位 RecipeCandidate。
- 生产：调用 controller.run_full_explore（browser-use 全自主探索），返回真实 RecipeCandidate。
对应 Go 侧 explorer_agents.go 的 actor（executeAction）。
"""
from app import config
from app.schemas.recipe import RecipeCandidate, TraceStep
from app.browser.controller import run_full_explore


def _offline_extract(state: dict) -> tuple[RecipeCandidate, str]:
    obs = state.get("observation", {})
    url = obs.get("url", "") or state.get("base_url", "")
    candidate = RecipeCandidate(
        list_api=url,
        method="GET",
        id_field="id",
        title_field="jobName",
        list_path="data.positionList",
        field_map={"title": "jobName", "city": "cityName", "deadline": "publishTime"},
        notes="[offline heuristic] 占位配置；生产由 browser-use + LLM 从真实网络观测抽取",
        confidence=70,
    )
    return candidate, "offline 抽取完成，生成占位 RecipeCandidate"


def production_actor(state: dict) -> tuple:
    try:
        cand, obs = run_full_explore(
            site=state["site"], base_url=state["base_url"], keyword=state.get("keyword", "")
        )
    except Exception as e:  # 探索失败不要盲抛 500，转为 aborted 让响应可诊断
        state["aborted"] = True
        state["abort_reason"] = str(e)[:2000]
        return None, f"探索失败：{e}", {}
    state["observed"] = obs.get("responses", [])
    return cand, "browser-use 探索完成，产出真实 RecipeCandidate", obs


def actor(state: dict) -> dict:
    decision = state.get("decision", {})
    if decision.get("action") == "extract":
        if config.settings.offline:
            cand, result = _offline_extract(state)
            obs = {}
            state["observed"] = []
        else:
            rounds = state.get("verify_rounds", 0)
            if rounds > 0 and state.get("observed"):
                # 自修正轮：浏览器已探索过一次并抓到已观测响应，
                # 不再重开浏览器，沿用 critic 基于已观测响应修正的候选（对齐 Go 观测复验）。
                cand = state.get("candidate")
                obs = {}
                result = f"自修正第 {rounds} 轮：沿用 critic 基于已观测响应修正的候选，不重开浏览器"
            else:
                cand, result, obs = production_actor(state)
        state["candidate"] = cand
        state["confidence"] = cand.confidence if cand is not None else 0
        if obs:
            state["observed"] = obs.get("responses", [])
    else:
        result = f"执行动作 {decision.get('action')}（offline 模拟成功）"
    state["last_result"] = result
    state.setdefault("trace", []).append(
        TraceStep(
            step=state["step"],
            role="actor",
            action=decision.get("action", ""),
            target=str(decision.get("target", "")),
            result=result,
            confidence=int(state.get("confidence", 0)),
        )
    )
    return state
