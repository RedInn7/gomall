# 02 支付（上）Labs

配套讲义：`docs/lecture/02-payment-up.md`。三题沿着一笔余额支付的数据库事务展开：

1. `02.01-ordered-account-locks`：为买家和卖家生成稳定的加锁顺序，避免相向支付死锁。
2. `02.02-double-entry-ledger`：同时落 debit / credit 两条资金流水，并用业务键吸收重复请求。
3. `02.03-payment-finalize`：用状态条件更新订单，并把订单状态与 Outbox 事件放进同一事务。

运行学生版本：

```bash
go test -tags exercise ./exercises/02-payment-up/.../problem
```

公开测试只覆盖主要路径；正式评测还会检查相同账户、零金额、重复流水、状态竞争和事务回滚。
