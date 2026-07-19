# 商品搜索（下）：读懂 Hybrid Search 的代码与边界

关键词搜索依赖“字面上能对得上”。用户搜“iPhone 15 256G”时，这种办法很合适；型号和容量都能成为 Elasticsearch 的匹配依据。但用户也可能搜“适合雨天通勤的鞋”，商品标题里未必出现“雨天”或“通勤”。这类查询需要另一种召回办法：先把查询和商品文本转换成向量，再找距离较近的商品。

本章讨论 Gomall 为此写下的 Hybrid Search，也就是“关键词召回 + 向量召回”。重点不是向量索引的数学推导，而是读清一条真实代码路径，并判断它现在能做什么、还缺什么。

录制时建议拆成两段：第一段用 40–45 分钟讲第 1–7 节，第二段用约 10 分钟完成第 8 节自测与答案讲解。第 9 节是课后实践，不计入录制时间。

读完后，你应该能回答这些问题：

- `POST /api/v1/product/semantic-search` 收到请求后依次调用了谁；
- 为什么 Elasticsearch 的 `_score` 不能直接与 Milvus 的 L2 distance 相加；
- 为什么仓库里有 `SearchProductVector`，线上请求仍然可能根本没有访问 Milvus；
- embedding、Milvus 或 Elasticsearch 故障时，当前代码分别怎样处理；
- 改变 0.5 / 0.5 的权重后，怎样用查询样本判断结果有没有改善。

## 阅读前准备

建议先读完[商品搜索（上）](./03-product-search.md)，至少知道普通搜索通过 Elasticsearch 召回商品，ES 不可用时会退到 MySQL `LIKE` 查询。本章还会用到几个概念：

先记住几个后文反复出现的词：

| 术语 | 本章中的含义 |
|---|---|
| 召回 | 从全部商品中先找出一批候选，还不是最终排序 |
| embedding | 一段文本对应的浮点数数组；本项目固定为 768 维 |
| score | 通常越大越相关，例如 ES `_score` |
| distance | 两个向量的距离；L2 distance 越小越接近 |
| TopK | 最终保留相关度最高的 K 条结果 |

阅读时可以同时打开这些文件：

```text
service/search/routes.go
service/search/handler.go
service/search/semantic.go
service/search/embedding.go
service/search/milvus_stub.go
repository/milvus/product_vector.go
internal/product/dto.go
```

## 1. 先看接口：请求提供了哪些搜索条件

路由注册在 `service/search/routes.go`：

```go
public.POST("product/semantic-search", SemanticSearchProductsHandler())
```

因此完整接口是 `POST /api/v1/product/semantic-search`，它属于公开路由。请求体由 `ProductSemanticSearchReq` 描述：

```go
type ProductSemanticSearchReq struct {
    Query      string `json:"query" form:"query" binding:"required"`
    TopK       int    `json:"top_k" form:"top_k"`
    CategoryID *uint  `json:"category_id,omitempty" form:"category_id"`
}
```

例如：

```json
{
  "query": "适合雨天通勤的鞋",
  "top_k": 5,
  "category_id": 12
}
```

`query` 必填；`top_k` 没填或小于等于 0 时使用 10，大于 50 时压到 50。`category_id` 是可选的类目过滤条件。用指针而不是 `uint`，是为了区分“没有传类目”和“传入数值 0”。

Handler 本身很薄：绑定 JSON，调用 `SemanticSearch`，再用 `DataListResp` 包装结果。真正的搜索逻辑从 `service/search/semantic.go` 开始。

## 2. 为什么要保留两路召回

考虑下面两次搜索：

| 查询 | 关键词搜索的表现 | 向量搜索的表现 |
|---|---|---|
| `iPhone 15 256G` | 型号词明确，通常很准 | 可能混入同类手机 |
| `适合雨天通勤的鞋` | 商品文案没有原词时容易漏掉 | 有机会找到“防水”“城市徒步”等近义商品 |

向量搜索补的是语义相近但字面不同的候选，它并不适合取代关键词搜索。精确型号、品牌名和专有名词往往更依赖词项匹配；而向量结果还受到 embedding 模型、商品文本质量以及向量索引状态的影响。

Gomall 的设计是让两路各找一批候选，然后按商品 ID 合并。计划中的完整数据路径是：

```text
query
  ├─ EmbedText ──> GetSearcher().Search ──> 向量候选
  └─ SearchProductsWithScore ─────────────> 关键词候选
                         │
                 两路分别归一化
                         │
                 按商品 ID 合并分数
                         │
                  MySQL 批量回查
                         │
                   排序并截断 TopK
```

