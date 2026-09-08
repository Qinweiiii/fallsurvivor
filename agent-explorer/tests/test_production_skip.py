"""production 路径的结构/契约检查（未安装 browser-use 或缺少 key 时整体跳过）。

本沙箱无法构建 browser-use 依赖，因此不在此环境真跑。请在装有 browser-use 的机器上：
    pip install 'browser-use>=0.1,<0.2' langchain-openai
    export DEEPSEEK_API_KEY=... BROWSER_WORKER_TOKEN=... OFFLINE=false
    PYTHONPATH=. python tests/test_production_skip.py
验证 browser-use 能否真正开浏览器、复用 profile、回传 record_recipe。
"""
import os

import pytest

try:
    import browser_use  # noqa: F401
    HAVE_BROWSER_USE = True
except ImportError:  # 沙箱无此依赖，跳过
    HAVE_BROWSER_USE = False

NEED_KEY = bool(os.getenv("DEEPSEEK_API_KEY"))
ONLINE = os.getenv("OFFLINE", "true").lower() in ("", "0", "false", "no")

pytestmark = pytest.mark.skipif(
    not (HAVE_BROWSER_USE and NEED_KEY and ONLINE),
    reason="需要 browser-use + DEEPSEEK_API_KEY + OFFLINE=false 才跑真机",
)


def test_production_explore_returns_recipe():
    from app.browser.controller import run_full_explore

    cand, obs = run_full_explore(
        site="kuaishou",
        base_url="https://www.kuaishou.com/campus",
        keyword="算法",
    )
    assert cand.list_api
    assert cand.method
    assert cand.id_field
    assert cand.title_field
    assert cand.list_path
    assert obs.get("requests") is not None  # best-effort：有头模式通常能抓到；无头可能为空
