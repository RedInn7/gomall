# 讲义 12：库存与防超卖

这组练习用内存模型复现 Redis Lua 守住的库存约束：`Available`、`Reserved`、`Sold` 三个桶在并发下不能为负、总量守恒，同一个 `reservationID` 的预占、提交和释放都要幂等。

| 作业 | 主要问题 | 题目 |
| --- | --- | --- |
| 12.01 原子库存预占 | 并发抢最后一件、请求重复到达时怎样不超卖也不多退 | [查看题目](./12.01-atomic-reservation/problem/readme.md) |

运行学生版本，并发题需要通过竞态检测：

```bash
go test -race -tags exercise ./exercises/12-inventory/12.01-atomic-reservation/problem
```

维护参考实现时运行：

```bash
go test -race -tags exercise ./exercises/12-inventory/12.01-atomic-reservation/solution
```

每题只修改 `problem` 目录中带 TODO 的实现文件，不要修改测试、函数签名和错误变量。
