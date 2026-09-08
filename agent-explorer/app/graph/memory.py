"""Memory 节点：长期记忆（Playbook 经验）的召回与沉淀。

- recall：返回历史命中经验，注入到每一步观测（离线返回空）。
- learn：把本次命中经验沉淀回长期记忆（离线为 no-op；生产用向量库）。
对应 Go 侧 explorer_agents.go 的 memory（loadPlaybook / recordPlaybookHits +
exploration_playbook 表）。
"""
from app import config
from app.schemas.recipe import TraceStep


def recall() -> list:
    if config.settings.offline:
        return []  # 离线无长期记忆
    # TODO(production): 从向量库召回 Playbook 经验
    return []


def learn(state: dict) -> dict:
    cand = state.get("candidate")
    step = state.get("step", 0)
    if cand and not config.settings.offline:
        # TODO(production): upsert 到向量库
        pass
    state.setdefault("trace", []).append(
        TraceStep(
            step=step,
            role="memory",
            action="",
            result="沉淀本次命中经验" if cand else "无候选，跳过沉淀",
            confidence=int(state.get("confidence", 0)),
        )
    )
    return state
