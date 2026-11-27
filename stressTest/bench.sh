#!/usr/bin/env bash
# 一键压测（make bench）：
#   1. MySQL / Redis 没在跑就用 docker compose 拉起来；
#   2. 服务没在跑就编译并启动，带 DISABLE_GLOBAL_RATE_LIMIT=true，
#      否则所有 VU 共用 127.0.0.1 一个令牌桶，测到的只是 70001 拒绝路径；
#   3. 注册一个压测账号，token 写进 stressTest/config.json；
#   4. 依次跑 k6 脚本，最后把 QPS、P50/P95/P99、错误率汇总成一张表。
#
# 可调环境变量：
#   BENCH_SCRIPTS   要跑的脚本名（不带 .js，空格分隔），默认是下面四条读链路
#   BENCH_DURATION  覆盖每个脚本的时长，如 10s；不设就用脚本里写的时长
#   BASE_URL        服务地址，默认 http://localhost:5003/api/v1
set -euo pipefail

cd "$(dirname "$0")/.."
export BASE_URL=${BASE_URL:-http://localhost:5003/api/v1}
# 默认只跑不需要额外准备数据的读链路；下单、领券、秒杀要地址 / 管理员权限，按 REPORT.md 单独跑
SCRIPTS=${BENCH_SCRIPTS:-"baseline_ping product_show product_list order_list_optimized"}
OUT=stressTest/results
mkdir -p "$OUT"

# ---- 1. 依赖 ----
if ! nc -z localhost 3306 2>/dev/null || ! nc -z localhost 6379 2>/dev/null; then
    echo ">> MySQL / Redis 未就绪，用 docker compose 拉起"
    docker compose up -d --wait mysql redis
fi

# ---- 2. 服务 ----
if curl -sf "$BASE_URL/ping" >/dev/null; then
    # 复用别人起的服务时无法确认它关了全局限流；被限流的请求会体现在“检查失败率”上
    echo ">> 复用已在运行的服务（需自行确认启动时带了 DISABLE_GLOBAL_RATE_LIMIT=true）"
else
    echo ">> 编译并启动服务，日志在 $OUT/server.log"
    go build -o ./main ./cmd
    DISABLE_GLOBAL_RATE_LIMIT=true SNOWFLAKE_ALLOW_DEFAULT=true ./main >"$OUT/server.log" 2>&1 &
    SERVER_PID=$!
    # 只停自己启动的服务，复用的服务不动
    trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT
    for _ in $(seq 60); do
        curl -sf "$BASE_URL/ping" >/dev/null && break
        sleep 1
    done
    curl -sf "$BASE_URL/ping" >/dev/null || { echo "服务 60s 内没有起来，看 $OUT/server.log"; exit 1; }
fi

# ---- 3. 压测账号 ----
stressTest/setup_token.sh >"$OUT/setup_token.log" 2>&1 || { echo "注册压测账号失败，看 $OUT/setup_token.log"; exit 1; }

# ---- 4. 压测 ----
for s in $SCRIPTS; do
    echo ">> $s"
    rm -f "$OUT/$s.json"   # 防止脚本失败时汇总读到上一次的结果
    # 阈值没达标时 k6 返回非零，但结果照常导出，所以不中断后面的脚本
    k6 run --quiet ${BENCH_DURATION:+--duration "$BENCH_DURATION"} \
        --summary-trend-stats 'avg,med,p(95),p(99),max' \
        --summary-export "$OUT/$s.json" "stressTest/$s.js" >"$OUT/$s.log" 2>&1 \
        || echo "   阈值未达标或脚本报错，详见 $OUT/$s.log"
done

# ---- 5. 汇总 ----
# HTTP 错误率：非 2xx/3xx 或连接失败的请求占比；
# 检查失败率：脚本里 check 不通过的占比，会把 HTTP 200 但业务码 70001（被限流）也算进去。
{
    echo "| 脚本 | 请求数 | QPS | P50 | P95 | P99 | HTTP 错误率 | 检查失败率 |"
    echo "|------|------:|----:|----:|----:|----:|-----------:|-----------:|"
    for s in $SCRIPTS; do
        if [[ ! -f "$OUT/$s.json" ]]; then
            echo "| $s | 未产出结果，见 $OUT/$s.log | | | | | | |"
            continue
        fi
        jq -r --arg n "$s" '
            def ms: (. * 100 | round / 100 | tostring) + "ms";
            def pct: (. * 10000 | round / 100 | tostring) + "%";
            .metrics as $m
            | ($m.checks // {passes: 0, fails: 0}) as $c
            | "| \($n) | \($m.http_reqs.count) | \($m.http_reqs.rate | floor) | \($m.http_req_duration.med | ms) | \($m.http_req_duration["p(95)"] | ms) | \($m.http_req_duration["p(99)"] | ms) | \($m.http_req_failed.value | pct) | \(if ($c.passes + $c.fails) > 0 then ($c.fails / ($c.passes + $c.fails) | pct) else "-" end) |"
        ' "$OUT/$s.json"
    done
} | tee "$OUT/summary.md"
echo
echo "汇总已写入 $OUT/summary.md，每个脚本的完整输出在 $OUT/<脚本>.log"
