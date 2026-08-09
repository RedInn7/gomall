# 支付对账扫描

## 故障现场

凌晨的支付链路看起来已经恢复，但客服仍收到三类投诉：有人订单显示已支付却查不到流水；有人账上扣了两次；还有订单写的是 Stripe，流水却来自余额账户。单看 HTTP 日志无法判断钱是否真的对。

数据库中的已支付订单是本次扫描的基准。每个订单应该恰好对应一条 debit 和一条 credit，两条金额都等于订单实付金额，渠道也必须与订单一致。

## 任务

补全 `Reconcile`，为异常订单返回一条 `Issue`。问题代码定义如下：

- `missing_entries`：不是恰好两条流水；
- `unbalanced`：debit / credit 不成对，或者金额与订单不一致；
- `channel_mismatch`：流水渠道与订单渠道不同。

同一订单同时命中多个问题时，优先级为：缺流水 → 不平账 → 渠道不符。结果必须按 `OrderID` 升序，便于每日扫描结果稳定比较。

## 样例

```text
订单 1: paid, balance, 1000 cents
流水  : debit 1000 + credit 1000, balance
结果  : 无异常

订单 2: paid, stripe, 2000 cents
流水  : 只有 debit 2000
结果  : {OrderID: 2, Code: "missing_entries"}
```

## 评测

```bash
go test -tags exercise ./exercises/03-payment-down/03.03-payment-reconciliation/problem
```
