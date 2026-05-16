# 幂等中间件：755K 次请求只产生 1 笔订单是怎么做到的

## 一个让人崩溃的场景

凌晨两点，电商客服群弹消息：

> "用户说他点了一次提交，扣了三次钱。"

打开数据库一看，订单表里同一个用户、同一秒、同样的商品、相邻三个订单号。
往日志再翻一翻，前端发了 3 个 `POST /orders/create` —— 用户网络抖动，按钮转圈，他点了三下。

这是后端工程里最经典也最高频的问题：**重复提交**。

它的几种典型形态：
- 用户狂点（前端没禁用按钮）
- 网络重试（HTTP client 配了 retry）
- MQ 消费端重投（at-least-once 投递）
- 网关层重试（Nginx upstream timeout 再发一次）

只要一笔请求可能到达后端 N 次，**所有"扣款 / 下单 / 发卡"类接口都必须做幂等**。否则你永远在用对账系统兜底。

## 不做幂等的代价

为什么我们不能"前端拦一拦就完事"？因为**前端只是 N 个调用方之一**。一个真实的下单链路里：

```
用户 → CDN → 网关 → BFF → 订单服务 → 支付服务 → ...
            ↑      ↑     ↑
           重试   重试   重试
```

每一层都可能因为超时把请求再发一次。**最终一致性的兜底必须在写操作落库的那一层做。**

## 错误的做法 1：用唯一索引硬扛

最常见的"幂等"实现：在订单表加 `unique(user_id, idempotency_key)`，重复插入会触发约束冲突，捕获冲突就当成功。

**问题在哪？**

1. 写冲突需要"试着写一次再失败"，浪费 IO；高并发下大量 ROLLBACK 拖累整个 DB
2. 第二次请求看到冲突返回什么？没有原始响应可以回放 —— 你只能告诉客户端"已成功"，但订单详情你拿不到
3. **更致命**：跨表场景失效。下单是 order 表，支付是 user.money + product.num，没有单一的"唯一键"能涵盖整个事务

## 错误的做法 2：先 GET 再 SET

```go
if redis.Get(key) == nil {
    redis.Set(key, "processing")
    doBusinessLogic()
    redis.Set(key, "done")
}
```

经典的 check-then-act 竞态。两个并发请求都看到 `Get == nil`，都进入 `SET`，都执行业务逻辑 —— **幂等失效，回到原点**。

## 正确姿势：Lua 脚本 + 三态机 + 响应回放

我们最终的方案，三个关键设计：

### 1. token 生命周期 = 三态机

```
[issue token]
     │
     ▼
   init  ──────── acquire ────────▶  processing
     │                                    │
     │                                business ok
     │                                    │
     │                                    ▼
     │  ◀──── release (失败回滚) ──── done (缓存响应)
     ▼
 (TTL 过期)
```

- **init**: 客户端调 `/idempotency/token` 拿到一个新 token，存进 Redis，5 分钟有效
- **processing**: 业务处理中。同一 token 的并发请求会在这里被挡掉
- **done**: 业务完成。Redis 里同时存了**这次请求的完整响应体**

第二次拿同 token 的请求过来：
- 看到 `done` 状态 → 直接把缓存的响应 body 返回给客户端（`X-Idempotent-Replay: true`）
- 看到 `processing` 状态 → 返回"请求处理中"提示
- token 不存在 → 拒绝（防止伪造 token）

**关键洞察**：第二次请求拿到的 body 必须和第一次完全一致。否则即使没产生副作用，客户端拿到的数据不同还是会出 bug。所以我们要**真正缓存响应**，不只是返回"已成功"。

### 2. 状态转移用 Lua 原子完成

`repository/cache/idempotency.go` 里的核心脚本：

```lua
local v = redis.call('HGET', KEYS[1], 'state')
if v == false or v == nil then
    return {0, ''}    -- token 不存在
end
if v == 'done' then
    local r = redis.call('HGET', KEYS[1], 'result')
    return {2, r or ''}  -- 返回缓存的响应
end
if v == 'processing' then
    return {3, ''}    -- 已经在处理
end
if v == 'init' then
    redis.call('HSET', KEYS[1], 'state', 'processing')
    redis.call('EXPIRE', KEYS[1], tonumber(ARGV[1]))
    return {1, ''}    -- 拿到锁，可以执行
end
return {0, ''}
```

