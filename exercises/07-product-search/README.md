# 第七讲：商品搜索

这组练习对应商品搜索的两条核心链路：读请求先清洗成稳定的 Elasticsearch 查询计划；商品写入则通过 Transactional Outbox 保证 MySQL 事实与索引更新事件一起落地。

## 练习顺序

1. `07.01-search-query-plan`：关键词优先级、分页规范化，以及类目和在售状态的结构化过滤。
2. `07.02-index-outbox`：商品与索引事件的原子提交、事件幂等和商品版本冲突。

每道题先阅读 `problem/readme.md` 的背景、契约、样例和约束，再补同目录 Go 文件中的 TODO。

```bash
go test -tags exercise ./exercises/07-product-search/07.01-search-query-plan/problem
go test -tags exercise ./exercises/07-product-search/07.02-index-outbox/problem
```

维护参考实现时运行：

```bash
go test -tags exercise ./exercises/07-product-search/.../solution
```
