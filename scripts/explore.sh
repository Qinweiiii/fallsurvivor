#!/bin/bash
# 临时探索脚本：脱离调用方 shell 生命周期发起一次站点探索。
#
# 存在原因：探索需要 1-3 分钟（浏览器导航 + 多次 LLM 调用），
# 直接在命令行里跑会被工具超时或 shell 退出打断。
set -u

URL="${1:?用法: explore.sh <入口URL> <site_key> <公司名> [关键词]}"
SITE_KEY="${2:?缺少 site_key}"
COMPANY="${3:?缺少公司名}"
KEYWORD="${4:-后端}"
OUT="/tmp/explore_${SITE_KEY}.json"

BODY=$(printf '{"url":"%s","site_key":"%s","company":"%s","keyword":"%s","save":true}' \
  "$URL" "$SITE_KEY" "$COMPANY" "$KEYWORD")

curl -s -m 600 -X POST http://127.0.0.1:9090/api/explore \
  -H 'Content-Type: application/json' \
  -d "$BODY" \
  -o "$OUT"

echo "done exit=$? out=$OUT"
