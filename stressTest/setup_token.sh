#!/usr/bin/env bash
# 注册一个测试用户、登录、把 token 写回 config.json
# 服务地址默认 http://localhost:5003/api/v1，可用 BASE_URL 覆盖
set -euo pipefail

BASE=${BASE_URL:-http://localhost:5003/api/v1}
USER="k6_$(date +%s)"
PASS="K6test123!"
KEY="123456"

cd "$(dirname "$0")"

curl -sS -X POST "$BASE/user/register" \
  -H 'Content-Type: application/json' \
  -d "{\"user_name\":\"$USER\",\"password\":\"$PASS\",\"nick_name\":\"k6\",\"key\":\"$KEY\"}" \
  > /tmp/k6_register.json
cat /tmp/k6_register.json
echo

LOGIN_BODY=$(curl -sS -X POST "$BASE/user/login" \
  -H 'Content-Type: application/json' \
  -d "{\"user_name\":\"$USER\",\"password\":\"$PASS\"}")
echo "$LOGIN_BODY"
echo

ACCESS=$(echo "$LOGIN_BODY" | jq -r '.data.access_token')
REFRESH=$(echo "$LOGIN_BODY" | jq -r '.data.refresh_token')

if [[ -z "$ACCESS" || "$ACCESS" == "null" ]]; then
    echo "failed to extract tokens from response:"
    echo "$LOGIN_BODY"
    exit 1
fi

# 建券批次、初始化秒杀只对管理员开放：服务端设置了 ADMIN_BOOTSTRAP_TOKEN 且系统里
# 还没有管理员时，用同一个 token 把这个测试用户提升为管理员。
if [[ -n "${ADMIN_BOOTSTRAP_TOKEN:-}" ]]; then
    curl -sS -X POST "$BASE/admin/bootstrap" \
      -H 'Content-Type: application/json' \
      -H "access_token: $ACCESS" -H "refresh_token: $REFRESH" \
      -d "{\"token\":\"$ADMIN_BOOTSTRAP_TOKEN\"}"
    echo
fi

cat > config.json <<EOF
{
  "access_token": "$ACCESS",
  "refresh_token": "$REFRESH",
  "user_name": "$USER"
}
EOF

echo "config.json updated for user $USER"
