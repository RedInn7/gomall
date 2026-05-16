# 我在自己的项目里发现了一个库存虚高 bug —— 顺便讲讲 Outbox + Saga

## 故事开始

上周我盘 gomall 的代码，看 `OrderCreate` 和 `CancelUnpaidOrder` 这对孪生函数：

```go
// 下单
func (s *OrderSrv) OrderCreate(ctx, req) {
    order := &Order{...}
    orderDao.CreateOrder(order)         // 写订单
    // ... 没了。库存呢？
}

// 关单（订单超时未支付）
func CancelUnpaidOrder(orderNum uint64) error {
    return Transaction(func(tx) {
        ok, _ := orderDao.CloseOrderWithCheck(orderNum)
        if !ok { return nil }
        // ← 关单成功就……
        productDao.RollbackStock(order.ProductID, order.Num)  // 把库存加回去？？
    })
}
```

慢着 —— `OrderCreate` **从来没扣过库存**，但 `CancelUnpaidOrder` 在订单取消时调 `RollbackStock` 把库存加回来。

这意味着什么？

**每个未支付订单超时一次，商品库存就凭空 +N**。

模拟一下：商品库存 100，10 个用户下单各买 1 件不付款，30 分钟后系统自动关单。结果商品库存变成 **110**。再来 10 个，变成 **120**。

线上跑久了，**库存数字逐步膨胀，超卖事故的种子已经埋下**——直到某天用户付钱时系统提示"库存不足，请重试"，但售前还在显示"有货"。

## 修这个 bug 的两条路

### 路 A：尊重现状 + 改 Cancel

直接的修复：判断订单原来是否扣过库存，没扣过就别加回去。

```go
if order.HasDeductedStock {  // 加一个字段
    productDao.RollbackStock(...)
}
```

**问题**：
- 增加了一个状态字段，需要 migration
- 状态字段会蔓延（"hasDeductedStock"、"hasNotifiedBoss"、"hasIssuedInvoice"……订单表迟早爆炸）
- 没解决根本架构问题：**库存的扣减时机始终不清晰**

### 路 B：重新设计库存状态机

库存其实从来就是个状态机，我们只是没把它建模出来：

```
available (可下单) ──reserve──▶ reserved (已下单未支付)
                                       │
                              ┌────────┴─────────┐
                              ▼                  ▼
                  commit (支付成功)        release (取消/超时)
                              │                  │
                              ▼                  ▼
                       真正消耗的库存        退回 available
```

这就是**库存预扣 + Saga**。下面的关键好处：
- **绝不超卖**：reserve 失败立即拒单
- **逻辑清晰**：每个状态转移都有明确触发点和补偿动作
- **可追溯**：每次状态变更都发一条事件，下游能感知

代价：要重做下单/支付/取消三处。但**这是一次性投资，未来加退款、改库存维度等等都顺**。

我们走了路 B。

## 实现细节：Redis Lua 双桶

`repository/cache/inventory.go`：

```
stock:available:{pid}   还能下单的数量
stock:reserved:{pid}    被未支付订单占住的数量
```

启动时由 syncer 把 DB 里 `product.num` 复制到 Redis available 桶。

**Reserve（下单时）**：

```lua
local avail = redis.call('GET', KEYS[1])
if avail == false then return -2 end
if tonumber(avail) < tonumber(ARGV[1]) then return -1 end
redis.call('DECRBY', KEYS[1], ARGV[1])   -- available -= N
redis.call('INCRBY', KEYS[2], ARGV[1])   -- reserved += N
return 1
```

**Commit（支付成功）**：reserved 桶减掉，库存正式售出。

**Release（取消/超时）**：reserved 桶减掉，available 桶加回去。

三个 Lua 脚本各自原子，并发 500 个 goroutine 抢 100 件库存的单元测试**实测零超发**。

## 但还有一个问题：事件丢失

新代码大致这样：