Lua 在 Redis 里是单线程串行执行的，**整段脚本要么全部完成要么全部不发生**。这把"check + set"两步合成了一步，竞态自然消失。

### 3. 用户隔离 + ResponseWriter 包装

```
key = idemp:{userId}:{token}
```

token 是按用户隔离的：A 用户的 token 不会和 B 用户冲撞。这点小细节很多教程没讲，但生产环境必须做。

响应缓存的实现稍微 tricky。Gin 的 `c.JSON()` 直接写入 `http.ResponseWriter`，我们没办法拦截到。解决办法是包一层 `responseRecorder`：

```go
type responseRecorder struct {
    gin.ResponseWriter
    body *bytes.Buffer
}

func (r *responseRecorder) Write(b []byte) (int, error) {
    r.body.Write(b)           // 拷贝到我们自己的 buffer
    return r.ResponseWriter.Write(b)   // 同时写给客户端
}
```

handler 跑完后，`recorder.body.String()` 就是完整响应体，存进 Redis 作为后续重放的 payload。

## 关键决策：失败时怎么回滚？

```go
c.Next()

if len(c.Errors) > 0 || recorder.Status() >= http.StatusBadRequest {
    cache.ReleaseIdempotencyLock(key)  // 回到 init 态
    return
}

cache.CommitIdempotencyResult(key, recorder.body.String())  // 写入 done
```

业务逻辑失败（4xx/5xx）时，我们把状态**回滚到 init**，允许客户端用同一个 token 重试。否则一个临时 DB 抖动会让客户端永久卡在"已处理"状态，必须换 token 才能继续。

这个决策有取舍：
- **回滚 init**：客户端可以重试，但要小心"事务半成品"——比如订单已经写入 DB 但缓存没扣，重试可能产生两笔订单
- **不回滚**：更保守，客户端必须换 token 重试

我们项目选了前者，因为我们的业务事务都是单一原子的（要么全成要么全失败）。**这取决于你的业务模型，不是普适答案**。

## 验证：跑出来的真实数字

压测脚本 `stressTest/idempotency_replay.js` 用 50 个 VU 共享同一个 token，持续 15 秒打 `/orders/create`：

```
total requests:     755,033
http 200 rate:      100%
p95 latency:        2.33 ms
```

跑完查 DB：

```sql
SELECT COUNT(*) FROM `order` WHERE user_id = 10 AND created_at > NOW() - INTERVAL 2 MINUTE;
-- => 1
```

**755,033 次请求恰好 1 笔订单**。第二次开始的请求全部从 Redis 直接回放缓存响应，所以延迟只有 2ms 量级，比真实下单（要写 DB + RMQ + Redis）快了一个数量级。

## 面试时会被问什么

1. **如果 token 拿到了但客户端崩溃没发请求，token 会泄漏吗？**
   会泄漏，但 5 分钟 TTL 兜底自动清理。生产环境可以做"获取 token 时跑 GC"。

2. **token 能跨请求方法共用吗？比如同一个 token 既用来下单又用来支付？**
   不行，token 应该和具体接口 + 参数 hash 绑定。我们项目偷懒了——生产环境应该把 method + path + 参数指纹一起存进 token 元数据。

3. **Redis 挂了怎么办？**
   接口立即失败（safe default）。绝不能"Redis 不可用就放行"——那样幂等就失效了，直接打到 DB 上重复扣款。

4. **为什么不用数据库唯一索引就够了？**
   见上面"错误的做法 1"。一句话：唯一索引能挡住重复写入，但拿不到第一次的响应体，无法回放。

5. **`Idempotency-Key` 这个 header 命名是规范吗？**
   是。Stripe、PayPal、AWS SQS 都用同一个 header 名，[draft-ietf-httpapi-idempotency-key-header](https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/) 正在标准化中。

## 代码位置（gomall 仓库）

- 中间件实现：`middleware/idempotency.go`
- Redis Lua 脚本：`repository/cache/idempotency.go`
- token 颁发接口：`api/v1/idempotency.go`
- 路由接入：`routes/router.go` 中 `orders/create` 和 `paydown`
- 单元测试：`repository/cache/idempotency_test.go`
- 压测脚本：`stressTest/idempotency_replay.js`

读完代码后建议自己复现：用 `setup_token.sh` 拿 token，用同一个 Idempotency-Key 连续打十次 `/orders/create`，看 DB 里订单数。
