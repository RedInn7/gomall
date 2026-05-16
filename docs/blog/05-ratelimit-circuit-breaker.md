# 限流 + 熔断：50 行代码挡住 99.99% 的滥用请求

## 为什么单机扛得住但还是要限流

工程师常有一个误区："我的服务能跑 5 万 RPS，所以不需要限流。" 错。

限流的目的从来不是"防止服务挂"，而是：

1. **隔离故障**：一个用户/接口出问题，不能拖垮整个系统
2. **保护下游**：你的 5 万 RPS，下游可能只能扛 1 万
3. **资源公平**：免费 / 付费用户的 QPS 不能给同一份蛋糕
4. **防爬虫/防滥用**：脚本一秒打 1000 次的行为必须挡住

熔断器是限流的孪生兄弟：限流防"前门进来太多"，熔断防"后门返回失败连锁"。

## 三种限流算法

### 1. 计数器（最简单，不推荐）

```
1 秒内请求数 >= 阈值 → 拒绝
```

致命问题：临界点突刺。如果阈值 = 100：
- 第 0.99 秒：放进来 100 个请求
- 第 1.01 秒：又放进来 100 个

实际**两秒内放进来 200 个**，瞬时压力 200/秒。完全没起到限流作用。

### 2. 令牌桶（Token Bucket）

```
桶容量：burst
按 rate 每秒往桶里加令牌
每个请求消耗 1 个令牌
桶空了 → 拒绝
```

**特点**：
- 平均速率受 rate 控制
- 允许短时突发（burst 大小）
- 对"先短暂高峰，后长期低速"的场景友好

### 3. 滑动窗口（Sliding Window）

```
维护一个 [now - window, now] 的请求时间戳列表
窗口内请求数 >= 阈值 → 拒绝
否则加入当前时间戳
```

**特点**：
- 精确控制"任意 1 秒内请求数 ≤ N"
- 不允许突发
- 实现稍复杂（需要清理过期时间戳）

我们项目两种都实现了：单机 Token Bucket + 分布式 Sliding Window。

## 单机令牌桶

`middleware/ratelimit.go`：

```go
func TokenBucket(rps rate.Limit, burst int) gin.HandlerFunc {
    var mu sync.Mutex
    limiters := make(map[string]*rate.Limiter)

    get := func(ip string) *rate.Limiter {
        mu.Lock()
        defer mu.Unlock()
        l, ok := limiters[ip]
        if !ok {
            l = rate.NewLimiter(rps, burst)
            limiters[ip] = l
        }
        return l
    }

    return func(c *gin.Context) {
        if !get(c.ClientIP()).Allow() {
            c.JSON(200, gin.H{"status": 70001, "msg": "请求过于频繁"})
            c.Abort()
            return
        }
        c.Next()
    }
}
```

`golang.org/x/time/rate` 标准库实现了完整的令牌桶，我们只是按 IP 创建独立 limiter。

**全局接入**：

```go
r.Use(middleware.TokenBucket(rate.Limit(100), 200))
```

每 IP 100 RPS、突发 200。爬虫和脚本一打就限。

## 分布式滑动窗口（Redis Lua）

单机限流的问题：**多实例部署时各算各的**。3 个实例都设 100/秒，实际放行 300。

解决：把状态放进 Redis，让所有实例共享。

`repository/cache/ratelimit.go` 的 Lua 脚本：

```lua
local key       = KEYS[1]
local window_ms = tonumber(ARGV[1])
local limit     = tonumber(ARGV[2])
local now_ms    = tonumber(ARGV[3])
local member    = ARGV[4]

-- 清理窗口外的旧时间戳
redis.call('ZREMRANGEBYSCORE', key, 0, now_ms - window_ms)
-- 数当前窗口内的请求数
local count = redis.call('ZCARD', key)
if count >= limit then
    return {0, count}        -- 超限
end
-- 加入当前请求
redis.call('ZADD', key, now_ms, member)
redis.call('PEXPIRE', key, window_ms)
return {1, count + 1}
```

