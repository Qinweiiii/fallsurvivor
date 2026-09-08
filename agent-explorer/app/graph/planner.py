"""Planner 节点：观测 -> 决策下一步动作。

- 离线：确定性启发式。
- 生产：调用 DeepSeek（OpenAI 兼容）产出动作；输出截断时精简观测重来 = Reflection。
对应 Go 侧 explorer_agents.go 的 planner（decideWithRetry）。
"""
import os

from app import config
from app.schemas.recipe import TraceStep
from app.llm import chat_json

_PROMPT_DIR = os.path.join(os.path.dirname(__file__), "..", "prompts")


def _load(name: str) -> str:
    with open(os.path.join(_PROMPT_DIR, name), encoding="utf-8") as f:
        return f.read()


def _offline_decision(state: dict) -> dict:
    if state.get("candidate"):
        return {"action": "finish", "confidence": state.get("confidence", 0),
                "reasoning": "已有候选配置，结束"}
    if state.get("observation", {}).get("markdown"):
        return {"action": "extract", "confidence": 60,
                "reasoning": "观测到列表页，抽取招聘接口配置"}
    return {"action": "navigate", "target": state.get("base_url", ""),
            "confidence": 30, "reasoning": "打开站点根域名"}


def production_planner(state: dict) -> dict:
    # 生产探索由 browser-use 一次性完成整站导航+搜索+定位接口，
    # 不需要 LangGraph 一步步 drive 导航；因此直接触发 extract。
    # 仅当上一轮已验证通过（finished）才结束；验证失败的自修正候选
    # 会带着 verify_rounds 重新进入 extract，触发 browser-use 重新探索。
    if state.get("finished"):
        return {"action": "finish", "confidence": state.get("confidence", 0),
                "reasoning": "已验证通过，结束"}
    return {"action": "extract", "confidence": 0,
            "reasoning": "触发 browser-use 完整探索（导航/搜索/定位接口由 browser-use 自主完成）"}


def planner(state: dict) -> dict:
    decision = _offline_decision(state) if config.settings.offline else production_planner(state)
    state["decision"] = decision
    state.setdefault("trace", []).append(
        TraceStep(
            step=state["step"],
            role="planner",
            action=decision.get("action", ""),
            target=str(decision.get("target", "")),
            reasoning=decision.get("reasoning", ""),
            confidence=int(decision.get("confidence", 0)),
        )
    )
    return state
