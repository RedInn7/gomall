# 08.01 合并两路召回并返回 TopK

## 题目背景

Gomall 的商品搜索同时使用 Elasticsearch 和 Milvus。Elasticsearch 返回关键词相关度 `score`，数值越大越相关；Milvus 返回向量的 L2 `distance`，数值越小越相关。两套数值不能直接相加，而且同一件商品可能同时出现在两路结果里。

搜索服务需要先在每一路内部归一化，再按商品 ID 合并并计算融合分数。某一路暂时不可用时，另一条健康链路仍应返回结果；两路都失败时才让本次搜索失败。最终只返回融合排序后的 TopK，避免把所有候选都交给前端。

## 题目描述

请实现：

```go
func FuseTopK(
    keywordHits []KeywordHit,
    keywordErr error,
    vectorHits []VectorHit,
    vectorErr error,
    topK int,
) ([]Result, error)
```

规则如下：

1. `topK <= 0` 时返回 `ErrInvalidTopK`，且不返回部分结果。
2. 某一路返回非空错误时，该路结果全部忽略；两路同时出错时返回 `ErrBothRecallsFailed`。
3. 同一路出现重复商品时，关键词召回保留最大的 `Score`，向量召回保留最小的 `Distance`。
4. 关键词得分使用 min-max 归一化到 `[0,1]`，原始值越大，归一化得分越高。
5. 向量距离使用反向 min-max 归一化到 `[0,1]`，距离越小，归一化得分越高。
6. 一路只有一个不同取值，或该路全部值相等时，该路所有候选的归一化得分都记为 `1`，不得产生 `NaN`。
7. 两路都有可用候选时，`Score = 0.5 × KeywordScore + 0.5 × SemanticScore`；只有一路有候选时，直接使用该路归一化得分。
8. 同一商品要合并为一个 `Result`。没有出现在某一路时，对应的分数字段保持 `0`。
9. 按最终 `Score` 从高到低排序；分数相同按 `ProductID` 从小到大排序，保证结果稳定。
10. 最多返回前 `topK` 条；候选不足时返回全部候选。不得修改调用方传入的切片。

## 输入与已有数据

```go
type KeywordHit struct {
    ProductID uint
    Score     float64
}

type VectorHit struct {
    ProductID uint
    Distance  float64
}

type Result struct {
    ProductID    uint
    KeywordScore float64
    SemanticScore float64
    Score        float64
}
```

测试数据中的 `ProductID` 都大于零，`Score` 和 `Distance` 都是有限数值。

## 输出与完整样例

调用：

```go
got, err := FuseTopK(
    []KeywordHit{
        {ProductID: 101, Score: 20},
        {ProductID: 102, Score: 10},
    },
    nil,
    []VectorHit{
        {ProductID: 101, Distance: 0.2},
        {ProductID: 103, Distance: 0.8},
    },
    nil,
    2,
)
```

两路分别归一化后：

```text
keyword: 101 → 1, 102 → 0
vector:  101 → 1, 103 → 0
```

融合并取 TopK：

```go
err == nil
got == []Result{
    {
        ProductID: 101,
        KeywordScore: 1,
        SemanticScore: 1,
        Score: 1,
    },
    {
        ProductID: 102,
        KeywordScore: 0,
        SemanticScore: 0,
        Score: 0,
    },
}
```

商品 102 和 103 的融合分数相同，因此按较小的商品 ID 选择 102 进入 Top 2。

## 约束与提示

- 每一路候选数不超过 `10^5`，不要对每个商品重复扫描整份输入。
- 先去重，再计算该路的最小值和最大值。
- 路由成功但返回空切片不算故障。
- 降级时只使用健康链路；不要让失败链路携带的旧数据参与融合。
- 返回空结果时使用非 `nil` 的空切片或 `nil` 均可。

## 本地运行

```bash
go test -tags exercise ./exercises/08-product-search-hybrid/08.01-hybrid-fusion/problem
```

完成 `problem/hybrid.go` 中的 TODO 后，测试会覆盖归一化方向、同商品融合、重复候选、单路降级、双路失败、稳定排序以及 TopK 的边界行为。
