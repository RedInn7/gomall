# 自动评测说明（教师版）

## 发布前检查

```bash
./scripts/grade-exercises.sh compile
./scripts/grade-exercises.sh solution
```

`compile` 检查所有题目与参考答案能够编译；`solution` 运行参考答案测试。学生 starter 中保留 `TODO`，所以发布前不要求 `student` 模式通过。

## 学生本地评测

学生完成当前作业后运行对应目录的 `go test`。全部章节完成后可以运行：

```bash
./scripts/grade-exercises.sh student
```

## 正式评测

正式评测沿用公开测试的 package，不修改学生函数签名。评测环境将额外测试文件放入对应 `problem/` package 后运行 `go test -tags exercise`。

隐藏测试重点检查：

| 题目 | 隐藏测试重点 |
| --- | --- |
| 02.01 | 任意 ID、同账户去重、返回顺序 |
| 02.02 | 负数、失败后重试、业务键隔离、内部切片隔离 |
| 02.03 | 订单不存在、状态竞争、Outbox 回滚、原数据不变 |
| 03.01 | 空 key、重复完成、成功响应不可覆盖 |
| 03.02 | 回放优先级、失败不缓存、错误分类、调用次数 |
| 03.03 | 多订单混合、问题优先级、方向重复、稳定排序 |

评测时应拒绝学生修改 `*_test.go`、build tag、函数签名和预置错误变量。分数应以行为测试为准，不根据代码文本是否还包含 `TODO` 判断。
