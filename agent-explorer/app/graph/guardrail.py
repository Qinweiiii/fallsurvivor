"""Guardrail 节点：执行前拦截 / 改写高风险与无意义决策。

当前（离线骨架）只做日志与放行；生产实现需补齐：
- 重复 inspect 同一请求序号 -> 拒绝并要求换动作（防死循环）；
- 候选已查完且有搜索框 -> 改写为关键词搜索，触发新请求；
- 导航动作仅允许 http/https（由浏览器侧强制）。
对应 Go 侧 explorer_agents.go 的 guardrail.Check。
"""
from app.schemas.recipe import TraceStep


def guardrail(state: dict) -> dict:
    step = state.get("step", 0) + 1
    state["step"] = step
    decision = state.get("decision") or {}
    reason = "allow"
    state.setdefault("trace", []).append(
        TraceStep(
            step=step,
            role="guardrail",
            action=decision.get("action", ""),
            target=str(decision.get("target", "")),
            reasoning="执行前拦截检查",
            result=reason,
            confidence=int(decision.get("confidence", 0)),
        )
    )
    return state
