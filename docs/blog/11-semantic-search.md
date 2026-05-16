# 11 - 语义检索：ES 关键词 + Milvus 向量的加权融合

## 背景

电商搜索里只有 BM25 关键词召回会漏掉同义词、口语化查询和长尾意图。比如用户搜「适合通勤的笔记本」，BM25 会去匹配字面上的 "通勤"、"笔记本"，无法理解「轻薄、续航长」这类语义。Milvus 向量召回正好补这一块：把 query 和商品文本都过 embedding 模型，按向量相似度找最近邻。

但单独用向量也有问题：embedding 模型对品牌名、SKU、型号这类强字面信息不敏感，"iPhone 15 Pro Max" 走 ANN 大概率被 "iPhone 15"、"iPhone 14 Pro" 干扰；而 ES `multi_match` 会精准命中。所以生产里通常是 **两路召回 + 融合排序**。

## 接口

`POST /api/v1/product/semantic-search`

```json
{
  "query": "适合通勤的笔记本",
  "top_k": 10,
  "category_id": 3
}
```

返回每条命中带 `score` / `semantic_score` / `keyword_score`，前端可以选择是否暴露子分数用于 debug。

## 实现拆解

代码全部在 `service/search/` 下：

- `embedding.go` - `EmbedText` 抽象 embedding 服务，env `EMBEDDING_API_URL` 控制走线上 (OpenAI / 自建 BGE / Ollama)，未配置时走 SHA-256 衍生的 768 维占位向量，保证本地与单测可跑。
- `milvus_stub.go` - `MilvusSearcher` interface + 全局 `Searcher` 变量 + `SetSearcher` setter。默认 `nopMilvusSearcher` 返回空，等真实现接入后调 `search.SetSearcher(client)` 注入。
- `semantic.go` - 编排 embedding -> 向量召回 -> ES 关键词召回 -> 融合 -> DB 取详情 -> 排序截断。

### 融合公式

```
fused = 0.5 * normalize(vec_score) + 0.5 * normalize(es_score)
```

50/50 是默认权重，作为**业务调优入口**。线上一般有两种调法：

1. 离线 NDCG / MRR 调参：用人工标注或点击日志做评估集，网格搜 `(α, 1-α)`。
2. 在线分桶：把权重做成配置项，按 UV 切量 AB。

之所以选 50/50 作为出厂值：
- 没有先验数据时给两路同等信任度，避免一上线就偏。
- 工程上，向量召回 recall 高、precision 低；ES 反之。等权融合可以让两边互补，先保 recall，再用排序模型 (`learning to rank`) 精排——不过精排是后续 deck 的事。

### Normalization 为什么用 min-max

两路分数量纲完全不同：

- ES 的 `_score` 是 BM25/TF-IDF 加权和，绝对值受 query 词长、文档长度影响，几十到几百都常见。
- Milvus 向量分数取决于度量方式 (cosine / IP / L2)，cosine 是 [-1, 1]，L2 是 0 到 +∞ 反过来。

直接加权会被大量纲压死。两种主流归一化方式：

1. **Min-max**：`(x - min) / (max - min)`，把当前 batch 压到 [0, 1]。简单，对 outlier 敏感。
2. **Z-score**：`(x - mean) / std`，对分布敏感但需要更多样本。

线上我们选 min-max，因为：
- 单次 query 召回量通常 30~100 条，min-max 在小样本下稳定。
- 业务关心相对序，不关心绝对值是否服从正态。

代码：`minMaxNormalize` 在 `service/search/semantic.go`，对空集合返回 nil、对全等集合返回全 1（视为 tie，让另一路决定排序）。

### Embedding 模型抽象

`EmbedText` 不绑死任何具体模型：

- `EMBEDDING_API_URL` 未设 -> 占位向量。CI / 本地起服务不依赖外部。
- 设了 -> POST `{model, input}`，兼容 OpenAI `data[0].embedding` 和 BGE/Ollama 的 `embedding` 两种响应结构。
- 超时 5s，避免拖垮检索 P99。
- `EMBEDDING_MODEL` 切模型，`EMBEDDING_API_KEY` 走 Bearer auth。

切模型只改 env，不动代码，符合 12-factor。

## 降级策略

- ES 不可用：向量召回继续，融合时关键词分数全为 0，相当于纯向量召回。
- Milvus 不可用 (`nopMilvusSearcher` 或网络挂)：返回空 hits，融合时只有 ES 一路有分。
- Embedding 服务不可用：直接报错给前端，因为整个 pipeline 断了。这里不再降级到 ES，避免静默语义失效。

## 关于和 Unit 6 的协作

仓库另一条线 (Unit 6) 在实现 `repository/milvus/` 真实客户端。本 PR 故意只定义 `MilvusSearcher` interface，**不引用** `repository/milvus`，让两边 PR 互不阻塞。Unit 6 合并后，只需要在 `initialize/` 里加一行：

```go
search.SetSearcher(milvus.NewClient(...))
```

整个语义检索链路就被点亮。
