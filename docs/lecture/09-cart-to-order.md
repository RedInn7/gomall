# 09 购物车到订单：一次点击背后的库存承诺

> 这一讲只跟一位用户走完一次下单：加购、选地址、预扣库存、写订单、记录事件。重点不是把接口背下来，而是解释系统怎样保证“不超卖、不丢单、不重复下单”。

# 购物车职责

购物车主要负责：

- **持久化用户的选购行为**
  用户把商品加入购物车后，系统要记住：谁、买什么、买几件、选了哪个规格/SKU、是否勾选。
  即使浏览器关闭、App 退出，下次登录还能看到。
- **支持加购、减购、删除、修改数量**
  这些操作要保证并发安全（同一用户多端操作）、数量上限控制（MaxNum）、以及异常时的正确语义（数据库挂了不能假装购物车是空的）。
- **跨会话、跨设备同步**
  未登录时用 Cookie / 本地存储 + 临时 ID；登录后把游客购物车合并到用户购物车，避免丢数据。
- **提供结算前的预览信息**
  展示商品标题、图片、当前价格、小计、可用优惠券、运费预估等，让用户有个“大概要付多少钱”的感觉。

![购物车中的商品预览](./assets/cart-product-preview.png)

# 边界

这是很多系统容易踩坑的地方。购物车**不负责**以下事情：

| 事项            | 为什么不由购物车负责                        |
| --------------- | ------------------------------------------- |
| 最终成交价格    | 价格可能随时变动，必须以商品表/定价服务为准 |
| 真实库存扣减    | 购物车只做软校验，真正预占在下单时发生      |
| 卖家/收款方确认 | 必须重新从商品表读取，防止客户端伪造        |
| 订单生成与支付  | 订单是不可变的交易记录，购物车是可变的意向  |
| 优惠最终落地    | 满减、券、积分等必须在下单瞬间重新计算      |

所以下单时系统必须：

1. 重新读取商品最新价格、状态、卖家信息；
2. 重新校验地址归属；
3. 真正预扣库存；
4. 写入订单 + 事件。

购物车只提供“用户想买什么、买几件”这个意图。

## 一、先看业务：用户只点了一次“提交订单”

假设小林一次购买两件商品。对用户来说只是点一下“购买”，但后端需要同时保证价格、库存、订单和消息都不会出错。

这一讲主要关注三个必须始终成立的规则。

### 1. 商品价格只能由服务端决定

客户端只负责告诉后端：

```text
买哪个商品
买几件
```

真正的价格、卖家等信息必须重新从服务端商品表读取。

例如商品真实价格是 999 元，即使有人抓包把前端请求里的价格改成 0.01 元（非常常见），后端也不能按 1 分钱创建订单。

```text
product_id
    ↓
查询服务端商品数据
    ↓
得到真实价格和卖家
    ↓
按真实数据创建订单
```

这样可以避免客户端篡改价格。

### 2. 订单和 OrderCreated 事件必须保持一致

订单创建成功后，通常还要通知其他系统：

```text
订单创建成功
    ↓
OrderCreated
    ├─ 超时取消
    ├─ 风控
    └─ 其他下游服务
```

系统不能出现这种情况：

```text
订单已经写入 MySQL ✅
OrderCreated 消息却没有发出去 ❌
```

否则订单虽然存在，但超时取消、风控等下游完全不知道。

因此需要保证：

> **订单创建成功时，对应的 OrderCreated 事件也必须可靠产生。**

### 3. 先预占库存，失败时及时释放

假设小林要买 2 件商品，系统会先尝试预占 2 件库存：

```text
库存 5
  ↓
预占 2
  ↓
剩余可用库存 3
  ↓
创建订单
```

如果库存不足，预占失败，就不能继续创建订单。

```text
需要 2 件
库存只有 1 件
    ↓
预占失败
    ↓
不创建订单
```

反过来，如果库存已经预占成功，但后面的建单失败：

```text
预占库存 ✅
    ↓
创建订单 ❌
```

系统就要尝试释放刚才预占的库存，否则会出现“没有订单，库存却被占住”的问题。

所以整个原则可以概括成：

> **客户端没有定价权；订单和事件必须保持一致；库存先预占，建单失败要释放。**

后面的下单代码，基本都是围绕这三个规则展开。

### 请求进入系统的位置

同步下单走 `POST /api/v1/orders/create`，路由挂了认证和幂等中间件。异步下单走 enqueue 接口，先返回 ticket，再由前端查询 ticket 状态。两条路最终都要得到一条权威订单。

```mermaid
flowchart LR
    U["用户确认购物车"] --> A["校验地址归属"]
    A --> P["从商品表读取价格与卖家"]
    P --> R["Redis 预扣库存"]
    R --> T["MySQL 写订单和 outbox"]
    T --> W["等待支付"]
```

