#!/bin/bash
# 临时诊断脚本：验证「页面已渲染出岗位」这一判断是否成立。
#
# 目的：browser-use 的解法是读渲染后的 DOM，而非逆向接口。
# 在改架构之前，必须先确认快手列表页确实把岗位渲染到了 DOM 里
# ——若渲染不出来，读 DOM 这条路同样走不通。
#
# 注意：Worker 返回的是裸 JSON（无 data 包装），与后端 API 不同。
set -u

TOKEN=$(grep -E '^BROWSER_WORKER_TOKEN=' .env | cut -d= -f2-)
# Worker 要求 task_id 为 UUID 格式。
TASK=$(python3 -c 'import uuid;print(uuid.uuid4())')
W="http://127.0.0.1:8390"

hdr=(-H 'Content-Type: application/json' -H "X-Worker-Token: $TOKEN")

show() {
python3 - "$1" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print('  title:', d.get('title'))
print('  url  :', d.get('current_url'))
cards = d.get('card_samples') or []
print('  岗位卡片样本:', len(cards))
for c in cards[:4]:
    print('    -', (c or '')[:150].replace('\n', ' '))
txt = d.get('text_sample') or ''
print('  正文长度:', len(txt))
print('  正文片段:', txt[:300].replace('\n', ' '))
PY
}

echo "=== 1. 打开会话（快手校招首页）"
curl -s -m 90 -X POST "$W/session/open" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"site_key\":\"kuaishou\",\"url\":\"https://campus.kuaishou.cn/\"}" \
  | head -c 300
echo; echo

echo "=== 2. 首页快照"
curl -s -m 60 -X POST "$W/explore/snapshot" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\"}" -o /tmp/diag_home.json
show /tmp/diag_home.json
echo

echo "=== 3. 点击「应届招聘」"
curl -s -m 60 -X POST "$W/nav/act" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"action\":{\"type\":\"click\",\"text\":\"应届招聘\"}}" | head -c 300
echo
# SPA 路由切换 + 列表异步加载，需要给足渲染时间。
sleep 8

echo "=== 4. 列表页快照（关键：DOM 里有岗位吗？）"
curl -s -m 60 -X POST "$W/explore/snapshot" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\"}" -o /tmp/diag_list.json
show /tmp/diag_list.json
echo

echo "=== 4b. 再等 6 秒后二次快照（判断是否只是渲染慢）"
sleep 6
curl -s -m 60 -X POST "$W/explore/snapshot" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\"}" -o /tmp/diag_list2.json
show /tmp/diag_list2.json
echo

echo "=== 5. 关闭会话"
curl -s -m 20 -X POST "$W/session/close" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\"}" | head -c 120
echo
