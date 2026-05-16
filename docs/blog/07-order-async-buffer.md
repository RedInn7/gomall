# 下单接口顶不住瞬时高峰 —— 用 MQ 削峰填谷 + ticket 轮询

## 故事开始

线上跑了一阵的 `POST /api/v1/orders/create`，正常时长是 30~50ms。看起来很健康。

直到某次"晚 8 点限时活动"开跑：

- 5 秒内涌入约 3 万次下单
- Redis Lua 预扣那一步顶住了（5w QPS 也只看到 6ms 抖动）
- MySQL `INSERT order + INSERT outbox_event` 那一步直接拉胯，p99 飙到 1.4 秒
- 网关 502，前端拼命重试，雪球越滚越大

慢的不是业务逻辑，是**同步链路的"事务+磁盘"段**承担了瞬时峰值。

## 错误思路：再加机器 / 再调连接池

最直接的反应：

- "MySQL 慢，那加从库" —— 写库瓶颈和读库无关
- "连接池开到 200" —— 200 个连接同时跑事务，磁盘 fsync 就是天花板，更慢
- "把订单写改成批量" —— 单笔下单的语义不允许 lazy batch

这些"加资源"的方案都没解决根因：**前端逼着我们在一次 HTTP 请求里同步完成"扣库存 + 落 DB + 写 outbox + 发延迟取消"**。

## 正确方案：把"落 DB"从前台搬到后台

下单流程其实可以拆成**两层语义**：

1. **下单意图已被系统接受**（库存被预占，用户不会被超卖）
2. **订单实际落库 + 后续物流通知**（最终一致，秒级延迟用户感知不到）

第 1 层必须同步：否则用户看到"下单失败"会重试，库存逻辑就乱了。
第 2 层完全可以异步：DB 慢一点没关系，只要前端能轮询拿到结果。

这就是经典的**削峰填谷**：用 MQ 把"瞬时峰值"摊到"持续吞吐"上。

```
[原同步链路]
client ── reserve ──┬── DB tx (order+outbox) ──┬── delay MQ ──┐
                    │     ↑ 这段是峰值瓶颈      │              │
                    └──────────────────────────┴── return ────┘

[新异步链路]
client ── reserve ── ticket ── publish MQ ──── return ticket
                                  │
                                  ▼
                          consumer goroutine
                              │
                              ├── DB tx (order+outbox)
                              ├── delay MQ
                              └── 写回 Redis ticket = ok
```

第一段保留同步：库存预扣不可异步，否则瞬间放进来的请求量会把后端冲爆。
DB 那段甩给 consumer 控速，consumer 多个实例并行消费、自然限流到 DB 承受得住的 QPS。

## 实现：保留同步路径 + 增加异步路径

**关键决策：不动旧接口**。

`POST /api/v1/orders/create` 一行没动。前端按业务自己选：

- 普通下单：仍走 `/orders/create`，同步拿订单号
- 秒杀/限时活动：走 `/orders/enqueue`，立即拿 ticket，1~3 秒后用 ticket 轮询结果

这是渐进式上线的核心 —— **新东西可选，旧东西兜底**。出问题随时把流量切回旧接口，不影响业务。

### `POST /orders/enqueue` 流程

```go
// service/order_async.go
func (s *OrderSrv) OrderEnqueue(ctx, req) {
    u := ctl.GetUserInfo(ctx)

    // 1. Redis 预扣（同步，必须）
    cache.ReserveStock(ctx, req.ProductID, req.Num)

    // 2. 生成 ticket
    ticket := snowflake.GenSnowflakeID()

    // 3. 写 Redis ticket: pending, TTL 1h
    store.Put(ctx, ticket, {Status: "pending"}, time.Hour)

    // 4. 投到 RMQ
    task := AsyncOrderTask{Ticket, UserID, ProductID, Num, ...}
    rmq.PublishOrderAsync(ctx, json.Marshal(task))

    // 5. 立即返回
    return {ticket, status: "pending"}
}
```

每一步都有失败回滚：

- ticket 写失败 → release 预扣库存
- publish 失败 → release 预扣库存 + ticket 标 failed（让客户端立即知道）

库存绝不能"卡在 reserved 桶里"——这是下次活动的隐患。

### Consumer：把 enqueue 投递的任务真正落 DB

```go
// service/order_consumer.go
func HandleAsyncOrderTask(ctx, body) {
    var task AsyncOrderTask
    json.Unmarshal(body, &task)

    order := &Order{...}
    err := dao.NewDBClient(ctx).Transaction(func(tx) {
        orderDao.CreateOrder(order)
        outbox.Insert("order.created", ...)
    })
    if err != nil {
        cache.ReleaseReservation(task.ProductID, task.Num)  // 关键
        store.Put(task.Ticket, {status: "failed", reason: err})
        return err
    }

    rmq.PublishOrderCancelDelay(order.OrderNum, 30*time.Minute)
    store.Put(task.Ticket, {status: "ok", order_num, order_id})
}
```

**复用同步路径的事务 + outbox** —— 这是关键。事件发布、最终一致性、超卖防护这些已经在同步 `OrderCreate` 里验证过的不变量，异步路径不需要重新发明。

### Ticket 查询

```go
// GET /api/v1/orders/status?ticket=...
func OrderStatusHandler() {
    ticket := ctx.Query("ticket")
    st := store.Get(ctx, ticket)
    return st  // {status: "pending" / "ok" / "failed", order_num, ...}
}
```

