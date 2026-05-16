# 向量数据库：当用户搜"苹果手机"时，ES 救不了你

## 一个真实场景

电商 App 搜索框。用户输入：

> "便宜耐用的水果机"

你的 ES 召回了什么？大概率**零结果**。

因为 ES 是关键词倒排索引。**"水果机" 不在任何商品标题里，所以匹配不上**。

但是站在用户角度，TA 想要的是 iPhone 入门款。你能给 TA 一个空白页吗？

这就是**语义搜索**要解决的问题，也是为什么 gomall 在 ES 之上再加一层 Milvus。

## 关键词召回 vs 语义召回

ES 现在的工作（参见 `repository/es/product_index.go`）：

```go
"query": {
    "multi_match": {
        "query":  "便宜耐用的水果机",
        "fields": []string{"name^3", "title^2", "info"},
    },
},
```

它在做什么：

1. 把 query 切词："便宜 / 耐用 / 的 / 水果 / 机"
2. 倒排索引里查每个词，求并集
3. 按 BM25 给文档打分

**它的边界**：

| 用户输入       | ES 行为                   |
|----------------|---------------------------|
| "苹果手机"     | OK，"苹果"+"手机" 命中 |
| "水果机"       | miss，"水果机" 不在词典 |
| "iphome"       | miss，错别字 |
| "可以打电话的设备" | miss，纯描述，没有关键词重叠 |

**根本原因**：ES 衡量的是"**词的重合度**"，不是"**意思的接近度**"。

## 语义召回怎么做

把"文本"和"商品标题"都映射成同一个向量空间里的点：

```
"便宜耐用的水果机"   →   [0.12, -0.34, 0.78, ..., 0.05]   (768 维)
"iPhone 13 国行"     →   [0.15, -0.31, 0.81, ..., 0.07]   (768 维)
```

两个向量在 L2 距离上很近 → 命中。

**做这件事的引擎，就是嵌入模型（embedding model）**。BGE、OpenAI text-embedding-3、Cohere embed、bce-embedding-base 等等。

但模型只产生向量。**怎么在亿级向量里 ms 级别找最近邻？** 这才是向量数据库（Milvus / Qdrant / Pinecone）的工作。

## product_vector schema 设计

gomall 用 Milvus，collection 叫 `product_vector`：

```
field          type            note
-------------- --------------- ----------------------------------------
id             Int64 PK        = product.ID，直接复用业务主键
vector         FloatVector     dim=768
category_id    Int64           标量过滤字段
```

### 为什么 dim=768

不是拍脑袋。是因为：

- BGE-base-zh-v1.5：768 dim
- text-embedding-ada-002（OpenAI 老款）：1536 dim
- text-embedding-3-small：1536 dim（可降维到 768）
- bce-embedding-base_v1：768 dim

**主流中文 base 模型默认就是 768**。如果选 large 系列（BGE-large 等）就是 1024 dim。

dim 越大：召回质量上限越高、但索引内存和算力成本线性涨。**768 是质量/成本曲线的甜点**，绝大多数业务场景够用。

### 为什么不复用 ES 文档

复用不了。ES 文档存的是结构化字段（name、price、on_sale...），是给关键词检索用的。

向量是**几百维的浮点数**，强行塞进 ES 你能存但搜不动——ES 没有 HNSW 这种 ANN 索引。即使 7.x 后 ES 加了 dense_vector，性能也远不如专用向量库。

**职责分离**：
- ES：关键词召回 + 结构化过滤
- Milvus：语义召回
- 上层服务做**双路融合**（reciprocal rank fusion / RRF）

## HNSW 索引：M=16, efConstruction=200, efSearch=64

向量搜索本质是 **kNN（k 近邻）**。穷举法 O(N) 在亿级数据上就是死路。

HNSW（Hierarchical Navigable Small World）是当前最主流的近似最近邻索引。简单理解：**多层的"小世界"图**，上层稀疏跳跃式定位、下层稠密精搜。

三个核心参数：

