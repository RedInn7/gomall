# 第五讲：支付结算

支付成功后，钱先停在 `merchant_escrow`。只有订单完成，结算模块才能增加卖家真实余额，同时写入托管账户 debit、卖家钱包 credit，并把清算状态从 `cleared` 推进到 `settled`。

练习覆盖重复消息、流水写入失败、状态更新失败、退款先完成和历史账目不一致等情况。

| 作业 | 主要问题 | 题目 |
| --- | --- | --- |
| 05.01 结算状态守卫 | 当前订单和资金状态是否允许放款 | [查看题目](./05.01-settlement-state-guard/problem/readme.md) |
| 05.02 原子卖家结算 | 余额、双边流水和状态怎样一起提交 | [查看题目](./05.02-atomic-seller-settlement/problem/readme.md) |
| 05.03 并发幂等结算 | 大量重复事件怎样保证只入账一次 | [查看题目](./05.03-concurrent-idempotent-settlement/problem/readme.md) |
| 05.04 退款与结算竞争 | 同一笔托管资金最终应该流向谁 | [查看题目](./05.04-refund-settlement-race/problem/readme.md) |
| 05.05 结算对账 | 怎样从订单、清算单、流水和余额中找出坏账 | [查看题目](./05.05-settlement-reconciliation/problem/readme.md) |

运行全部学生版本：

```bash
go test -tags exercise ./exercises/05-payment-settlement/.../problem
```

并发题还需要通过竞态检测：

```bash
go test -race -tags exercise ./exercises/05-payment-settlement/05.03-concurrent-idempotent-settlement/problem ./exercises/05-payment-settlement/05.04-refund-settlement-race/problem
```

每题只修改 `problem` 目录中带 TODO 的实现文件，不要修改测试、函数签名和错误变量。