**用 ZSet 维护时间戳**：member 是请求 ID（防同毫秒冲突），score 是时间戳。每次请求：
1. 删窗口外的旧记录
2. 数窗口内还剩多少
3. 如果没超限，把当前时间戳加进去

ZSet 的操作复杂度都是 O(log N)，N 是窗口内请求数。对 1 秒窗口 + 1000 RPS 来说，N ≈ 1000，操作 < 10μs。

**用法**（接到秒杀接口）：

```go
authed.POST("skill_product/skill",
    middleware.SlidingWindow(middleware.SlidingWindowOption{
        Scope:  "seckill",
        Window: time.Second,
        Limit:  3,
        ByUser: true,
    }),
    api.SkillProductHandler(),
)
```

单用户 1 秒内最多 3 次秒杀请求。

## 实测：滑动窗口的精度

`stressTest/seckill_rate_limit.js`：30 个 VU 用同一个 token 持续打 15 秒，配置 `Limit=3 Window=1s ByUser=true`。

```
通过：    46 次
被限流：  781,624 次
总请求：  781,670
精度：    15 秒 × 3/秒 = 45 期望，实际 46
```

**误差 2%**。滑动窗口的实现是经得起检验的。

## 熔断：三态机

限流防"上游过来太多"，熔断防"下游挂了别拖累自己"。

经典场景：支付服务调用第三方网关。网关挂了，第三方返回 500 平均要等 30 秒超时。如果你的支付服务没有熔断：
- 第 1 秒：100 个支付请求都在等 30 秒
- 第 2 秒：再 100 个
- ...
- 第 30 秒：3000 个 goroutine 挂在那等同一个挂掉的接口
- 你的进程：内存爆炸、连接耗尽

熔断器在调用方层面识别"下游一直失败"，**主动拒绝后续请求**，给下游和自己都喘息时间。

`middleware/circuitbreaker.go` 实现了三态机：

```
       failure count
[Closed] ─── reach threshold ──▶ [Open]
   ▲                                │
   │                          wait OpenTimeout
   │                                │
   │                                ▼
   │                          [HalfOpen]
   │                                │
   └── all probes succeed ──────────┤
                                    │
   ◀──── any probe fails ───────────┘
        (back to Open)
```

**状态语义**：
- **Closed**: 正常放行。每次失败计数 +1，达到阈值进入 Open
- **Open**: 直接拒绝所有请求。等待 OpenTimeout（如 10s）后进入 HalfOpen
- **HalfOpen**: 只放过 `HalfOpenMaxReq` 个探测请求。全部成功 → 回到 Closed；任一失败 → 回到 Open

**核心代码**：

```go
func (cb *circuitBreaker) allow() error {
    switch circuitState(cb.state.Load()) {
    case stateClosed:
        return nil
    case stateOpen:
        if time.Now().UnixNano() - cb.openedAt.Load() < int64(cb.opt.OpenTimeout) {
            return errCircuitOpen
        }
        // 时间到，尝试进入 HalfOpen
        cb.state.Store(int32(stateHalfOpen))
        cb.halfOpenReq.Store(0)
        fallthrough
    case stateHalfOpen:
        if cb.halfOpenReq.Add(1) > cb.opt.HalfOpenMaxReq {
            return errCircuitOpen
        }
        return nil
    }
    return nil
}
```

`report` 函数根据请求结果更新状态：

```go
func (cb *circuitBreaker) report(failed bool) {
    switch circuitState(cb.state.Load()) {
    case stateClosed:
        if failed {
            if cb.failures.Add(1) >= cb.opt.FailureThreshold {
                cb.tripOpen()
            }
        } else {
            cb.failures.Store(0)
        }
    case stateHalfOpen:
        if failed {
            cb.tripOpen()
        } else if cb.halfOpenReq.Load() >= cb.opt.HalfOpenMaxReq {
            cb.state.Store(int32(stateClosed))
        }
    }
}
```