| 参数            | 我们的值 | 含义                                    | 调小 vs 调大            |
|-----------------|----------|-----------------------------------------|-------------------------|
| M               | 16       | 每个节点的图边数                         | 内存↓召回↓ vs 内存↑召回↑ |
| efConstruction  | 200      | 建索引时的候选池大小                     | 建索引快 vs 索引质量好  |
| efSearch        | 64       | 查询时的候选池大小（决定召回率/延迟）    | 快但召回低 vs 慢但召回高 |

**为什么选 16 / 200 / 64**：

- `M=16`：Milvus 官方推荐的中等配置。`M=8` 内存最省但召回明显掉，`M=32` 内存翻倍但召回提升边际很小
- `efConstruction=200`：建一次受益长期，给到 200 让索引质量到位
- `efSearch=64`：在电商搜索场景下 p99 延迟可控（<50ms），召回率通常能到 95%+

**调参经验法则**：
- 召回不达标 → 先调 efSearch（**查询时参数**，无需重建索引）
- efSearch 拉到 256+ 还不行 → 重建索引时调大 M
- 建索引慢得不能忍 → 调小 efConstruction

`efSearch` 是**查询级**参数，可以按业务 SLA 动态切。比如"首页热门搜"用 efSearch=128 求召回率，"搜索建议下拉"用 efSearch=32 求 p99 延迟。

## 静默降级：MILVUS_ADDR 未设跳过启动

gomall 启动流程参考 `cmd/main.go`：

```go
tryInitES(context.Background())
tryInitMilvus(context.Background())
```

`tryInitMilvus` 内部：

```go
func tryInitMilvus(ctx context.Context) {
    defer func() {
        if r := recover(); r != nil {
            util.LogrusObj.Warnf("Milvus 初始化失败，语义召回能力关闭: %v", r)
        }
    }()
    if err := milvus.InitMilvus(); err != nil {
        util.LogrusObj.Warnf("Milvus 客户端连接失败，语义召回能力关闭: %v", err)
        return
    }
    initialize.InitMilvusCollection(ctx)
}
```

`milvus.InitMilvus()`：

```go
func InitMilvus() error {
    addr := os.Getenv("MILVUS_ADDR")
    if addr == "" {
        return nil   // 静默不启动
    }
    c, err := client.NewClient(...)
    ...
}
```

**为什么静默降级？**

- 本地开发：80% 的时间你不在调向量搜索。一个 Milvus 容器吃 2G 内存，没必要每个开发者都拉
- CI：单测应该跑得快，禁用所有外部依赖
- 生产：Milvus 集群挂了，**关键词搜索应该继续工作**，不能让向量库变成全站搜索的单点

降级策略：MILVUS_ADDR 未设 → client 保持 nil → 调用层 if check → 走纯 ES 路径。

跟 `tryInitES` 一个模板，gomall 所有外部依赖（RMQ、ES、Milvus）都按这个套路。

## 增量更新：靠 Outbox 事件

商品上下架、改标题、改图——Milvus 里的向量必须跟着更新。

复用 ES indexer 一样的链路（参考 `service/search/indexer.go`）：

```
service.ProductUpdate(...)
    └→ DB UPDATE
    └→ outbox 写入 `product.changed` 事件 (同事务)
        └→ outbox publisher → RMQ exchange
            ├→ ES indexer (existing)
            └→ Milvus indexer (Unit 7 接管)
                └→ 取 product.title → embedding 模型 → vector
                └→ milvus.UpsertProductVector(id, vec, category_id)
```

**为什么不是同步写**：

1. embedding 模型调用慢（50~500ms / 次）。挂在写路径上拖死接口
2. embedding 模型挂了不能让商品保存失败——业务可用性 > 搜索新鲜度

**为什么靠 outbox**：

- 业务事务和事件发布**同一个 DB 事务**写入，零数据丢失
- 消费者幂等：Upsert 天然幂等，重放零副作用
- 异步隔离故障：Milvus / embedding 服务任一挂了，业务写入仍 OK

这套架构上 Outbox 我们前面就讲过（`gomall #57`），这里 Milvus indexer 只是**多挂一个消费者**——这就是事件驱动架构最爽的地方。

## 查询融合：双路召回怎么合并

`SearchProductVector` 返回 `[]ProductSearchHit{ProductID, Score}`。但上层用户接口要的是商品 doc，而且要**结合 ES 的关键词召回**。

