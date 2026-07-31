# 00 业务总览 Labs

配套讲义：

- `docs/lecture/00-overview.md`
- `docs/lecture/00-overview-architecture.md`

第一次做实验请先阅读[代码实验使用说明](../README.md)，里面写了作答目录、运行命令和提交要求。

形式参考 CMU 15-445：每题的 `problem/` 是学生代码，函数签名和公开测试已经给出；学生只补 `TODO`。`solution/` 保存参考实现。评分时还应加入隐藏测试，避免只针对公开样例写死答案。

练习使用 `exercise` build tag，不影响项目默认测试：

```bash
go test -tags exercise ./exercises/00-overview/00.01-authoritative-order/problem
go test -tags exercise ./exercises/00-overview/00.02-inventory-buckets/problem
go test -tags exercise ./exercises/00-overview/00.03-transactional-outbox/problem
```

## 题目顺序

1. `00.01-authoritative-order`：客户端只提交购买意图，身份、价格和卖家必须来自服务端权威数据。
2. `00.02-inventory-buckets`：实现 `available / reserved` 的预扣、提交与释放。
3. `00.03-transactional-outbox`：订单与 Outbox 事件必须同事务提交，失败时一起回滚。
