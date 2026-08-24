# 复式资金流水

## 背景

一位买家支付了 12.50 美元。页面显示支付成功，买家余额也减少了，但卖家声称没有收到钱。客服查数据库时只看到一个最新余额，看不出这 12.50 美元去了哪里，也无法判断系统当时是少记了一笔还是后来有人改过余额。

GoMall 因此不把资金流水当普通日志。一次余额支付必须留下两条可以互相核对的记录：

```text
买家账户  debit   1250 cents
卖家账户  credit  1250 cents
```

两边金额必须相等，并共享同一个资金业务键。

## 需要实现

补全：

```go
func (l *Ledger) PostPayment(bizKey string, buyerID, sellerID uint, cents int64) error
```

`bizKey` 可以理解为 `order_pay:42`。它代表一笔唯一的资金事实，同一个键再次到达时不能重复入账。

## 实现要求

1. `cents <= 0` 返回 `ErrInvalidAmount`；
2. 买家和卖家相同返回 `ErrSameAccount`；
3. 已处理过的 `bizKey` 返回 `ErrDuplicate`；
4. 成功时先写 buyer 的 `debit`，再写 seller 的 `credit`；
5. 两条流水的 `BizKey` 和 `Cents` 必须完全相同；
6. 任何失败都不能新增流水，也不能把失败的 `bizKey` 标记为已处理。

## 样例

```text
PostPayment("order:42", 7, 9, 1250)

entries:
  {order:42, 7, debit,  1250}
  {order:42, 9, credit, 1250}
```

第二次执行同一调用返回 `ErrDuplicate`，流水数量仍然是 2。

## 运行测试

```bash
go test -tags exercise ./exercises/02-payment-up/02.02-double-entry-ledger/problem
```