MySQL 不负责“找相似商品”，它在最后补齐当前商品数据，并过滤已经删除的 ID。搜索引擎保存的是检索副本，交易时仍然要以 MySQL 中的价格、上下架状态和库存规则为准。

要把“设计”与“当前运行状态”分开看。仓库没有注入真实 Milvus searcher，默认向量分支返回空切片，所以现阶段的实际路径更接近：

```text
“适合雨天通勤的鞋”
  ├─ EmbedText ──> nopMilvusSearcher ──> 0 条向量候选
  └─ Elasticsearch ────────────────────> 关键词候选
                                      │
                                MySQL 批量回查
```

也就是说，如果商品文案里没有相关词，当前可运行路径并不能依靠 Milvus 把它补回来。后文会沿源码解释原因。

### 停一下（约 30 秒）

假设服务器已经配置 `MILVUS_ADDR`，但没有其他代码改动。一次语义搜索会访问真实 Milvus 吗？老师可以先让学生根据上面的实际路径判断。

<details>
<summary>参考答案</summary>

不会。配置地址只负责初始化 Milvus 客户端；搜索服务默认持有 `nopMilvusSearcher`，仓库里没有生产 `SetSearcher` 调用把真实实现注入进去，所以向量分支仍返回空候选。

</details>

## 3. 逐段阅读 `SemanticSearch`

### 3.1 入口函数为什么只有一行

```go
func SemanticSearch(
    ctx context.Context,
    req *product.ProductSemanticSearchReq,
) ([]product.ProductSemanticHit, error) {
    return semanticSearchWith(ctx, req, defaultHybridDeps())
}
```

`defaultHybridDeps()` 提供三个真实依赖：`EmbedText`、`es.SearchProductsWithScore` 和 `ListByIDs`。核心函数把它们作为参数接收，原意是让测试能替换外部服务，不必真的启动 embedding API、Elasticsearch 和 MySQL。

不过当前仓库没有覆盖 `semanticSearchWith` 的测试。这个可注入结构已经搭好，测试还需要补上。

### 3.2 校验并限制 `topK`

```go
if req == nil || req.Query == "" {
    return nil, errors.New("query 不能为空")
}
topK := req.TopK
if topK <= 0 {
    topK = defaultTopK // 10
}
if topK > maxTopK {
    topK = maxTopK // 50
}
```

限制 `topK` 不只是接口体验问题。它还会影响后面的 embedding 之外两次查询、内存中的合并数量和 MySQL 回查规模。调用方不能用一个很大的 `top_k` 把这些成本无限放大。

注意，只有空字符串会被拒绝；全是空格的 `"   "` 仍能通过。如果接口要把它视为无效输入，应在校验前 `strings.TrimSpace`。

### 3.3 先生成查询向量

```go
vec, err := deps.embed(ctx, req.Query)
if err != nil {
    return nil, err
}
```

`EmbedText` 有两种工作方式：

- 配置 `EMBEDDING_API_URL` 后，它向外部服务发送 `{model, input}`，超时为 5 秒；
- 没有配置时，它用 SHA-256 派生出 768 维占位向量。

占位向量只保证“同一段文本得到同一数组”，方便本地把接口跑通。它没有学习词义，所以不能证明“雨天通勤”和“防水城市徒步”语义接近。看到接口返回 200，不代表语义检索已经有效。

embedding 失败会直接结束请求，当前代码没有退到纯关键词搜索。

### 3.4 两路都取 `topK * 3`

```go
vecHits, err := GetSearcher().Search(
    ctx, vec, topK*3, req.CategoryID,
)
if err != nil {
    return nil, err
}

keywordHits, _, err := deps.keyword(
    ctx, req.Query, 0, topK*3, req.CategoryID,
)
if err != nil {
    keywordHits = nil
}
```

用户只要前 10 条时，两路各取 30 条。这样做是给融合留余地：某个商品可能在单路中只排第 14，但它同时被两路命中，合并后反而应该进入前 10。

两处分支的错误处理并不对称。向量查询失败会返回错误；关键词查询失败则丢掉 `keywordHits`，继续走纯向量结果。这是现有代码的选择，不是 Hybrid Search 必然采用的规则。

还有一个容易漏看的细节：`GetSearcher()` 不在 `hybridDeps` 中。即使测试替换了 embedding、关键词查询和 DB loader，向量分支仍需额外调用全局 `SetSearcher`。全局可变状态会增加并行测试相互影响的风险。

### 3.5 两路分数为什么先归一化

