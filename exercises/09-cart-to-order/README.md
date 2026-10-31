# 讲义 09：购物车到订单

这组练习对应提交订单的两步：先只相信认证用户和服务端的商品、地址数据，构造结算快照；再用 Saga 串起库存预占、订单与 Outbox 原子写入，以及订单事务失败后的库存补偿。

| 作业 | 主要问题 | 题目 |
| --- | --- | --- |
| 09.01 服务端权威结算 | 哪些字段可以来自客户端，价格、卖家和买家以谁为准 | [查看题目](./09.01-authoritative-checkout/problem/readme.md) |
| 09.02 下单 Saga | 库存已预占、订单事务失败时怎样补偿，重复请求怎样幂等 | [查看题目](./09.02-create-order-saga/problem/readme.md) |

运行学生版本：

```bash
go test -tags exercise ./exercises/09-cart-to-order/09.01-authoritative-checkout/problem
go test -tags exercise ./exercises/09-cart-to-order/09.02-create-order-saga/problem
```

维护参考实现时运行：

```bash
go test -tags exercise ./exercises/09-cart-to-order/.../solution
```

每题只修改 `problem` 目录中带 TODO 的实现文件，不要修改测试、函数签名和错误变量。