整个实现就 100 行 Go，**没引入任何第三方依赖**。`atomic.Int64` 是原子操作主力，`sync.Mutex` 只在状态转移时短暂持有。

## 接到 paydown

```go
authed.POST("paydown",
    middleware.CircuitBreaker(middleware.CircuitBreakerOption{
        FailureThreshold: 5,
        OpenTimeout:      10 * time.Second,
        HalfOpenMaxReq:   3,
    }),
    middleware.Idempotency(),
    api.OrderPaymentHandler(),
)
```

中间件顺序很重要：**熔断在外，幂等在内**。
- 熔断挂掉时直接拒绝，不消耗 idempotency token
- 幂等检查通过后才走到真实业务

## 单元测试覆盖

`middleware/circuitbreaker_test.go` 通过一个可控制 success/failure 的 handler，验证完整状态机：

```go
func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
    flip := &flipHandler{}
    flip.shouldFail.Store(true)
    r := newCBRouter(opt, flip.gin())

    // 触发 Open
    for i := 0; i < 2; i++ { call(t, r) }
    // 此时再调 → 70002

    time.Sleep(120 * time.Millisecond)  // 等过 OpenTimeout

    flip.shouldFail.Store(false)
    for i := 0; i < 2; i++ {
        // HalfOpen 探测请求，成功
    }

    // 应该回到 Closed
    if w := call(t, r); strings.Contains(w.Body.String(), "70002") {
        t.Fatal("should have recovered")
    }
}
```

测了三个关键路径：
1. 触发 Open 后拒绝请求
2. HalfOpen 探测全部成功 → 回 Closed
3. HalfOpen 探测失败 → 重新 Open

## 选型建议

**单服务内 / 调单实例下游**：
→ 单机 Token Bucket（简单、不依赖外部存储、延迟可忽略）

**多实例部署 / 全局共享配额**：
→ Redis Lua 滑动窗口

**调用外部依赖（DB / 第三方 API / 微服务）**：
→ 熔断（必须做，不做就等着雪崩）

**所有外网入口**：
→ 限流 + 黑名单 + 验证码三件套（限流是兜底，业务层规则才是主力）

## 一个面试题

> 限流和熔断是不是一回事？

**不是**：
- **限流**关心**入口流量**：上游打过来太多，我拒绝一部分。我自己还是健康的。
- **熔断**关心**出口依赖**：下游故障了，我主动放弃，避免自己被拖死。

两者经常一起部署，但解决的是不同方向的问题。
**入口处限流，出口处熔断**是基本的架构纪律。

## 代码位置

- 令牌桶：`middleware/ratelimit.go` 的 `TokenBucket`
- 滑动窗口：`middleware/ratelimit.go` 的 `SlidingWindow` + `repository/cache/ratelimit.go`
- 熔断器：`middleware/circuitbreaker.go`
- 路由接入：`routes/router.go`（全局令牌桶 + 秒杀滑动窗口 + 支付熔断）
- 单元测试：`middleware/ratelimit_test.go`、`middleware/circuitbreaker_test.go`
- 压测：`stressTest/seckill_rate_limit.js`

## 实操建议

在你自己的项目里：
1. **入口先挂全局 IP 限流**：100-1000 RPS/IP，主要防爬虫
2. **关键业务接口加 ByUser 限流**：秒杀、领券、提交评论之类
3. **所有外部依赖调用包熔断**：DB 不用（连接池已隔离），第三方 API / 微服务必须

**调参方法**：用压测推到你的服务承载上限，然后限流阈值设到 70%。给突发留 30% 余量。

熔断阈值是经验值：FailureThreshold 通常 3-10，OpenTimeout 通常 5-30 秒，HalfOpenMaxReq 通常 1-3。具体看你的依赖故障恢复时间分布。