Elasticsearch 的 `_score` 与 Milvus 返回值不在同一量纲。假设关键词分数是 18.2、9.7，向量一侧是 0.31、0.84，直接相加以后，关键词数值可能仅仅因为范围较大就控制最终排名。

代码对两组数据分别执行 min-max 归一化，把当前候选组的数值压到 `[0,1]`：

```go
semNorm := minMaxNormalize(vecScores(vecHits))
kwNorm := minMaxNormalize(esScores(keywordHits))
```

若同一路所有值都相等，`minMaxNormalize` 会统一返回 1。它表达的是“这批候选都有效，但本路无法区分先后”。若输入为空，则返回 `nil`。

这种方法简单，却有两个限制。归一化结果取决于当前候选集合，同一商品换一次查询或改变召回数量，归一化分数就可能变化；极端值也会压缩其他候选的差异。因此 0.8 并不是跨查询可比较的相关度概率。

### 3.6 按商品 ID 合并

```go
for i, h := range vecHits {
    id := uint(h.ID)
    if id == 0 {
        continue
    }
    hit := getOrInit(fused, id)
    hit.SemanticScore = semNorm[i]
}

for i, h := range keywordHits {
    id := h.Doc.ID
    hit := getOrInit(fused, id)
    hit.KeywordScore = kwNorm[i]
}

for id, h := range fused {
    h.Score = 0.5*h.SemanticScore + 0.5*h.KeywordScore
    ids = append(ids, id)
}
```

`fused` 以商品 ID 为 key。只被向量命中的商品，其 `KeywordScore` 保持 0；只被 ES 命中的商品则相反。两路都命中的商品能同时拿到两部分分数，所以通常更容易上升。

0.5 / 0.5 只是写在常量里的初始权重。它是否合适，取决于商品文本、用户查询和 embedding 模型，不能凭直觉宣布最优。

用一组数字看合并过程会更直观。假设修正了 L2 方向后，商品 A 的归一化语义分是 0.8、关键词分是 0.2；商品 B 的两项分数分别是 0.3 和 0.9。按当前权重：

```text
A = 0.5 × 0.8 + 0.5 × 0.2 = 0.50
B = 0.5 × 0.3 + 0.5 × 0.9 = 0.60
```

B 排在 A 前面。若把语义权重改成 0.7、关键词权重改成 0.3，则 A 得 0.62，B 得 0.48，顺序反转。这说明调权重会直接交换某些商品的名次，必须用固定查询集比较，不能只看一条顺眼的结果。

### 3.7 回查商品并形成稳定顺序

融合阶段只有 ID 和分数，代码随后执行 `ListByIDs(ids)`，把当前商品记录装入 `ProductSemanticHit`。如果某个索引 ID 在 MySQL 中已经不存在，它不会进入结果。

```go
sort.SliceStable(out, func(i, j int) bool {
    if out[i].Score == out[j].Score {
        return out[i].Product.ID < out[j].Product.ID
    }
    return out[i].Score > out[j].Score
})

if len(out) > topK {
    out = out[:topK]
}
```

最终按融合分数降序；同分时按商品 ID 升序，避免 Go map 的随机遍历顺序泄漏到接口结果。

但 `ListByIDs` 没有显式添加 `on_sale = true`。回查 MySQL 只能说明记录还存在，不能说明它满足展示规则。若产品要求搜索只返回在售商品，需要在召回、回查或最终过滤中明确实现，而且分页与总数口径也要随之调整。

## 4. 当前实现中的 L2 方向错误

`repository/milvus/product_vector.go` 使用 L2：

```go
results, err := MilvusClient.Search(
    ctx,
    ProductVectorCollection,
    []string{},
    expr,
    []string{productVectorIDField},
    []entity.Vector{entity.FloatVector(queryVec)},
    productVectorVectorField,
    entity.L2,
    topK,
    sp,
)
```

SDK 的 `SearchResult.Scores` 在这里保存 distance。L2 distance 越小，向量越接近。`flattenSearchResults` 却把它原样放进名为 `Score` 的字段：

```go
hits = append(hits, ProductSearchHit{
    ProductID: uint(id),
    Score:     score,
})
```

接下来 `SemanticSearch` 做 min-max 归一化，并按“数值越大越相关”参与融合。结果是方向反了。举一个只为说明方向的例子：

| 商品 | L2 distance | 当前归一化结果 | 当前代码的判断 |
|---|---:|---:|---|
| A | 0.2 | 0 | 最不相关 |
| B | 0.8 | 1 | 最相关 |

可选修法之一是先对 distance 做归一化，再计算 `1 - normalizedDistance`。也可以让 Milvus 适配器直接返回统一语义的“相关度分数”，但接口必须把字段含义写清楚。仅把字段从 `Score` 政名为 `Distance` 还不够，融合前仍需反向。