## 二、下单前：购物车、地址与定价

购物车只是“购买意向”，不能作为结算凭证。用户可能在购物车停了三天，期间商品价格、库存和上下架状态都变了。因此下单时要重新读取权威数据。

### 购物车查询要分清三种结果

`GetCartById` 的返回不是简单的“有或没有”：

| 查询结果 | 处理 |
|---|---|
| 命中 | 在 `MaxNum` 内增加数量 |
| `gorm.ErrRecordNotFound` | 新建购物车行 |
| 连接失败、超时、死锁 | 返回系统错误，不能假装购物车为空 |

```go
cart, err = d.GetCartById(pId, uId, bId)
if errors.Is(err, gorm.ErrRecordNotFound) {
    cart = &Cart{UserID: uId, ProductID: pId,
        BossID: bId, Num: 1, MaxNum: 10}
    err = d.DB.Create(&cart).Error
    return cart, e.SUCCESS, err
}
if err != nil {
    return nil, e.ERROR, err
}
```

这段代码的教学点不是 `errors.Is` 的语法，而是错误语义：查不到可以创建，查失败必须停下来。

### 地址 ID 也是不可信输入

客户端传来的 `address_id` 可能属于另一个用户。`OrderCreate` 在任何库存动作之前调用 `EnsureOwned(req.AddressID, u.Id)`；校验失败要尽早返回，否则既泄露他人地址，又制造无效预占。

### 金额与收款方只从商品表读取

```go
unitCents, categoryID, bossID, err := resolveProductPricing(
    ctx, req.ProductID,
)
if err != nil {
    return nil, err
}
subtotalCents := unitCents * int64(req.Num)
```

`req.Money` 和 `req.BossID` 都不能进入计费链路。信任前者，买家可以一分钱下单；信任后者，买家甚至能指定货款打给谁。

## 三、订单号和订单状态

数据库主键解决表内关联，`OrderNum` 才是暴露给用户、客服和消息系统的业务编号。gomall 用雪花算法生成趋势递增的 `int64`，再转成模型里的 `uint64`。

```go
func InitSnowflake(machineID int64) {
    snowflake.Epoch = time.Date(
        2024, 1, 1, 0, 0, 0, 0, time.UTC,
    ).UnixNano() / 1_000_000
    node, err = snowflake.NewNode(machineID)
    if err != nil {
        panic(err)
    }
}
```

这里真正要盯的是部署配置：多实例若使用同一个 `machineID`，同毫秒内可能生成重复编号。当前 `OrderNum` 没有数据库唯一索引，撞号时数据库也不会兜底；生产环境应保证 machine ID 唯一，并给业务编号加唯一约束。

新订单的初始状态是 `OrderWaitPay`。下单不会直接改成已支付，也不会真正消耗商品库存；它先为用户保留一段付款窗口。

## 四、主链：预扣库存、写订单、写事件

### 为什么库存要分桶

把库存想成三只桶：

- `available`：仍可出售；
- `reserved`：已下单但未付款；
- `sold`：付款后确认售出。

下单把数量从 `available` 移到 `reserved`；支付成功再减少 `reserved` 并扣数据库商品库存；取消或超时则把预占还给 `available`。这样既给用户付款时间，又不会把同一件商品卖给两个人。

### 标准交互时序

```mermaid
sequenceDiagram
    actor User as 用户
    participant API as Order API
    participant Redis as Redis 库存
    participant DB as MySQL 事务
    participant Outbox as Outbox Publisher
    participant MQ as RabbitMQ

    User->>API: 提交 product_id、num、address_id
    API->>API: 校验地址，反查价格与卖家
    API->>Redis: ReserveStock(product_id, num)
    alt 库存不足
        Redis-->>API: ErrStockInsufficient
        API-->>User: 下单失败
    else 预扣成功
        Redis-->>API: available -= num, reserved += num
        API->>DB: BEGIN
        API->>DB: INSERT order
        API->>DB: INSERT outbox(order.created)
        DB-->>API: COMMIT
        API-->>User: 返回 OrderNum
        Outbox->>MQ: 发布 OrderCreated
        MQ-->>Outbox: ack 后标记 sent
    end
```

### `OrderCreate` 的事务边界

```go
if err = cache.ReserveStock(ctx, req.ProductID, int64(req.Num)); err != nil {
    return nil, err
}

err = dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
    if err := NewOrderDaoByDB(tx).CreateOrder(order); err != nil {
        return err
    }
    // 满减预算在这里扣；预算耗尽时降级为无折扣
    return outbox.NewOutboxDaoByDB(tx).Insert(
        "order", "OrderCreated", "order.created", order.ID,
        events.OrderCreated{OrderID: order.ID, OrderNum: order.OrderNum,
            UserID: u.Id, ProductID: req.ProductID, Num: int(req.Num)},
    )
})
if err != nil {
    _ = cache.ReleaseReservation(ctx, req.ProductID, int64(req.Num))
    return nil, err
}
```