最简实现：**RRF（reciprocal rank fusion）**

```go
// 伪代码
esHits := es.SearchProducts(ctx, kw, 0, 100)        // 关键词召回 100 条
vecHits := milvus.SearchProductVector(ctx, vec, 100, nil)  // 语义召回 100 条

scores := map[uint]float64{}
for rank, h := range esHits {
    scores[h.ID] += 1.0 / float64(rank + 60)
}
for rank, h := range vecHits {
    scores[h.ProductID] += 1.0 / float64(rank + 60)
}

// 按 score 倒排，取 top 20
```

`+60` 是 RRF 的经典常数 `k`（论文里取 60），平衡两路召回的权重。

更高级的玩法：上一层 rerank 模型（如 bge-reranker-base）。但 ES + Milvus + RRF **已经能解决 80% 业务场景**。

## 为什么不直接用 PGVector / Redis Stack VSS

都能跑，但有边界：

| 方案              | 适用规模     | 短板                          |
|-------------------|--------------|-------------------------------|
| pgvector          | <100w 向量   | 单机、索引重建慢、并发查询差   |
| Redis Stack VSS   | <500w 向量   | 内存全量驻留，存储成本高       |
| Milvus            | 千万~亿级    | 部署复杂，资源占用高           |
| Pinecone (SaaS)   | 全量级       | 贵、外网依赖                   |

gomall 没到亿级，目前 100w 商品。**严格说 pgvector 就够**，选 Milvus 主要是为了：

1. **横向扩展空间**：业务增长不至于再换底层
2. **学习/演示价值**：业内电商大厂（淘宝、京东、Shopee）都是 Milvus / Vespa 路线

## 性能口径：768 dim × 100w 向量

工程参考数字：

- 单条向量内存：768 × 4 bytes = 3 KB
- 100w 向量原始数据：~3 GB
- HNSW 索引（M=16）大致 2x 倍数：~6 GB
- 单 query p99（efSearch=64）：5~20ms

**比 ES 慢吗？** 不慢。专用向量库 + ANN 索引比"塞 ES 里搜 dense_vector" 快 10x+。

**比 DB 慢吗？** 比 DB 全表扫快 1000x+。

## 还有哪些坑

### 1. 向量漂移（model drift）

embedding 模型升级了，旧向量和新向量不在同一空间。直接搜召回率会暴跌。

**应对**：版本化 collection（`product_vector_v1`, `product_vector_v2`），灰度切流量。

### 2. 冷启动空召回

新商品入库 → outbox 事件 → 异步 embedding → Milvus upsert。**这一链路有秒级延迟**。

新品上架那几秒搜不到？大多数业务能接受。不能接受的话：写路径同步 embedding + 降级走 ES。

### 3. 过滤的代价

`category_id == 5` 这种标量过滤，Milvus 是**先做向量召回再过滤**（post-filter），如果某品类下向量很稀，可能 topK 都不够 K 个。

**应对**：拉大召回 topK（如查 100 取 20），或用 partition 物理隔离品类。

### 4. 长文本怎么 embed

商品 `info` 字段可能很长（几百字描述）。直接 embedding 会丢细节。

主流做法：
- 截断（128/256 token），重要信息放前面
- 或 chunk 切段后 multi-vector，搜索时取 max

gomall 现在只 embed `name + title`，info 走 ES，**先简单后复杂**。

## 代码位置（gomall）

- 客户端初始化：`repository/milvus/milvus.go`
- product_vector collection + 增删查：`repository/milvus/product_vector.go`
- 启动 hook：`initialize/milvus.go` + `cmd/main.go:tryInitMilvus`
- 增量索引消费者：Unit 7 接管，订阅 `product.changed`

## 想自己玩一下

```bash
# 拉一个本地 Milvus 单机
docker run -d --name milvus -p 19530:19530 milvusdb/milvus:v2.4.0 standalone

# 启动 gomall
MILVUS_ADDR=localhost:19530 go run cmd/main.go

# 看日志确认 collection ready
grep "Milvus product_vector collection ready" logs/*.log
```

不想拉 Milvus？啥都不用做。**MILVUS_ADDR 不设 → 静默跳过 → 跟没这功能一样跑**。这就是优雅降级的工程价值。
