#!/bin/bash
# 临时诊断：分层验证序列化脚本，定位「输出为空」到底断在哪一环。
#
# 依次验证：body 是否存在 → innerText 有无内容 → 逐层遍历能否取到文本。
# 不猜，只测。
set -u

TOKEN=$(grep -E '^BROWSER_WORKER_TOKEN=' .env | cut -d= -f2-)
TASK=$(python3 -c 'import uuid;print(uuid.uuid4())')
W="http://127.0.0.1:8390"
hdr=(-H 'Content-Type: application/json' -H "X-Worker-Token: $TOKEN")

curl -s -m 90 -X POST "$W/session/open" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"site_key\":\"kuaishou\",\"url\":\"https://campus.kuaishou.cn/\"}" > /dev/null
curl -s -m 60 -X POST "$W/nav/act" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"action\":{\"type\":\"click\",\"text\":\"应届招聘\"}}" > /dev/null
sleep 8

echo "=== A. 旧 snapshot（已知能取到 705 字符正文）"
curl -s -m 60 -X POST "$W/explore/snapshot" "${hdr[@]}" -d "{\"task_id\":\"$TASK\"}" \
  -o /tmp/dbg_snap.json
python3 -c "
import json;d=json.load(open('/tmp/dbg_snap.json'))
print('  text_sample 长度:', len(d.get('text_sample') or ''))
print('  elements:', len(d.get('elements') or []))
"

echo "=== B. markdown 端点"
curl -s -m 60 -X POST "$W/page/markdown" "${hdr[@]}" -d "{\"task_id\":\"$TASK\"}" \
  -o /tmp/dbg_md.json
head -c 300 /tmp/dbg_md.json; echo

curl -s -m 20 -X POST "$W/session/close" "${hdr[@]}" -d "{\"task_id\":\"$TASK\"}" > /dev/null
echo "done"