Redis 不能参加这次 MySQL 本地事务，所以 reserve 放在事务外。DB 失败后，代码用 `ReleaseReservation` 做补偿；如果补偿也失败，只能依赖库存对账发现差额。这是最终一致，不是跨存储强事务。

订单与 outbox 必须在同一个 MySQL 事务里。否则会出现两种坏结果：订单存在但没有事件，下游永远不知道；或者事件已经发出，订单却没创建成功。

事务提交后，代码还会把订单加入 Redis 的超时集合，并尝试发布延迟取消消息。两步都不在建单事务内，发布失败只记日志，因此超时扫描与对账仍然有必要。

## 五、用户连点五次：幂等和补偿

幂等键来自请求头 `Idempotency-Key`，并与用户 ID 组合，避免不同用户互相命中。Lua 状态机的课堂版可以记成四种结果：

| 状态 | 中间件行为 |
|---|---|
| token 不存在或过期 | 拒绝请求 |
| 首次获得执行权 | 放行到 `OrderCreate` |
| 正在处理 | 返回“处理中”，不再执行 |
| 已完成 | 回放上一次 JSON，并加 `X-Idempotent-Replay: true` |

```go
switch state {
case 0:
    c.Abort() // token 不存在或已过期
case 2:
    c.Header("X-Idempotent-Replay", "true")
    c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(cached))
    c.Abort()
case 3:
    c.Abort() // 同一个请求仍在执行
}
```

注意边界：幂等中间件缓存响应，不等于数据库自动幂等。若业务已落库而提交幂等结果连续失败，中间件会释放锁，让客户端重试；资金和订单表仍应有唯一约束或状态守卫兜底。

失败补偿按责任归类：

| 失败位置 | 当前动作 | 仍需什么兜底 |
|---|---|---|
| 预扣库存失败 | 不建订单 | 无 |
| DB 事务失败 | 尝试释放预占 | 库存对账 |
| outbox 发布失败 | publisher 重试 | 死信与告警 |
| 延迟取消发布失败 | 只记日志 | Redis 超时集合扫描 |

## 六、同步与异步下单怎么选

同步接口适合普通流量：用户等待数据库事务结束，马上拿到订单号。大促峰值时，可以先走 `OrderEnqueue`：

1. 校验地址并预扣库存；
2. 写一个 TTL 为 1 小时的 `pending` ticket；
3. 发布 `AsyncOrderTask`；
4. 立即返回 ticket，消费者建单后把状态改成 `ok` 或 `failed`。

任务消息不携带金额和卖家，消费者仍从商品表反查，安全边界没有因为异步化而降低。

```go
type AsyncOrderTask struct {
    Ticket    string `json:"ticket"`
    UserID    uint   `json:"user_id"`
    ProductID uint   `json:"product_id"`
    Num       uint   `json:"num"`
    AddressID uint   `json:"address_id"`
}
```

异步不是免费的：ticket 写成功而 MQ 发布失败时要释放库存，并把 ticket 标成 `failed`；消费端还要处理重复消息。没有削峰需要时，同步链更容易排障。

## 七、课堂演示与回顾

只做一个演示：同一个 `Idempotency-Key` 连续提交两次下单请求。

观察三件事：第二次响应是否带 `X-Idempotent-Replay`，订单表是否只多一行，库存预占是否只变化一次。若环境不完整，就用断点跟踪 `Idempotency()`、`OrderCreate()` 和 `ReserveStock()`，不要临时讲完整压测。

## 八、收束：用不变量做代码评审

学生离开这一讲前，应能回答：

- 为什么 `address_id`、金额和卖家都要在服务端核验？
- 为什么 Redis reserve 在 MySQL 事务外，而 outbox 在事务内？
- 如果 DB 回滚后 Redis release 失败，系统靠什么发现？
- 幂等回放解决了哪一种重复，数据库还需要守什么底线？

一句话记忆：**先核验权威数据，再预留稀缺资源；订单与事件原子落库，跨存储失败靠补偿和对账收口。**

## 课后延伸

- 画出异步消费者重复消费同一个 ticket 时的状态机，并给出幂等点。
- 设计库存巡检：输入 `available`、`reserved` 和数据库已售数量，输出需要告警的差额。
- 阅读 `internal/order/cancel.go`，说明超时取消怎样与支付竞争。
- 思考订单地址快照该包含哪些字段，以及修改地址簿为何不能影响历史订单。

代码入口：`internal/order/service.go`、`internal/order/async.go`、`repository/cache/inventory.go`、`middleware/idempotency.go`。
