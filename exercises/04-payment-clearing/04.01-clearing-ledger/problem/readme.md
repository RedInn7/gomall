# 04.01：把“支付成功”记成一笔平衡的清算账

## 背景

Wallet 的钱来自站内余额，Stripe/Web3 的钱来自外部系统，但三种渠道最终都要形成同一种业务结果：订单已经实收，资金进入卖家待结算托管账户。若只写清算单、漏写账本，或者只写借方、漏写贷方，系统就会出现资损和对账差异。

## 需要实现

完成 `RecordClearedTx`：在一个原子操作中写入清算凭证，并生成金额相等的一借一贷两条分录。

```go
func RecordClearedTx(
    store *Store,
    o *Order,
    channel, providerRef, currency string,
    walletBalanceAfter *int64,
) error
```

## 实现要求

1. Wallet 借记买家的 `user_wallet`，并保存扣款后余额。
2. Stripe/Web3 借记系统账户 `external_clearing`，不需要买家余额。
3. 三种渠道都贷记 `merchant_escrow`；清算时不能直接增加卖家钱包。
4. 普通订单应付金额为 `Money * Num`；促销订单以 `FinalCents` 为准。
5. 当前手续费为零，因此清算凭证满足 `FeeCents = 0`、`NetCents = GrossCents`。
6. 两条分录必须金额相等、方向相反、订单与业务类型相同。
7. 币种去空格并转成大写；外部凭证去除首尾空格。
8. 同一订单只能生成一次清算记录；这里练习的是唯一约束兜底，不等同于外部回调的完整幂等状态机。
9. 任一步失败必须整体回滚，不能留下清算单或单边账。
10. 为贴合当前生产契约，零元订单允许清算；负数金额必须拒绝。

## 测试场景

| # | 场景 | 预期 |
| ---: | --- | --- |
| 1 | Wallet 清算 | 买家钱包 debit，托管账户 credit，并记录余额快照 |
| 2 | Stripe 清算 | external_clearing debit，托管账户 credit |
| 3 | Web3 清算 | external_clearing debit，链上凭证被保存 |
| 4 | Wallet 缺少扣款后余额 | 拒绝且零写入 |
| 5 | Stripe 错带钱包余额 | 拒绝且零写入 |
| 6 | Web3 错带钱包余额 | 拒绝且零写入 |
| 7 | 未知渠道 | 拒绝且零写入 |
| 8 | 空币种 | 拒绝且零写入 |
| 9 | 币种与 provider ref 标准化 | 去空格，币种转大写 |
| 10 | 普通订单 | 金额使用 `Money * Num` |
| 11 | 促销订单 | 金额使用 `FinalCents`，不再乘数量 |
| 12 | 非法订单字段 | 拒绝且零写入 |
| 13 | 负数应付金额 | 拒绝且零写入 |
| 14 | 零元订单 | 允许生成平衡分录 |
| 15 | 同一订单重复清算 | 返回重复错误，不新增记录 |
| 16 | merchant_escrow 写入失败 | 清算单和 debit 一起回滚 |

## 运行测试

```bash
go test -tags exercise ./exercises/04-payment-clearing/04.01-clearing-ledger/problem
```
