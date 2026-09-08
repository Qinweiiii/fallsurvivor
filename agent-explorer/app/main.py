"""FastAPI 入口：组装 LangGraph 五角色循环，暴露 /health 与 /explore。"""
import json
import os

from fastapi import FastAPI
from langgraph.graph import END, StateGraph

from app import config
from app.graph import actor, critic, guardrail, memory, planner
from app.graph.state import ExploreState
from app.schemas.recipe import ExploreRequest, ExploreResponse

app = FastAPI(title="fallsurvivor agent-explorer")


def build_graph():
    g = StateGraph(ExploreState)
    g.add_node("memory_recall", lambda s: {**s, "playbook": memory.recall()})
    g.add_node("guardrail", guardrail.guardrail)
    g.add_node("planner", planner.planner)
    g.add_node("actor", actor.actor)
    g.add_node("critic", critic.critic)
    g.add_node("memory_learn", memory.learn)
    g.set_entry_point("memory_recall")
    g.add_edge("memory_recall", "guardrail")
    g.add_edge("guardrail", "planner")
    g.add_edge("planner", "actor")
    g.add_edge("actor", "critic")
    g.add_edge("critic", "memory_learn")
    g.add_conditional_edges(
        "memory_learn",
        lambda s: END if (s.get("finished") or s.get("aborted")) else "memory_recall",
    )
    return g.compile()


GRAPH = build_graph()


def load_observation(req: ExploreRequest) -> dict:
    """离线：按 site 名读取 backend/testdata/pages/{site}.json 作为观测；
    生产：由 Actor 经 browser-use 捕获。"""
    if config.settings.offline:
        path = os.path.join(
            os.path.dirname(__file__), "..", "..", "backend", "testdata", "pages",
            f"{req.site}.json",
        )
        if os.path.exists(path):
            with open(path, encoding="utf-8") as f:
                d = json.load(f)
            return {"markdown": d.get("markdown", ""), "url": d.get("current_url", "")}
        return {"markdown": "", "url": req.base_url}
    return {"markdown": "", "url": req.base_url}


@app.get("/health")
def health():
    return {"status": "ok", "offline": config.settings.offline}


@app.post("/explore", response_model=ExploreResponse)
def explore(req: ExploreRequest) -> ExploreResponse:
    observation = load_observation(req)
    init: ExploreState = {
        "site": req.site,
        "base_url": req.base_url,
        "keyword": req.keyword,
        "save": req.save,
        "observation": observation,
        "trace": [],
        "step": 0,
        "confidence": 0,
        "playbook": [],
        "finished": False,
        "aborted": False,
        "last_result": "",
    }
    final = GRAPH.invoke(init, config={"recursion_limit": 30})
    cand = final.get("candidate")
    if final.get("aborted") or not cand:
        reason = final.get("abort_reason") or "探索未产出有效配置"
        return ExploreResponse(
            status="failed", trace=final.get("trace", []), reason=reason
        )
    return ExploreResponse(
        status="success",
        recipe=cand,
        trace=final.get("trace", []),
        verified=bool(final.get("verified", False)),
        verify_result=final.get("verify_result"),
        refine_rounds=final.get("verify_rounds", 0),
    )
