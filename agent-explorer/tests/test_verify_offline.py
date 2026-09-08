"""verify 自修正闭环的离线单测（不依赖浏览器 / LLM，秒级）。

验证 P1 核心逻辑：
  1. verify_with_observed 能从「已观测响应」正确解析出岗位（JobsFound>0 + title 可达）。
  2. 错误 list_path 时 verify 返回 ok=False，且 refine_recipe 能基于观测响应修正路径。
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from app.schemas.recipe import RecipeCandidate
from app.graph import verify


def _observed(body: dict, url: str = "https://campus.kuaishou.cn/recruit/api/campus/e/api/job/list") -> list:
    import json
    return [{
        "method": "POST",
        "url": url,
        "status": 200,
        "content_type": "application/json",
        "body": json.dumps(body, ensure_ascii=False),
    }]


def test_verify_pass():
    obs = _observed({
        "code": 0,
        "data": {"positionList": [
            {"id": "1", "jobName": "后端工程师"},
            {"id": "2", "jobName": "算法工程师"},
        ]},
    })
    cand = RecipeCandidate(
        list_api="https://campus.kuaishou.cn/recruit/api/campus/e/api/job/list",
        method="POST", id_field="id", title_field="jobName",
        list_path="data.positionList",
    )
    res = verify.verify_with_observed(cand, obs)
    assert res["ok"] is True, res
    assert res["jobs_found"] == 2
    assert "后端工程师" in res["sample_titles"]


def test_verify_fail_wrong_path():
    obs = _observed({
        "code": 0,
        "data": {"positionList": [{"id": "1", "jobName": "后端工程师"}]},
    })
    # 故意写错 list_path
    cand = RecipeCandidate(
        list_api="https://campus.kuaishou.cn/recruit/api/campus/e/api/job/list",
        method="POST", id_field="id", title_field="jobName",
        list_path="data.wrongPath",
    )
    res = verify.verify_with_observed(cand, obs)
    assert res["ok"] is False
    assert "list_path" in res["error"] or "解析" in res["error"]


def test_refine_fixes_path(monkeypatch):
    obs = _observed({
        "code": 0,
        "data": {"positionList": [{"id": "1", "jobName": "后端工程师"}]},
    })
    bad = RecipeCandidate(
        list_api="https://campus.kuaishou.cn/recruit/api/campus/e/api/job/list",
        method="POST", id_field="id", title_field="jobName",
        list_path="data.wrongPath",
    )

    # 用伪 LLM：直接回一份正确路径的配置，绕过真实 DeepSeek 调用
    class _FakeResp:
        content = '{"list_api":"https://campus.kuaishou.cn/recruit/api/campus/e/api/job/list","method":"POST","id_field":"id","title_field":"jobName","list_path":"data.positionList"}'

    class _FakeLLM:
        def invoke(self, msgs):
            return _FakeResp()

    monkeypatch.setattr("app.llm.get_llm", lambda: _FakeLLM())

    refined = verify.refine_recipe(bad, obs, "list_path 解析失败")
    assert refined is not None
    assert refined.list_path == "data.positionList"
    # 修正后应能通过验证
    res = verify.verify_with_observed(refined, obs)
    assert res["ok"] is True
