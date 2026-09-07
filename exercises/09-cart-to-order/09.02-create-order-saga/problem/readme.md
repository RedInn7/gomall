# 09.02 创建订单与失败补偿

## 题目背景

用户提交订单后，系统先在 Redis 把库存从 `available` 移到 `reserved`，再在 MySQL 的同一个事务里写订单和 `order.created` Outbox 事件。Redis 不受 MySQL 本地事务控制：如果库存预占成功而订单事务失败，系统必须执行反向操作释放预占，否则没有订单的库存会一直被占住。

请求重试也会发生。相同订单和事件已经成功落地时，再执行一次不能重复预占；相同订单号或事件 ID 指向不同内容时，则必须拒绝。

## 题目描述

实现 `CreateOrderSaga`：

1. 校验依赖、订单字段、`OrderWaitPay` 初始状态和非空事件 ID。
2. 对已成功的完全相同请求做幂等重放；订单号或事件 ID 冲突时拒绝，且不能触碰库存。
3. 先预占库存。库存不足或预占依赖失败时，不写订单和事件。
4. 在一次 `store.transaction` 中写订单和 `order.created` 事件，保证同时提交或同时回滚。
5. MySQL 事务失败后释放预占库存。补偿也失败时，返回值必须同时保留原事务错误与 `ErrReleaseFailed`。

## 输入与已有接口

```go
func CreateOrderSaga(inv *Inventory, store *Store, order Order, eventID string) error
```

题目提供内存版库存与事务模型。`FailReserve`、`FailRelease`、`FailOrder` 和 `FailOutbox` 用于模拟各步骤失败。

## 输出与完成条件

成功后：

- 对应数量从 `Available` 转到 `Reserved`；
- 订单状态为 `OrderWaitPay`；
- 订单与 Outbox 事件同时存在。

失败后，MySQL 不能留下半状态；若补偿成功，库存恢复调用前状态。相同成功请求重放时，库存和两张表都不再变化。

## 调用样例

```go
inv := NewInventory(7, 5)
store := NewStore()
order := Order{Number: "ord-1", UserID: 10, ProductID: 7, Num: 2, State: "OrderWaitPay"}

err := CreateOrderSaga(inv, store, order, "evt-ord-1")
// err == nil
// inv.Available[7] == 3
// inv.Reserved[7] == 2
// store.Orders["ord-1"] == order
// store.Events["evt-ord-1"].Topic == "order.created"
```

若设置 `store.FailOutbox = true`，函数返回 `ErrOutboxWrite`，订单和事件都不存在，并且库存恢复为 available=5、reserved=0。

## 约束与提示

- `eventID` 和订单号去除首尾空白后参与校验和存储。
- 幂等判断必须发生在库存预占前。
- 不要在事务外写 `store.Orders` 或 `store.Events`。
- 补偿失败意味着库存仍处于预占状态，调用方需要同时识别原始失败和补偿失败，以便告警与巡检。

## 本地运行

```bash
go test -tags exercise ./exercises/09-cart-to-order/09.02-create-order-saga/problem
```