在这个问题修复前，真实 Milvus searcher 即使接通，Hybrid 排序也不可信。

## 5. Milvus 文件存在，生产链却没有闭合

仓库已经准备了若干基础能力：

- `product_vector` collection 使用商品 ID 作为主键，向量维度是 768；
- `category_id` 可以进入过滤表达式；
- HNSW 参数为 `M=16`、`efConstruction=200`、`efSearch=64`；
- repository 提供 `UpsertProductVector`、`DeleteProductVector` 和 `SearchProductVector`。

这些函数目前没有组成生产调用链。

### 5.1 搜索侧默认使用空实现

`service/search/milvus_stub.go` 中的默认对象是 `nopMilvusSearcher`：

```go
var searcher MilvusSearcher = nopMilvusSearcher{}

func (nopMilvusSearcher) Search(
    ctx context.Context,
    vec []float32,
    topK int,
    categoryID *uint,
) ([]Hit, error) {
    return nil, nil
}
```

它不报错，只返回空候选。仓库中也找不到生产代码调用 `SetSearcher`，更没有适配器把 `repository/milvus.SearchProductVector` 转成 `[]search.Hit`。

于是默认运行时会发生一件很隐蔽的事：接口名是 semantic search，代码也生成了 embedding，但向量分支永远为空，最终只剩 ES 候选。监控如果只看 HTTP 200，会误以为 Hybrid 正常工作。

### 5.2 写入侧也没有接上商品变更

上一章介绍的 `product.changed` 消费者只负责更新 Elasticsearch。`UpsertProductVector` 和 `DeleteProductVector` 在当前仓库没有调用者，所以新增、修改或删除商品不会自动更新 Milvus。

要把链路补齐，至少要处理以下工作：

1. 决定哪些商品字段组成 embedding 文本，并记录模型与维度版本；
2. 在商品变更事件后生成 embedding，再 Upsert 或 Delete 向量；
3. 对 embedding 和 Milvus 写入失败进行重试，最终失败进入可检查的死信记录；
4. 为历史商品做全量回填，模型升级时能重建 collection；
5. 启动时注入真实 `MilvusSearcher`，并先修正 L2 distance 的方向。

“读路径能查”和“写路径持续更新”缺一不可。只补搜索适配器，会查到空 collection 或过期数据；只补消费者写入，默认的 nop searcher 仍然读不到它们。

## 6. 降级行为必须按代码逐项说明

当前系统并没有统一的“任何依赖失败都自动降级”策略：

| 故障或配置状态 | 当前行为 | 用户可能看到什么 |
|---|---|---|
| 普通搜索的 ES 查询失败 | 退到 MySQL `LIKE` | 仍有结果，但匹配口径与延迟改变 |
| Hybrid 的 ES 分支失败 | 丢弃关键词候选，继续向量分支 | 精确型号结果可能变差 |
| embedding API 失败或超时 | 直接返回错误 | 语义搜索不可用 |
| 已注入的 Milvus searcher 返回错误 | 直接返回错误 | 语义搜索不可用 |
| 未注入 searcher | nop 返回空切片，继续 ES | 表面成功，实际是纯关键词 |
| 商品事件消费者积压 | ES 索引暂时落后；Milvus 本来就未接写入 | 新商品或修改内容暂时搜不到 |

空结果和系统错误不能混为一谈。embedding 超时若被吞掉并返回空数组，用户会以为没有符合条件的商品；运维侧的错误率也会显得很低。更可检查的做法是记录本次请求实际启用了哪些召回源，例如 `keyword_used`、`vector_used` 和降级原因，同时观察两路候选数。

是否要在 embedding 或 Milvus 故障时退到 ES，应由接口协议决定。当前实现选择“报错”，如果团队改为降级，也要给纯 ES 路径设独立超时，避免依赖依次超时导致请求拖得更久。

## 7. 用小查询集评估，而不是猜权重

准备约 20 条来自业务表达的查询已经足够做第一轮检查。样本应覆盖精确型号、品牌与品类、场景描述、错别字、类目过滤冲突和预期零结果。例如：

```text
iPhone 15 256G
山大马克杯
适合雨天通勤的鞋
pingguo 15 手机
类目=数码，query=羊毛围巾
```

为每条查询人工标注“前五名中哪些商品算相关”，然后分别保存关键词、向量和 Hybrid 三组结果。先回答两个很朴素的问题：该出现的商品有没有进入前五，明显不相关的商品是否挤到了前面。还要记录候选来源和实际降级路径，否则一次 nop searcher 下跑出的“Hybrid 结果”会污染评估。