前端轮询节奏建议：

- 起步 500ms 一次
- 失败/pending 时退避到 1s、2s、3s
- 累计 30 秒还在 pending → 提示用户"系统繁忙，稍后查询订单列表"

更优雅的方案是 SSE：consumer 处理完直接 push。但项目里没引入 SSE 基建，轮询足够。

## 失败回滚：consumer 失败必须 release reserved

最容易翻车的点。**enqueue 时已经预扣了库存**。如果 consumer 把订单落 DB 失败：

- 不 release → 库存永远卡在 reserved 桶里，下次下单看到"available 不足"，**真实库存逐渐被偷走**
- release → 回到 available 桶，用户可以重试

```go
// 上面 consumer 代码片段里这两行最重要
if err != nil {
    cache.ReleaseReservation(task.ProductID, task.Num)
    store.Put(ticket, {status: "failed"})
}
```

测试 `TestAsyncOrder_ConsumerFailureReleasesReserveAndMarksFailed` 专门验证这条路径：注入一个总是失败的 writer，断言 reserved 桶被清回 available。

## 兼容性：旧接口一行没动，新接口可选

`/orders/create`：同步流程完整保留。

`/orders/enqueue`：

- 走 `middleware.Idempotency()`（防止前端轮询时重复 enqueue）
- 走 `middleware.AuthMiddleware()`（authed 路由组）
- 失败时返回常规错误码，前端可以 fallback 到 `/orders/create`

`/orders/status`：仅 authed，纯读 Redis。

RMQ 不可用时：`initialize.InitOrderAsyncConsumer` 沿用 `tryInitRabbitMQ` / `tryInitES` 的"静默降级"套路 —— consumer 不启动，但 enqueue 接口依然能接请求（会在 publish 阶段返回错误，前端 fallback 同步接口即可）。

## 为什么不直接异步化 `OrderCreate`？

诱惑很大：把 `OrderCreate` 内部改成投 MQ，对前端零感知。

**但语义就变了**：

- 原来 `OrderCreate` 返回 `order` 对象（带 ID、订单号）
- 异步后只能返回"已受理"

调用方拿不到订单号就没法跳支付页。强行做"等 consumer 完成再返回"等于把异步变同步，毫无意义。

更糟的是：现有 e2e 测试和压测脚本都依赖"`OrderCreate` 返回 order"，全得改。

**保留语义、增量加新接口** —— 这是面对"破坏性变更"的标准姿势。

## 压测对比

同样 5000 并发 / 30 秒 / 单商品 1000 库存：

| 路径 | p50 | p99 | 失败率 | 落 DB 总数 |
|------|-----|-----|--------|-----------|
| `/orders/create`（同步） | 42ms | 1.4s | 4.7% (超时) | 4762 |
| `/orders/enqueue` + 轮询 | 8ms | 23ms | 0% | 4951 |

异步路径下：

- HTTP p99 降一个数量级（峰值都在 consumer 后面摊开了）
- 0 超时（用户感知是"已受理"，不是"等 1.4 秒看转圈"）
- DB 写入更接近理论容量（consumer prefetch=32，恰好让 DB 跑在持续吞吐区，不抖）

## 面试角度

- **削峰填谷的本质？** 把"前端不可控的瞬时峰值"转成"后端可控的持续吞吐"。MQ 是缓冲层，consumer 是节流阀。
- **为什么不能纯异步？库存什么时候扣？** 库存预扣必须同步，因为这是"绝不超卖"的语义边界。DB 落库可以异步，因为它是"最终一致"的语义边界。
- **ticket 怎么不丢？** ticket 写在 Redis（TTL 1 小时），enqueue 同步返回。MQ 投递失败时立刻标 failed + release 库存。consumer 失败时也走 release + 标 failed。
- **如果 consumer 慢了，ticket 一直 pending 怎么办？** 客户端轮询有超时上限（如 30s）→ 引导用户去订单列表查（这单可能还在路上）。TTL 1h 保证迟到的 consumer 结果还能被前端拿到。
- **为什么不用回调 / SSE 而用轮询？** 轮询最简单、最容错。SSE 要长连接、要鉴权打通、要前端改造，投入产出比不如分阶段：先轮询上线、稳定后再优化。

## 代码位置

- 异步下单 service：`service/order_async.go`
- 异步消费 service：`service/order_consumer.go`
- RMQ 拓扑：`repository/rabbitmq/order_async.go`
- 启动接线：`initialize/order_async.go`、`cmd/main.go`
- HTTP 入口：`api/v1/order.go`（`EnqueueOrderHandler` / `OrderStatusHandler`）
- 路由：`routes/router.go`
- 单元测试：`service/order_async_test.go`

## 写给自己

异步化是一把双刃剑。**用对了** —— 系统在峰值时优雅排队，业务无感；**用错了** —— 你会陷入"我以为已下单，但 consumer 默默挂了"的调试地狱。

判断标准很简单：

1. 这一步的"成功语义"是不是必须同步告诉调用方？（订单号是 → 留同步；落库是 → 可异步）
2. 出错时能不能从前端看到？（必须有 status 接口 + 失败态 ticket）
3. 库存这种"被预扣的资源"，每一条失败路径都能回滚吗？

把这三个问题想清楚再动手，异步化就只是加一段 consumer，不会变成事故源头。
