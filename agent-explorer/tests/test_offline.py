"""离线回归：不联网、不调浏览器，对 backend/testdata/pages 的 5 份样本跑探索循环，
断言能产出符合 Go RecipeCandidate 契约的配置，且轨迹覆盖五个角色。
"""
import os

import pytest
from fastapi.testclient import TestClient

from app.main import app

CLIENT = TestClient(app)

# 与 backend/testdata/pages 下的样本文件名对齐
SITES = ["bytedance", "jd", "kuaishou", "meituan", "tencent"]


def test_health_offline():
    r = CLIENT.get("/health")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["offline"] is True


def test_explore_contract_for_each_sample():
    for site in SITES:
        r = CLIENT.post(
            "/explore",
            json={
                "site": site,
                "base_url": f"https://{site}.example.com",
                "keyword": "算法",
                "save": False,
            },
        )
        assert r.status_code == 200, f"{site}: HTTP {r.status_code}"
        body = r.json()
        assert body["status"] == "success", f"{site}: {body}"

        recipe = body["recipe"]
        # 必填字段非空（pydantic 已强制类型，这里再确认业务必填）
        assert recipe["list_api"], f"{site}: list_api 为空"
        assert recipe["method"], f"{site}: method 为空"
        assert recipe["id_field"], f"{site}: id_field 为空"
        assert recipe["title_field"], f"{site}: title_field 为空"
        assert recipe["list_path"], f"{site}: list_path 为空"
        assert isinstance(recipe["confidence"], int)
        assert 0 <= recipe["confidence"] <= 100

        # 轨迹应覆盖五个角色
        roles = {t["role"] for t in body["trace"]}
        assert {"guardrail", "planner", "actor", "critic", "memory"}.issubset(roles), (
            f"{site}: 轨迹缺角色 {roles}"
        )


if __name__ == "__main__":
    # 直接运行也可（无需 pytest）：python tests/test_offline.py
    test_health_offline()
    test_explore_contract_for_each_sample()
    print("OK: 5 个样本均产出合法 RecipeCandidate，轨迹含五个角色")
