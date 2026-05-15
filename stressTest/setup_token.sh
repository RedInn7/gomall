#!/usr/bin/env bash
# 注册一个测试用户、登录、把 token 写回 config.json
# 服务必须在 :5002 监听
set -euo pipefail

BASE=http://localhost:5002/api/v1
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

cat > config.json <<EOF
{
  "access_token": "$ACCESS",
  "refresh_token": "$REFRESH",
  "user_name": "$USER"
}
EOF

echo "config.json updated for user $USER"
