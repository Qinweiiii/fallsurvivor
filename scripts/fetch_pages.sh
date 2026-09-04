#!/bin/bash
# 抓取多个校招站点的渲染后正文，存为回归样本。
#
# 为什么要存样本：调 prompt / 分块 / 清洗规则时反复开浏览器既慢又贵。
# 页面正文抓一次存成文件，之后所有迭代都只读文件（见 cmd/mdextract）。
#
# 为什么要多站点：只盯一个站点调，改出来的规则必然带该站点的特异性。
# 有了多份样本，任何改动都能立刻看出是否只对某一家有效。
#
# 用法：
#   ./scripts/fetch_pages.sh                 # 抓取全部预置站点
#   ./scripts/fetch_pages.sh kuaishou        # 只抓某一个
#
# 输出：backend/testdata/pages/<站点>.json
set -u

TOKEN=$(grep -E '^BROWSER_WORKER_TOKEN=' .env | cut -d= -f2-)
W="http://127.0.0.1:8390"
OUT_DIR="backend/testdata/pages"
mkdir -p "$OUT_DIR"

hdr=(-H 'Content-Type: application/json' -H "X-Worker-Token: $TOKEN")

# 站点清单：站点key|入口URL|进入列表页可尝试点击的文案(用逗号分隔多个候选,可空)
#
# ★ 入口一律用**根域名**,不写任何猜测的路径后缀。
#
# 教训：早先给深链(如 #/home、/campus/position),结果
#   - 有的直接 404(路径是猜的,站点根本没这个 route)；
#   - 有的触发 SPA 路由守卫弹登录框(深链被当成未授权访问受保护路由),
#     而从首页正常点进去反而无需登录。
# 根域名让站点自己完成重定向,拿到的就是它真正的入口,
# 也更接近真实用户的访问路径。
#
# 点击文案给多个候选按顺序尝试：不同站点入口叫法不同,
# 但这只是抓样本时的导航捷径——真实运行时由 Agent 自行决定如何导航。
SITES=(
  "kuaishou|https://campus.kuaishou.cn|应届招聘,校园招聘,职位列表"
  "bytedance|https://jobs.bytedance.com|校园招聘,校招,职位搜索"
  "meituan|https://zhaopin.meituan.com|校园招聘,校招,职位列表"
  "jd|https://campus.jd.com|校园招聘,职位查询,职位列表,搜索职位"
  "tencent|https://join.qq.com|校园招聘,查看职位,职位搜索"
)

fetch_one() {
  local key="$1" url="$2" clicks="$3"
  local task
  task=$(python3 -c 'import uuid;print(uuid.uuid4())')

  echo "── $key"

  local open_res
  open_res=$(curl -s -m 120 -X POST "$W/session/open" "${hdr[@]}" \
    -d "{\"task_id\":\"$task\",\"site_key\":\"$key\",\"url\":\"$url\"}")
  echo "   会话: $(echo "$open_res" | head -c 150)"
  if ! echo "$open_res" | grep -q '"task_id"'; then
    echo "   跳过：会话打开失败"
    return
  fi

  # 首页可能仍在异步渲染,先等一会儿再尝试导航。
  sleep 6

  # 依次尝试候选入口文案,点中一个就停——
  # 站点叫法不统一,但导航意图是一样的。
  if [ -n "$clicks" ]; then
    IFS=',' read -ra candidates <<< "$clicks"
    for text in "${candidates[@]}"; do
      local act_res
      act_res=$(curl -s -m 60 -X POST "$W/nav/act" "${hdr[@]}" \
        -d "{\"task_id\":\"$task\",\"action\":{\"type\":\"click\",\"text\":\"$text\"}}")
      if echo "$act_res" | grep -q '"ok":true'; then
        echo "   已点击入口: $text"
        break
      fi
    done
  fi

  # SPA 异步渲染需要等待；再滚动一次触发懒加载
  sleep 10
  curl -s -m 30 -X POST "$W/nav/act" "${hdr[@]}" \
    -d "{\"task_id\":\"$task\",\"action\":{\"type\":\"scroll\",\"direction\":\"down\",\"amount\":2500}}" > /dev/null
  sleep 5

  curl -s -m 90 -X POST "$W/page/markdown" "${hdr[@]}" \
    -d "{\"task_id\":\"$task\"}" -o "$OUT_DIR/$key.json"

  python3 - "$OUT_DIR/$key.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception as e:
    print(f'   解析失败: {e}'); sys.exit(0)
md = d.get('markdown') or ''
if not md:
    print(f'   正文为空，错误: {str(d.get("error"))[:120]}')
else:
    print(f'   正文 {len(md)} 字符 | {d.get("current_url","")[:80]}')
PY

  curl -s -m 20 -X POST "$W/session/close" "${hdr[@]}" \
    -d "{\"task_id\":\"$task\"}" > /dev/null
}

only="${1:-}"
for entry in "${SITES[@]}"; do
  IFS='|' read -r key url clicks <<< "$entry"
  if [ -n "$only" ] && [ "$only" != "$key" ]; then
    continue
  fi
  fetch_one "$key" "$url" "$clicks"
done

echo
echo "样本目录: $OUT_DIR"
ls -la "$OUT_DIR" 2>/dev/null | tail -n +2
