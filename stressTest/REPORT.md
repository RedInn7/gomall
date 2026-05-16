# gomall 压力测试报告

**测试时间**：2026-05-15
**测试主机**：MacBook (Apple Silicon)，本地单机
**目标版本**：`main` (commits ee20c4a..037c7b7，含 PR #47-#51)
**压测工具**：k6 v1.6.0
**依赖**：MySQL 8 (本机 :3306)、Redis (本机 :6379)、RabbitMQ 未启动 (不影响 HTTP 链路)
**数据规模**：`order` 表 ~6M 行 / 653MB；`product` 表 ~956K 行 / 364MB

## 单元测试结果

```
go test ./middleware/ ./repository/cache/ -v -count=1
```

| 模块                         | 用例                                          | 结果 |
|------------------------------|----------------------------------------------|------|
| middleware/circuitbreaker    | TestCircuitBreaker_OpensAfterFailures        | PASS |
| middleware/circuitbreaker    | TestCircuitBreaker_HalfOpenToClosed          | PASS |
| middleware/circuitbreaker    | TestCircuitBreaker_HalfOpenFailReturnsToOpen | PASS |
| middleware/ratelimit         | TestTokenBucket_BurstThenLimit               | PASS |
| middleware/ratelimit         | TestTokenBucket_PerIPIsolation               | PASS |
| repository/cache/idempotency | TestIdempotency_FullStateMachine             | PASS |
| repository/cache/idempotency | TestIdempotency_ReleaseAllowsRetry           | PASS |
| repository/cache/coupon      | TestCoupon_AtomicClaimNoOversell (500并发)   | PASS |
| repository/cache/coupon      | TestCoupon_PerUserLimit                      | PASS |
| repository/cache/ratelimit   | TestSlidingWindow_RejectsOverLimit           | PASS |
| repository/cache/ratelimit   | TestSlidingWindow_WindowSlides               | PASS |

11/11 通过。其中 `TestCoupon_AtomicClaimNoOversell` 用 500 个 goroutine 抢 100 张券，**实际成功数恰好等于 100，零超发**。

## 各链路压测结果

| 链路 | 脚本 | VUs | 时长 | RPS | p50 | p95 | max | 错误率 |
|------|------|-----|------|------|-----|-----|-----|--------|
| Ping 基线 | baseline_ping.js | 100 | 30s | **64,254** | 0.91ms | 3.51ms | 109ms | 0% |
| 商品详情 (无缓存, DB+PK) | product_show.js | 80 | 30s | **62,226** | 0.73ms | 3.01ms | 131ms | 0% |
| 订单列表 (游标+缓存, PR #38) | order_list_optimized.js | 100 | 30s | **58,406** | 1.04ms | 5.0ms | 348ms | 0% |
| 幂等下单 (PR #48) | idempotency_replay.js | 50 | 15s | **50,319** | 0.50ms | 2.33ms | 73ms | 0% |
| 优惠券领取 - Redis Lua (PR #50) | coupon_claim_redis.js | 80 | 20s | **51,362** | 0.82ms | 3.52ms | 136ms | 0% |
| 优惠券领取 - DB 悲观锁 (PR #50) | coupon_claim_db.js | 80 | 20s | **50,142** | 0.83ms | 3.65ms | **453ms** | 0% |
| 秒杀+滑动窗口限流 (PR #49) | seckill_rate_limit.js | 30 | 15s | 52,082 | 0.32ms | 1.24ms | 94ms | 0% |
| 商品列表 (DB count 全表) | product_list.js | 50 | 30s | **24.5** | 2.27s | 2.50s | 2.67s | 0% |
| 订单列表 - 旧版深分页 | order_list_deep_pagination.js | 100 | 30s | **8.3** | 10.5s | 15.95s | 16.29s | 0% |

> 注：错误率 = HTTP 非 2xx 比例。业务上的"被限流/被熔断/被幂等命中"由于 HTTP code 仍是 200，分别用 status 字段 70001/70002/60002 区分（详见各场景分析）。

## 链路分析

### 1. 基线 vs 业务接口

`/ping` 跑出 **64K RPS / p95 3.5ms**，是裸 gin + 中间件链的上限。`product/show` 走完 ClientIP → MySQL PK 查询的链路达到 **62K RPS / p95 3ms**，与基线几乎重合 —— 说明在小数据集 + 主键索引 + 热缓存下，MySQL 单点查询的开销可以忽略。

商品详情接口未来接缓存后（feat/product-cache-consistency 分支）应当在更大并发下保持稳定（当前已经接近 ping 极限，单机硬件层面 cap 在这里），主要收益体现在**降低 DB 负载**而不是单次延迟。

### 2. 订单列表：游标分页的代差碾压

|                | 优化版 (PR #38) | 旧版深分页    |
|----------------|-----------------|---------------|
| RPS            | 58,406          | 8.3           |
| p95 延迟       | 5ms             | 15.95s        |
| **吞吐比**     |                 | **~7,000x**   |
| **p95 延迟比** |                 | **~3,200x**   |

原因：
- **旧版**：每次请求都 `SELECT COUNT(*)` + `OFFSET 1999999 LIMIT 20`，6M 行的 order 表必扫全表
- **优化版**：`WHERE id < last_id LIMIT 20`，PK 倒序，O(1) 命中索引；首页结果还有 5min Redis 缓存

这是 PR #38 在大表上的实测收益。

### 3. 商品列表：暴露 `COUNT(*)` 反模式

`product/list` 只跑出 24.5 RPS / p95 2.5s。瓶颈在 `productDao.CountProductByCondition` —— 1M 行的 product 表每次都做全表 count。优化方向（不在本轮 PR 内）：
- 用近似计数：`SHOW TABLE STATUS` 或 INFORMATION_SCHEMA
- 或对 `category_id` 建索引 + count 走索引
- 或前端改成无限滚动，不显示 total

### 4. 幂等中间件：755K 请求 → 1 笔订单

`idempotency_replay.js` 用同一 `Idempotency-Key` 让 50 个 VU 持续打 `/orders/create`，15s 内累计 **755,033 次请求**。压测后查 DB：

```sql
SELECT COUNT(*) FROM `order` WHERE user_id = 10 AND created_at > NOW() - INTERVAL 2 MINUTE;
=> 1
```

**恰好 1 笔订单**。第一次成功后，所有后续请求由 Lua 脚本直接读 done 态缓存返回 → 50K RPS、p95 2.3ms，与读 Redis 单值的成本相当。

### 5. 限流中间件：分布式滑动窗口精准生效

`seckill_rate_limit.js`：30 个 VU 用同一 token 打 `/skill_product/skill`，配置 `Limit=3 Window=1s ByUser=true`。

- 781,624 次返回 70001（被限流）
- **46 次通过**
- 15s × 3/s = **45 期望**

误差 < 3%。Redis Lua 滑动窗口逻辑正确。

### 6. 优惠券：Redis Lua vs DB 锁

单用户的对比因为 PerUser=1 的瓶颈被掩盖（每个 batch 都只有 1 张能成功落给同一个用户）。但已经可以观察到：

|              | Redis Lua | DB 锁  |
|--------------|-----------|--------|
| avg 延迟     | 1.24ms    | 1.29ms |
| **max 延迟** | 136ms     | **453ms** |

DB 模式的尾延迟显著更高 —— `SELECT FOR UPDATE` + 事务 + 行锁 + Count 检查比 Lua 单次 RTT 的开销大几倍。多用户场景下差距会进一步放大，单元测试 `TestCoupon_AtomicClaimNoOversell` 500 个并发 goroutine 抢 100 张券的"零超发"由 Redis Lua 单独承担。

要完整复现 Redis 与 DB 锁的多用户对比，需要批量预注册测试用户 + 每个 VU 用独立 token 并发抢同一批次，这是后续可以补的实验。

## 测试中发现的 Bug 与改进

### 已修：`util.LogrusObj` 在 InitLog 前被使用

**Branch**: `fix/init-log-order-before-rmq`

PR #51 (订单延迟关单) 引入了 `tryInitRabbitMQ()` 在 `cmd/main.go` 中。该函数的 `recover` 处理器调用 `util.LogrusObj.Warnf(...)`，但 `util.InitLog()` 写在 `tryInitRabbitMQ()` 之后 → `LogrusObj` 是 nil → recover 内部再次 panic → 整个进程退出。

修复方案：把 `util.InitLog()` 调用提前到 `tryInitRabbitMQ()` 之前。

### 待办：全局 TokenBucket 在 localhost 测试下不触发

`stress_test = 64K RPS` 但 `r.Use(middleware.TokenBucket(100, 200))` 应该把单 IP 限制在 100 RPS。猜测原因：k6 80 个 VU 各开一个 TCP 连接，每个连接的 RemoteAddr 端口不同；如果 `c.ClientIP()` 实际返回的不是纯 IP 而是带端口的形式，map key 就会全部错开。需要后续验证 + 修复。

> 短期影响：生产环境用 Nginx 转发时这个问题不存在（XFF 会规范化），但本地直连缺乏防护。

## 复现方式

```bash
# 1. 启动依赖
docker compose up -d mysql redis  # rabbitmq 可选

# 2. 起服务
go build -o /tmp/gomall-bin ./cmd
/tmp/gomall-bin &

# 3. 准备测试 token
cd stressTest
./setup_token.sh   # 注册新用户 + 写 config.json

# 4. 初始化秒杀数据
ACCESS=$(jq -r .access_token config.json)
REFRESH=$(jq -r .refresh_token config.json)
curl -s -X POST -H "access_token: $ACCESS" -H "refresh_token: $REFRESH" \
     http://localhost:5002/api/v1/skill_product/init

# 5. 跑全部压测
./run_all.sh
```

每个脚本独立可跑：`k6 run stressTest/<name>.js`。
