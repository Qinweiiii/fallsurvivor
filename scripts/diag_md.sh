#!/bin/bash
# 临时诊断脚本：验证 DOM→Markdown 序列化在快手列表页的产出质量。
#
# 判断标准：Markdown 里必须能看出「一条岗位」的完整信息
# （标题 / 类型 / 城市 / 日期），且岗位数量接近页面显示的总数。
# 若只能看到筛选器面板，说明序列化仍然没抓到主体内容。
set -u

TOKEN=$(grep -E '^BROWSER_WORKER_TOKEN=' .env | cut -d= -f2-)
TASK=$(python3 -c 'import uuid;print(uuid.uuid4())')
W="http://127.0.0.1:8390"
hdr=(-H 'Content-Type: application/json' -H "X-Worker-Token: $TOKEN")

echo "=== 1. 打开会话"
curl -s -m 90 -X POST "$W/session/open" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"site_key\":\"kuaishou\",\"url\":\"https://campus.kuaishou.cn/\"}" \
  | head -c 200
echo

echo "=== 2. 点击「应届招聘」"
curl -s -m 60 -X POST "$W/nav/act" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"action\":{\"type\":\"click\",\"text\":\"应届招聘\"}}" | head -c 200
echo
# 岗位列表是异步加载的，需要给足渲染时间；再滚动触发懒加载。
sleep 10
curl -s -m 30 -X POST "$W/nav/act" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\",\"action\":{\"type\":\"scroll\",\"direction\":\"down\",\"amount\":2000}}" > /dev/null
sleep 4

echo "=== 3. 取 Markdown"
curl -s -m 60 -X POST "$W/page/markdown" "${hdr[@]}" \
  -d "{\"task_id\":\"$TASK\"}" -o /tmp/ks_md.json

python3 - <<'PY'
import json, re
d = json.load(open('/tmp/ks_md.json'))
md = d.get('markdown') or ''
print('url      :', d.get('current_url'))
print('truncated:', d.get('truncated'))
print('长度     :', len(md))
print('行数     :', md.count('\n') + 1)
print('「查看职位」次数:', md.count('查看职位'))
print('日期次数 :', len(re.findall(r'20\d{2}-\d{2}-\d{2}', md)))

# 抽出含日期的行——岗位条目通常带发布日期，用它定位岗位区
lines = md.split('\n')
job_lines = [i for i, l in enumerate(lines) if re.search(r'20\d{2}-\d{2}-\d{2}', l)]
print('\n--- 含日期的行及其上下文（前 3 组，各向上 14 行）---')
for i in job_lines[:3]:
    lo = max(0, i - 14)
    print('  === 组 ===')
    for l in lines[lo:i+1]:
        print('   ', repr(l[:110]))
PY

echo "=== 4. 关闭"
curl -s -m 20 -X POST "$W/session/close" "${hdr[@]}" -d "{\"task_id\":\"$TASK\"}" | head -c 80
echo
