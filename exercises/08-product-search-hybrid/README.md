# 第八讲：商品搜索（二）

这组练习对应 Hybrid Search 的核心结果处理：Elasticsearch 关键词召回与 Milvus 向量召回使用不同的分数方向和量纲，必须先归一化、再按商品 ID 融合，最后稳定地返回 TopK。练习也要求在单路故障时继续使用健康链路。

1. `08.01-hybrid-fusion`：两路去重、归一化、融合、降级和 TopK。

运行学生题：

```bash
go test -tags exercise ./exercises/08-product-search-hybrid/08.01-hybrid-fusion/problem
```

维护参考实现时运行：

```bash
go test -tags exercise ./exercises/08-product-search-hybrid/08.01-hybrid-fusion/solution
```
