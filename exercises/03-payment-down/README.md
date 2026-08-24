# 03 支付（下）Labs

配套讲义：`docs/lecture/03-payment-down.md`。三题处理支付已经进入重试、熔断和对账阶段后的问题：

1. `03.01-idempotency-state-machine`：实现 `init / processing / done` 状态机。
2. `03.02-replay-before-breaker`：保证已完成请求在熔断器 Open 时仍能回放，并区分业务失败与系统失败。
3. `03.03-payment-reconciliation`：用订单事实核对渠道流水，找出缺失、金额不平和渠道不一致。

运行学生版本：

```bash
go test -tags exercise ./exercises/03-payment-down/.../problem
```

实现还需正确处理空 key、重复完成、失败响应、熔断状态和多订单混合。
