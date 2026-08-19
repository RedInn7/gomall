# 第五讲：支付清算

这一讲的作业把课堂上的 `RecordClearedTx` 收缩成一个内存模型，让你专注练习清算领域规则，不需要启动 MySQL、Redis、Stripe 或链上节点。

| 作业 | 核心能力 | 入口 |
| --- | --- | --- |
| 04.01 清算复式记账 | 渠道分流、金额口径、一借一贷、防重复入账、原子回滚 | [problem/readme.md](./04.01-clearing-ledger/problem/readme.md) |

运行学生版本：

```bash
go test -tags exercise ./exercises/04-payment-clearing/04.01-clearing-ledger/problem
```

验证参考答案：

```bash
go test -tags exercise ./exercises/04-payment-clearing/04.01-clearing-ledger/solution
```
