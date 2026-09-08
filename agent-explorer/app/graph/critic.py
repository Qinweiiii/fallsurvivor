"""Critic 节点：进度评估，决定是否继续 / 提前结束 / 放弃。

- 信心 >= 阈值 -> 提前结束（finished）
- 步数 >= 上限 -> 放弃（aborted）
- 否则继续
对应 Go 侧 explorer_agents.go 的 critic（progressHint + 信心阈值）。
"""
from app import config
from app.schemas.recipe import TraceStep


def critic(state: dict) -> dict:
    cand = state.get("candidate")
    if config.settings.offline:
        # 离线：按信心阈值决定提前结束
        if cand and cand.confidence >= config.settings.confidence_threshold:
            state["finished"] = True
            verdict = f"信心 {cand.confidence} >= 阈值 {config.settings.confidence_threshold}，提前结束"
        elif state["step"] >= config.settings.max_steps:
            state["aborted"] = True
            verdict = f"达到最大步数 {config.settings.max_steps}，放弃"
        else:
            verdict = "继续探索"
    else:
        # 生产：browser-use 已完整自主探索，存在候选则做 verify 自修正闭环
        if cand:
            from app.graph.verify import verify_with_observed, refine_recipe, MAX_REFINE_ROUNDS

            res = verify_with_observed(cand, state.get("observed", []))
            state.setdefault("trace", []).append(
                TraceStep(
                    step=state["step"], role="critic", action="verify",
                    target=cand.list_api,
                    result=f"verify ok={res['ok']} jobs={res['jobs_found']} {res.get('error','')}",
                    confidence=int(state.get("confidence", 0)),
                )
            )
            if res["ok"]:
                state["finished"] = True
                state["verified"] = True
                state["verify_result"] = res
                verdict = f"验证通过：解析出 {res['jobs_found']} 个岗位，结束"
            else:
                # 验证失败：有限轮自修正
                rounds = state.get("verify_rounds", 0)
                if rounds >= MAX_REFINE_ROUNDS:
                    state["aborted"] = True
                    verdict = f"验证失败且已达 {MAX_REFINE_ROUNDS} 轮自修正上限：{res['error']}"
                else:
                    refined = refine_recipe(cand, state.get("observed", []), res["error"])
                    if refined is None:
                        state["aborted"] = True
                        verdict = f"验证失败且模型无新思路，终止：{res['error']}"
                    else:
                        state["verify_rounds"] = rounds + 1
                        state["candidate"] = refined  # 下一轮用修正后的候选重探索
                        verdict = f"验证失败，第 {rounds+1} 轮自修正：{res['error']}"
        elif state["step"] >= config.settings.max_steps:
            state["aborted"] = True
            verdict = f"达到最大步数 {config.settings.max_steps}，放弃"
        else:
            verdict = "继续探索"
    state.setdefault("trace", []).append(
        TraceStep(
            step=state["step"],
            role="critic",
            action="",
            result=verdict,
            confidence=int(state.get("confidence", 0)),
        )
    )
    return state
