# 05.02：卖家余额、双边流水和状态必须一起提交

## 故障现场

平台先给卖家加了余额，随后托管流水写入失败。任务重试时清算单仍是 `cleared`，卖家再次到账。财务看到的钱包余额、账本与清算状态三者互相矛盾。

## 任务

完成 `Store.Settle`，模拟一个数据库事务，将托管资金原子地转入卖家钱包。

## 业务规则

1. 仅 `cleared` 可首次结算；`settled` 重试直接成功且不新增流水。
2. 托管余额不足、金额非正、卖家不存在或金额溢出时必须拒绝。
3. 托管账户 debit、卖家钱包 credit，两条金额必须相同。
4. 卖家流水的 `BalanceAfterCents` 必须等于实际新余额。
5. 两边余额、两条流水和清算状态必须作为一个整体提交。
6. `FailAfterDebit` 用来模拟半程故障；失败后任何可观察状态都不能变化。

## 本地评测

```bash
go test -tags exercise ./exercises/05-payment-settlement/05.02-atomic-seller-settlement/problem
```
