# 原子完成支付

## 故障现场

一笔钱已经从买家转给卖家，支付服务接下来需要把订单改成 `paid`，再通知库存、履约和客服系统。某次发布后，订单更新成功，但消息发送前进程崩溃。数据库里订单显示已支付，下游却永远没有收到通知。

把消息直接发在事务里也不安全：消息可能先发出去，事务随后回滚，下游处理的就成了一个不存在的支付事实。

GoMall 使用 Transactional Outbox：订单状态和“等待发布的事件”一起写入数据库事务，后台发布器再发送事件。

## 任务

补全：

```go
func FinalizePayment(db *DB, orderID uint, channel, eventID string) error
```

这里的 `DB` 是为了练习而缩小的内存数据库。请在事务副本中完成全部操作，全部成功后再替换正式数据。

## 业务规则

1. 找不到订单返回 `ErrOrderNotFound`；
2. 订单只有处于 `wait_pay` 时才能结算，否则返回 `ErrStateChanged`；
3. 成功后订单状态变成 `paid`，并记录实际支付渠道；
4. 同一事务写入 topic 为 `payment.succeeded` 的 Outbox 事件；
5. Outbox 故障返回 `ErrOutbox`，订单必须保持原状；
6. 状态竞争失败时，不得覆盖原有渠道，也不得留下事件。

## 样例

```text
before: order 8 = {state: wait_pay, channel: ""}
call:   FinalizePayment(db, 8, "balance", "evt-8")
after:  order 8 = {state: paid, channel: "balance"}
        evt-8   = {topic: payment.succeeded, order_id: 8}
```

如果写 Outbox 失败，`after` 必须与 `before` 完全相同。

## 本地评测

```bash
go test -tags exercise ./exercises/02-payment-up/02.03-payment-finalize/problem
```
