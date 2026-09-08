#!/usr/bin/env bash
# 同一站点：Go 探索 vs Python 探索，产出 Recipe 是否一致（真机对比）
#
# 前置（需手动起好，本脚本不负责启动服务）：
#   make server                       # Go 后端 :9090
#   make bw                           # Playwright Worker :8390（Go 探索需要它）
#   cd agent-explorer && uvicorn app.main:app --port 8400   # Python :8400
# 且 .env 中：
#   USE_PYTHON_AGENT=false            # 让 Go 侧 /api/explore 走 Go 内置 Explorer
#   BROWSER_WORKER_TOKEN=...          # 与 Python 服务一致（双向校验）
#
# 用法：
#   ./scripts/compare_recipes.sh <站点根URL> <关键词> [site_key]
#
# 说明：HTTP 入参命名两边不同（Go: url/site_key；Python: site/base_url），
#       但内部 RecipeCandidate 字段完全一致（snake_case），故可对拍。
set -euo pipefail

URL="${1:?用法: compare_recipes.sh <站点根URL> <关键词> [site_key]}"
KW="${2:-算法}"
KEY="${3:-$(basename "$URL")}"

GO=http://127.0.0.1:9090/api/explore
PY=http://127.0.0.1:8400/explore
TOKEN="${BROWSER_WORKER_TOKEN:-CHANGE_ME}"

echo "==> Go  (/api/explore)"
curl -s -X POST "$GO" -H 'content-type: application/json' \
  -d "{\"url\":\"$URL\",\"site_key\":\"$KEY\",\"keyword\":\"$KW\",\"save\":false}" -o /tmp/go_explore.json
echo "==> Python (/explore)"
curl -s -X POST "$PY" -H 'content-type: application/json' -H "X-Worker-Token: $TOKEN" \
  -d "{\"site\":\"$KEY\",\"base_url\":\"$URL\",\"keyword\":\"$KW\",\"save\":false}" -o /tmp/py_explore.json

echo
echo "== 原始响应（Go）=="
python3 -m json.tool /tmp/go_explore.json
echo
echo "== 原始响应（Python）=="
python3 -m json.tool /tmp/py_explore.json

echo
echo "== 提取 recipe 部分做归一化对比 =="
python3 - <<'PY'
import json
go = json.load(open('/tmp/go_explore.json'))
py = json.load(open('/tmp/py_explore.json'))
g_recipe = go.get('candidate') or go.get('recipe') or go
p_recipe = py.get('recipe') or py
gs = json.dumps(g_recipe, sort_keys=True, ensure_ascii=False, indent=2)
ps = json.dumps(p_recipe, sort_keys=True, ensure_ascii=False, indent=2)
if gs == ps:
    print("MATCH ✅ Go 与 Python 产出完全一致")
else:
    print("DIFF ❌ 下方为差异（Go 在前，Python 在后）：")
    import subprocess
    open('/tmp/g.json','w').write(gs); open('/tmp/p.json','w').write(ps)
    subprocess.run(['diff','/tmp/g.json','/tmp/p.json'])
PY
