# 07.01 把搜索请求变成稳定的查询计划

## 题目背景

Gomall 的搜索框同时服务 Web、移动端和旧版客户端。不同客户端可能把关键词放在 `info`、`title` 或 `name` 中，也可能传入非法页码、过大的分页数量，或者省略“只看在售”条件。如果搜索服务直接把原始请求拼进 Elasticsearch，请求含义会随客户端变化，深分页还可能给集群带来不必要的压力。

搜索服务需要先完成一次确定性的请求清洗：选出唯一关键词、规范分页，并把类目与在售状态转换成不参与相关度算分的过滤条件。相同请求必须产生相同的查询计划，才能稳定测试并安全缓存。

## 题目描述

请实现 `BuildSearchPlan`，把 `SearchRequest` 转换为 `QueryPlan`：

1. 关键词按 `info > title > name` 选择第一个去除首尾空白后仍非空的值；全部为空时允许空关键词。
2. `page_num < 1` 时使用第 1 页；`page_size <= 0` 时使用 20；`page_size > 100` 时限制为 100。
3. `from = (page_num - 1) × page_size`，其中页码和页大小必须先完成规范化。
4. `category_id < 0` 返回 `ErrInvalidCategory`；`category_id > 0` 时生成类目过滤条件。
5. `on_sale` 使用指针区分“未传”和显式 `false`；非空时必须生成过滤条件。
6. 过滤条件顺序固定为 `category_id`、`on_sale`。

## 输入格式

本题不读取标准输入。调用函数：

```go
func BuildSearchPlan(req SearchRequest) (QueryPlan, error)
```

`SearchRequest` 提供三个候选关键词、类目、可选在售状态和分页参数。

## 输出格式

成功时返回清洗后的 `QueryPlan`，包含唯一关键词、ES 的 `from/size` 和有序过滤条件；类目为负数时返回空计划和 `ErrInvalidCategory`。

## 输入输出样例 #1

### 调用

```go
onlyOnSale := true
plan, err := BuildSearchPlan(SearchRequest{
    Info:       " 露营咖啡壶 ",
    Title:      "不会被选中",
    CategoryID: 7,
    OnSale:     &onlyOnSale,
    PageNum:    3,
    PageSize:   20,
})
```

### 返回

```go
QueryPlan{
    Keyword: "露营咖啡壶",
    From:    40,
    Size:    20,
    Filters: []Filter{
        {Field: "category_id", Value: "7"},
        {Field: "on_sale", Value: "true"},
    },
}
// err == nil
```

## 约束与提示

- `0 <= page_num <= 10^6`，非法或缺省值按规则规范化。
- `page_size` 可能为负数或远大于 100。
- 关键词可能包含中文、英文和首尾空白；不要删除关键词内部空格。
- `OnSale == nil` 与 `OnSale != nil && *OnSale == false` 含义不同。
- 错误返回不能携带已经填了一半的查询计划。

## 本地运行

```bash
go test -tags exercise ./exercises/07-product-search/07.01-search-query-plan/problem
```

完成 `problem/plan.go` 中的 TODO 后，公开测试会覆盖关键词优先级、空值、分页边界、过滤条件顺序、显式 `false` 和非法类目。
