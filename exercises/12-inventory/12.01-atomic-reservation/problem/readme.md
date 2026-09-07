# 12.01：原子库存预占与幂等状态流转

## 题目背景

某件限量商品只剩最后 1 件，小王和小李同时点击购买。如果服务先查询“还有 1 件”，再分别扣减库存，两个人都可能下单成功，最终卖出 2 件并产生超卖。

下单请求还可能因页面转圈、网络超时或消息重投而重复到达。一次预占如果被执行两遍，会重复扣减可售库存；一次取消如果被执行两遍，又会凭空增加库存。因此，库存操作既要原子执行，也要根据同一个 `reservationID` 保证幂等。

本题使用一个内存版 `Inventory` 模拟 Redis Lua 负责的关键约束。库存分为三个桶：

- `Available`：仍可出售的数量；
- `Reserved`：订单已经占住、但尚未完成支付的数量；
- `Sold`：支付完成、已经售出的数量。

每次成功操作都必须保持：

```text
Available >= 0
Reserved  >= 0
Sold      >= 0

Available + Reserved + Sold = 初始库存
```

## 题目描述

完成 `Inventory` 的三个方法：

```go
func (i *Inventory) Reserve(reservationID string, qty int) error
func (i *Inventory) Commit(reservationID string) error
func (i *Inventory) Release(reservationID string) error
```

`Reserve` 把库存从 `Available` 移到 `Reserved`；`Commit` 把对应预占从 `Reserved` 移到 `Sold`；`Release` 把对应预占从 `Reserved` 退回 `Available`。

状态流转如下：

```text
不存在 --Reserve--> Reserved --Commit--> Committed
                              \
                               --Release--> Released
```

`Committed` 和 `Released` 都是终态。同一个终态操作可以安全重试，但两个终态不能互相转换。

## 已有接口与数据

`NewInventory(available)` 创建指定初始可售库存的实例。每个成功的 `Reserve` 会写入：

```go
Reservation{
    Quantity: qty,
    Status:   StatusReserved,
}
```

`Snapshot()` 返回当前三个库存桶和 reservation 记录的副本，可用于检查最终状态。

题目已经给出以下错误：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidQuantity` | ID 为空，或 `qty <= 0` |
| `ErrInsufficientStock` | 可售库存不足 |
| `ErrUnknownReservation` | Commit/Release 的 ID 从未成功预占 |
| `ErrReservationConflict` | 相同 ID 再次 Reserve，但数量不同 |
| `ErrAlreadyFinalized` | 已 Commit 后 Release，或已 Release 后 Commit |

## 完成条件

### Reserve

1. `reservationID` 为空或 `qty <= 0` 时返回 `ErrInvalidQuantity`，状态不变。
2. 第一次成功预占时原子执行 `Available -= qty`、`Reserved += qty` 并保存记录。
3. 库存不足时返回 `ErrInsufficientStock`，不能修改库存，也不能留下记录。
4. 相同 ID、相同数量重复调用时返回 `nil`，不能再次扣库存。
5. 相同 ID、不同数量重复调用时返回 `ErrReservationConflict`，状态不变。

### Commit

1. 只允许 `Reserved -> Committed`。
2. 成功时执行 `Reserved -= qty`、`Sold += qty`，`Available` 不变。
3. 重复 Commit 返回 `nil`，不能重复增加 `Sold`。
4. 未知 ID 返回 `ErrUnknownReservation`；已 Release 的 ID 返回 `ErrAlreadyFinalized`。

### Release

1. 只允许 `Reserved -> Released`。
2. 成功时执行 `Reserved -= qty`、`Available += qty`，`Sold` 不变。
3. 重复 Release 返回 `nil`，不能重复增加 `Available`。
4. 未知 ID 返回 `ErrUnknownReservation`；已 Commit 的 ID 返回 `ErrAlreadyFinalized`。

### 并发要求

状态检查、库存检查、库存移动和 reservation 状态更新必须位于同一临界区。测试会并发执行以下场景：

- 64 个不同买家争抢最后 1 件商品，只能有 1 个成功；
- 同一个 Reserve 请求并发重试 64 次，只能扣减一次；
- 同一个 Commit 或 Release 并发重试 64 次，只能完成一次状态变化；
- `Snapshot` 与写操作之间不能产生数据竞争。

## 调用样例

初始状态：

```text
Available = 5
Reserved  = 0
Sold      = 0
```

执行：

```go
inv := NewInventory(5)

_ = inv.Reserve("order-1001", 2)
// Available=3, Reserved=2, Sold=0

_ = inv.Reserve("order-1001", 2)
// 幂等重试：Available=3, Reserved=2, Sold=0

_ = inv.Commit("order-1001")
// Available=3, Reserved=0, Sold=2

_ = inv.Commit("order-1001")
// 幂等重试：Available=3, Reserved=0, Sold=2
```

取消路径示例：

```go
_ = inv.Reserve("order-1002", 1)
// Available=2, Reserved=1, Sold=2

_ = inv.Release("order-1002")
// Available=3, Reserved=0, Sold=2
```

如果 `order-1002` 已经 Release，再调用 `Commit("order-1002")`，应返回 `ErrAlreadyFinalized`，库存保持不变。

## 约束与提示

- 不要把锁只放在单个 map 读写旁边；“读取旧状态—检查—修改库存—推进状态”必须作为一个整体同步。
- 所有失败路径都要保持三个库存桶及 reservation 记录不变。
- 幂等判断要先于库存不足判断。同一个已经成功的 Reserve 重试时，即使当前 `Available` 已不足，也应返回成功。
- `Snapshot` 必须在同步保护下读取数据，并返回 map 副本。
- 不需要连接 MySQL、Redis 或 RabbitMQ。

## 本地运行

```bash
go test -race -tags exercise ./exercises/12-inventory/12.01-atomic-reservation/problem
```
