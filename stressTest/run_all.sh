#!/usr/bin/env bash
# 一键跑全部 k6 脚本，输出汇总到 stressTest/results/
# 服务地址默认 http://localhost:5003/api/v1，可用 BASE_URL 覆盖
set -euo pipefail

cd "$(dirname "$0")"
mkdir -p results

scripts=(
    baseline_ping.js
    product_list.js
    product_show.js
    order_list_optimized.js
    idempotency_replay.js
    seckill_rate_limit.js
    coupon_claim_redis.js
    coupon_claim_db.js
)

for s in "${scripts[@]}"; do
    name="${s%.js}"
    echo "===== $name ====="
    k6 run --summary-export=results/${name}.json "$s" 2>&1 | tee results/${name}.log || true
    echo
done

echo "All done. Summaries: stressTest/results/"
