"""导出离线 /explore 响应为 fixture（JSONL，每行一条），供 Go 侧契约测试消费。

不联网、不调浏览器、不依赖生产 LLM：复用 main.py 的离线图。
用法（agent-explorer 的 venv 中）：
    PYTHONPATH=. python tests/dump_fixtures.py
产出 agent-explorer/fixtures/explore.jsonl
"""
import json
import os

from fastapi.testclient import TestClient

from app.main import app

CLIENT = TestClient(app)
SITES = ["bytedance", "jd", "kuaishou", "meituan", "tencent"]


def main() -> None:
    here = os.path.dirname(__file__)
    out_dir = os.path.join(here, "fixtures")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, "explore.jsonl")
    n = 0
    with open(out_path, "w", encoding="utf-8") as f:
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
            f.write(r.text + "\n")
            n += 1
    print(f"wrote {n} lines -> {out_path}")


if __name__ == "__main__":
    main()