```go
func OrderCreate(ctx, req) {
    cache.ReserveStock(...)              // 1. Redis 预扣
    db.Create(order)                     // 2. 写订单
    rabbitmq.Publish(orderCreatedEvent)  // 3. 发事件
}
```

但凡步骤 3 失败（RMQ 挂了 / 网络抖动），订单已经落库了**但是事件丢了**。下游服务（搜索同步、对账系统、风控）永远不知道这单的存在。

反过来，先发事件再写 DB 也不行：**事件发出去了但 DB 没写成**——下游看到一笔不存在的订单。

**这就是分布式系统里最经典的"双写问题"**。

## Outbox 模式：把发事件变成"同事务写"

解决思路非常聪明：**别真发 MQ，把事件先存到 DB 的一张专用表里**。

```sql
CREATE TABLE outbox_event (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    aggregate_type VARCHAR(64),
    aggregate_id   BIGINT,
    event_type     VARCHAR(64),
    routing_key    VARCHAR(128),
    payload        TEXT,
    status         INT,           -- 1 pending, 2 sent, 3 dead
    attempts       INT,
    next_retry_at  DATETIME,
    last_error     VARCHAR(512)
);
```

`OrderCreate` 改成这样：

```go
db.Transaction(func(tx *gorm.DB) error {
    if err := orderDao.CreateOrder(order); err != nil { return err }
    return outboxDao.Insert("order", "OrderCreated", "order.created", order.ID, payload)
})
```

**关键**：订单和事件**在同一个事务里**写。要么两个都成功，要么两个都失败。**事件不可能"丢失"——它已经被持久化到 DB**。

然后呢？一个后台 goroutine（publisher）定时扫这张表：

```go
for ev := range outboxDao.FetchBatch(100) {
    if err := rmq.PublishDomainEvent(ev.RoutingKey, ev.Payload); err != nil {
        outboxDao.MarkFailed(ev.ID, ...)   // 标记失败，指数退避后重试
        continue
    }
    outboxDao.MarkSent(ev.ID)
}
```

publisher 拿到 pending 事件 → 投到 RMQ → 标记 sent。失败的指数退避重试（1s → 2s → 4s ... 上限 5min），超过 10 次标记为 dead，留人工处理。

**这就是 Outbox 模式**：DB 是事件的最终持久化层，MQ 只是异步分发的传输通道。

## 配合幂等：消费者必须接受重复投递

Outbox 的"至少一次"语义意味着同一事件可能投递多次（网络抖动 → 重试 → 真的成功了但是 MarkSent 失败 → 下次又被 fetch 出来）。

这正是为什么消费端必须幂等：

```
事件 ID → 处理过吗？  → 处理 + 标记
              └─ 处理过了 → 跳过
```

我们项目里所有消费者都遵循这个原则。库存的 commit/release 是天然幂等的（基于状态判断），ES 索引同步也是幂等的（upsert by id）。

## 完整链路：3 个特性串起来

最终的下单 → 支付流程：

```
[OrderCreate]
    ├─ Redis Lua: available -= N, reserved += N
    ├─ TX {
    │     db.Create(order)
    │     outbox.Insert("order.created", ...)
    │  }
    └─ TX 失败 → Redis Release（回滚预扣）

[publisher goroutine]
    每 1 秒 → outbox.FetchBatch(pending)
              ├─ RMQ.Publish(order.created)
              ├─ 成功 → outbox.MarkSent
              └─ 失败 → outbox.MarkFailed (退避后重试)

[Payment]
    └─ TX {
          db.UpdateOrder(paid)
          db.UpdateProduct(num -= N)
          outbox.Insert("order.paid", ...)
       }
       └─ TX 成功后 → Redis: reserved -= N (commit)

[Cancel (timeout 触发)]
    └─ TX {
          db.UpdateOrder(cancelled)
          outbox.Insert("order.cancelled", ...)
       }
       └─ TX 成功后 → Redis: reserved -= N, available += N (release)
```

**修了原 bug**：未支付订单取消不再 `RollbackStock`，因为从来没真正扣过 `product.num`。只是 Redis 预占解除。

