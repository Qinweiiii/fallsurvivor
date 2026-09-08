"""LangGraph 状态：在节点间传递的探索上下文。"""
from typing import List, Optional, TypedDict

from app.schemas.recipe import RecipeCandidate, TraceStep


class ExploreState(TypedDict, total=False):
    site: str
    base_url: str
    keyword: str
    save: bool
    # 观测：{markdown, url, requests}
    observation: dict
    # 当前决策动作
    decision: dict
    candidate: Optional[RecipeCandidate]
    trace: List[TraceStep]
    step: int
    confidence: int
    playbook: List[str]
    finished: bool
    aborted: bool
    # 失败原因（actor 探索异常时写入），main 层作为响应 reason 返回给前端。
    # 必须在 schema 中声明，否则 LangGraph 按 schema 合并状态时会丢弃未知键，
    # 导致 status=failed 的响应顶层 reason 恒为空。
    abort_reason: str
    last_result: str
    # 观测：browser-use 探索时抓到的真实接口响应体（供 verify 复验，不额外发请求）
    observed: List[dict]
    # 自修正轮数（对齐 Go maxRefineRounds=3）
    verify_rounds: int
    # verify 闭环结论：是否已用「已观测响应」验证通过、以及验证明细。
    # 同样必须在 schema 中声明，否则 critic 写入的值被 LangGraph 丢弃，
    # 导致响应顶层 verified/verify_result 恒为空/None。
    verified: bool
    verify_result: dict