权重实验必须使用同一批查询、同一份索引和同一个 embedding 模型。把 `weightSemantic` 从 0.5 改到 0.7 后，如果场景查询改善了，但精确型号大量退步，就不能只展示改善的那几条。

线上指标也有用，例如搜索后点击率、加购率和零结果率，但它们受价格、图片、排序位置等因素影响。离线查询集适合快速发现明显错误，线上实验再回答实际用户是否更愿意点击或购买。

## 8. 带答案的自测

### 题 1

请求 `top_k=20` 时，ES 与向量分支各请求多少条候选？为什么不只请求 20 条？

<details>
<summary>参考答案</summary>

各请求 60 条，即 `topK * 3`。多取候选是为了让两路共同命中但单路排名靠后的商品有机会在融合后进入最终前 20。

</details>

### 题 2

没有配置 `EMBEDDING_API_URL`，接口仍能生成 768 维向量。这是否说明本地已经具备语义搜索能力？

<details>
<summary>参考答案</summary>

不能。代码使用 SHA-256 派生占位向量，它只保证相同文本得到相同数组，没有学习语义关系；它用于联通代码路径。

</details>

### 题 3

为什么默认 `nopMilvusSearcher` 比直接返回“未初始化”错误更容易掩盖问题？

<details>
<summary>参考答案</summary>

nop 返回空切片且不报错，后续 ES 分支仍能产出结果，接口也会返回成功。如果没有记录各召回源候选数，调用方会把纯关键词结果误认成 Hybrid 结果。

</details>

### 题 4

商品 A 的 L2 distance 是 0.1，商品 B 是 0.9。哪一个更接近查询？当前代码归一化后会偏向哪一个？

<details>
<summary>参考答案</summary>

A 更接近查询。当前代码把 distance 当成越大越好的 score，min-max 后会给 B 更高的语义分，因此方向错误。

</details>

### 题 5

为什么最后回查 MySQL 仍不能保证搜索结果全部在售？

<details>
<summary>参考答案</summary>

`ListByIDs` 会过滤已经不存在的记录并返回当前字段，但它没有显式的 `on_sale = true` 条件。存在不等于允许展示。

</details>

## 9. 课后实践（不计入本次阅读时间）

### 练习 A：为 L2 方向错误补测试

给定两个向量候选，distance 分别为 0.2 和 0.8，再给它们相同的关键词分数。先写一个会暴露当前反向排序的测试，然后设计 distance 到相关度的转换。测试至少要覆盖 distance 全部相等的情况。

<details>
<summary>验收标准</summary>

- 修正后，distance 0.2 的商品必须排在 0.8 前面。
- distance 相同且关键词分数相同时，两件商品应得到相同融合分；最终顺序由现有商品 ID 规则决定。
- 测试应先在当前实现上失败，修正分数方向后通过。

</details>

### 练习 B：补一张生产接入清单

沿着“商品修改 → 事件 → embedding → Milvus Upsert → 语义查询”的方向检查仓库，写出每一步已有的函数、缺失的调用和失败后的处理办法。不要把“存在一个函数”写成“链路已完成”。

<details>
<summary>检查清单</summary>

答案至少应覆盖商品变更消费者、embedding 生成、Milvus Upsert/Delete、失败重试与历史回填、生产 `SetSearcher` 注入，以及 L2 distance 转相关度。每项都要标出“已有函数”和“缺失调用”。

</details>

### 练习 C：定义降级协议

分别为 embedding 超时、Milvus 查询失败和 ES 查询失败规定接口行为。每项需要写明：是否降级、降到哪一路、用户响应、日志字段和监控指标。最后检查这些选择会不会让一次请求连续等待多个 5 秒超时。

<details>
<summary>验收标准</summary>

三类故障都要写出响应、日志、指标和后备路径；还要给整次请求设置总超时，不能把各依赖超时简单相加。方案若返回空数组，必须区分“确实没有结果”和“搜索依赖故障”。

</details>

## 本章结论

Gomall 已经写出了 Hybrid Search 的主要骨架：生成查询向量、两路扩大召回、分别归一化、按商品 ID 合并，再回 MySQL 装配结果。代码也留下了便于替换外部依赖的接口。

它目前还不是可直接上线的向量搜索。默认 searcher 是 nop，商品变更没有写入 Milvus，L2 distance 又被当成越大越好的 score。读这类代码时，判断标准不该是“文件和函数齐不齐”，而应是请求能否走到真实依赖、数据能否持续写入，以及返回值的语义是否在整条链路中保持一致。