## 顺便：搜索同步用同一套机制

ES 索引同步是 Outbox 的另一个消费者：

```go
// service/search/indexer.go
ch.Consume("search.product.indexer", ...)
// 订阅 product.changed
// 收到事件 → 查 DB 拿最新数据 → ES upsert / delete
```

商品 Create/Update/Delete 时，业务代码插入一条 `product.changed` 到 outbox。publisher 发到 RMQ，indexer 消费，更新 ES。

**业务代码完全不知道 ES 的存在**。这就是事件驱动的解耦红利。

## 验证

### 库存零超卖（500 并发）
`repository/cache/inventory_test.go` 的 `TestInventory_NoOversellUnderConcurrency`：500 个 goroutine 抢 100 件，**实测恰好 100 个成功**。

### Outbox 重试 + dead
`repository/db/dao/outbox_test.go` 的 `TestOutbox_MarkFailedBackoff`：
- 失败一次 → attempts=1，next_retry_at 退到 1s 后
- 立即再 FetchBatch → 这条不会被取出（退避中）
- 失败超过阈值 → 状态变 dead，FetchBatch 永远不再取

## 验证 RabbitMQ 故障演练

最有教育意义的实验：**当 RMQ 挂了，会发生什么？**

```bash
# 1. 起 gomall + 下几单
# 2. 把 RMQ 停掉
docker stop rabbitmq

# 3. 继续下单（业务不受影响！）
curl -X POST .../orders/create -d '...'

# 4. 看 outbox 表
SELECT COUNT(*) FROM outbox_event WHERE status = 1;
# pending 数量持续增长

# 5. RMQ 恢复
docker start rabbitmq

# 6. 30 秒内 publisher 自动追平
SELECT status, COUNT(*) FROM outbox_event GROUP BY status;
# status=2 (sent) 全部 catch up
```

**业务代码无需任何感知**。Outbox 把"分布式系统的脆弱环节"和"核心业务逻辑"彻底解耦。

## 总结：什么时候该用 Outbox

✅ 适合：
- 写 DB + 发 MQ 必须最终一致
- 下游有多个消费者（用同一个事件源）
- 业务需要审计追溯（事件表本身就是审计日志）

❌ 不适合：
- 极低延迟要求（事件投递有 1 秒级延迟）
- 只是简单"通知" 而不要求可靠（直接发就行）
- 完全无 DB 的纯 MQ 场景

## 面试角度

- **DB + MQ 怎么保证一致？** Outbox。
- **Outbox 怎么处理 publisher 投递失败？** 失败计数 + 指数退避 + max attempts 后 dead。
- **怎么避免消息重复消费？** 消费者必须幂等（或上游 message-id 去重）。
- **怎么解决"事务表 + 业务表"在分库分表场景的问题？** 用 TCC、Saga、或者 Outbox + Saga 组合。
- **Saga 是补偿事务，跟 2PC 有什么区别？** 2PC 是同步阻塞 + 两阶段；Saga 是异步 + 通过补偿动作回滚。

## 代码位置

- Outbox 模型：`repository/db/model/outbox.go`
- Outbox DAO：`repository/db/dao/outbox.go`
- Publisher：`service/outbox/publisher.go`
- 库存 Lua：`repository/cache/inventory.go`
- 库存 syncer：`service/inventory/syncer.go`
- 订单流程：`service/order.go`、`service/payment.go`、`service/order_cancel.go`
- ES 消费者：`service/search/indexer.go`
- 单元测试：`repository/db/dao/outbox_test.go`、`repository/cache/inventory_test.go`

## 写给自己

这次重构最大的收获不是"上了 Outbox / Saga"，是**通过引入合适的模型，让一个隐藏 bug 自然暴露并消失**。

写代码时如果你觉得"这块逻辑哪里不对劲但说不清"——不要绕开，停下来把模型画清楚。多半就是缺了某个状态、某个事件、某个边界。这种"察觉到代码异味"的能力，是高级工程师和初级工程师真正的差距。
