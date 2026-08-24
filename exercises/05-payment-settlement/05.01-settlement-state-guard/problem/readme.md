# 05.01：结算状态判断

## 背景

订单服务会重复投递“订单已完成”事件，退款任务也可能同时运行。若结算入口只检查订单完成，不检查清算终态，卖家可能重复到账；若先看订单、后忽略资金状态，已退款资金也可能再次放给卖家。

## 需要实现

完成 `DecideSettlement`。它不修改数据，只根据订单与清算记录给出 `settle`、`noop` 或明确错误。

## 实现要求

1. 订单 ID 必须有效，且清算单必须存在并属于同一订单。
2. `settled` 与 `refunded` 都是清算终态，重复调用必须幂等返回 `noop`。
3. 只有 `cleared` 可以继续判断订单状态。
4. 退款中或已退款的订单返回 `noop`，不能进入卖家结算。
5. 只有 `completed + cleared` 返回 `settle`；未完成订单必须返回错误。
6. 判断顺序本身就是契约：清算终态优先于订单状态，保证重复事件不会被误报为失败。

## 运行测试

```bash
go test -tags exercise ./exercises/05-payment-settlement/05.01-settlement-state-guard/problem
```
