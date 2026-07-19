# 商品搜索（上）：关键词检索与索引同步

用户只传 `name=咖啡壶`，ES 正常时能搜到咖啡壶；ES 一旦故障，同一请求却可能从 MySQL 返回近似全量商品。接口没有换，请求也没有换，答案为什么变了？这处真实偏差正好能把商品搜索的读链和降级边界串起来。

商城搜索要从大量商品中找出候选，按相关性排列，并排除不该展示的商品。gomall 为此保留了两份数据：MySQL 记录商品事实，Elasticsearch（下文简称 ES）保存用于检索的副本。

这份讲义从 `POST /api/v1/product/search` 开始，顺着读请求追到 ES 和 MySQL，再回头看商品修改后怎样进入索引。语义召回、Milvus 和 Hybrid 融合留到[商品搜索（下）](./03-product-search-hybrid.md)。

学生需要能读懂 Go 函数调用、GORM 链式查询和 JSON。第一次接触 ES 时先记住两个词就够了：召回负责找候选，排序负责决定候选的先后。过滤则是另一回事，类目和上下架状态属于硬规则，不该交给相关性分数碰运气。

---

## 一、先把搜索放回购物流程里

搜索结果是导购信息，不是交易凭证。索引经由异步事件更新，可能暂时落后于 MySQL；客户端也能修改请求中的价格。用户点下“购买”以后，订单服务仍要从 MySQL 重新读取商品状态、价格和库存，并执行交易校验。

```text
用户输入
  │
  ▼
文本召回 ── 找到可能相关的商品
  │
  ▼
相关性排序 ── 名称命中通常排在详情命中之前
  │
  ▼
业务过滤 ── 类目、上架状态等硬规则
  │
  ▼
返回展示字段 ── 名称、图片、标价、库存展示值
```

因此，ES 丢失后可以根据 MySQL 重建；MySQL 中的商品事实丢失，ES 文档不能反过来充当完整备份。这也是搜索服务可以接受短暂数据延迟，而订单计价不能接受的原因。

下面一直用这条请求读代码：

```http
POST /api/v1/product/search
Content-Type: application/x-www-form-urlencoded

info=露营咖啡壶&category_id=7&page_num=2&page_size=10
```

按当前实现，关键词“露营咖啡壶”和分页会进入 ES，`category_id=7` 不会进入普通搜索条件。ES 报错后，数据库继续使用 `info` 做 `LIKE` 查询。

路由在 `service/search/routes.go` 注册，`POST /api/v1/product/search` 可以匿名访问。`service/search/handler.go` 中的 `SearchProductsHandler` 把请求绑定为 `ProductSearchReq`，页大小为 0 时补默认值，然后调用 `ProductSearch`。绑定失败后统一响应模块会结束请求，业务函数不会执行。

数据源选择位于 `service/search/product_query.go`。下面只保留分支，响应字段转换在真实文件中直接展开：

```go
func ProductSearch(ctx context.Context, req *product.ProductSearchReq) (
    resp *types.DataListResp, err error,
) {
    if es.EsClient != nil {
        docs, total, esErr := SearchProducts(ctx, req)
        if esErr == nil {
            return buildESResponse(docs, total), nil
        }
        log.LogrusObj.Errorf("ES search failed, fall back to DB: %v", esErr)
    }

    products, count, err := product.NewProductDao(ctx).
        SearchProduct(req.Info, req.BasePage)
    if err != nil {
        return nil, err
    }
    return buildDBResponse(products, count), nil
}
```

`buildESResponse` 和 `buildDBResponse` 是讲义中的代称，不是仓库函数名。这里有三种结果需要分清：

- `EsClient == nil`，直接查 MySQL；
- ES 客户端存在，但请求、解析或 ES 状态码出错，记录日志后查 MySQL；
- ES 成功返回空数组，接口直接返回空结果，不会降级。

空数组表示“没有匹配商品”，不是搜索服务故障。若把 `len(docs) == 0` 当成降级条件，每次搜不存在的型号都会让交易库再做一次模糊匹配，而且两套引擎还可能给出不同答案。只有两条路径都失败时，错误才继续向上传递。

MySQL 在这里保住的是接口可用性，不是完整的搜索体验。它没有复刻 ES 的分词、字段权重和相关性排序；后面还会看到，两条路径目前连关键词来源都不完全相同。

## 二、DTO 能接收，不等于查询会使用

`ProductSearchReq` 看起来字段很多：

```go
type ProductSearchReq struct {
    ID         uint   `form:"id" json:"id"`
    Name       string `form:"name" json:"name"`
    CategoryID int    `form:"category_id" json:"category_id"`
    Title      string `form:"title" json:"title"`
    Info       string `form:"info" json:"info"`
    OnSale     bool   `form:"on_sale" json:"on_sale"`
    types.BasePage
}
```

框架能绑定这些字段，查询实现却只读取 `Info`、`Title`、`Name` 和分页参数。`service/search/service.go` 的处理如下：

```go
func SearchProducts(ctx context.Context, req *product.ProductSearchReq) (
    []*es.ProductDoc, int64, error,
) {
    kw := firstNonEmpty(req.Info, req.Title, req.Name)
    req.BasePage.Normalize()
    from := (req.PageNum - 1) * req.PageSize
    return es.SearchProducts(ctx, kw, from, req.PageSize)
}
```

`firstNonEmpty` 按 `info → title → name` 取第一个非空字符串。请求若同时携带 `info=露营`、`title=咖啡壶`、`name=手冲壶`，ES 最终只搜索“露营”，不会组合三个条件。

`Normalize` 把小于 1 的页码改为 1，把非正数页大小换成默认值，并限制最大页大小。之后计算 offset：

```text
from = (page_num - 1) × page_size
```

第 3 页、每页 20 条时，`from=40`，ES 跳过前 40 条再取 20 条。这适合浅分页；页数很深时，ES 需要维护并丢弃大量前置结果，通常会改用 `search_after`。当前项目没有实现深分页，本讲不展开。

回到开头的请求，`category_id=7` 到这里就断了。`SearchProducts` 没把它传给仓储层，`on_sale` 也没有被读取，所以普通搜索可能返回其他类目的商品或下架商品。下半讲使用的 `SearchProductsWithScore` 会把可选的 `category_id` 放入 ES 的 `bool.filter`，那是另一条实现，不能用来证明普通搜索已经支持类目过滤。

## 三、ES 排序与 MySQL 降级

项目创建名为 `product` 的索引。`name`、`title` 和 `info` 使用 `text` 类型与 `standard` analyzer；`category_id`、`num`、`boss_id` 等保存结构化数据，价格目前以 `keyword` 保存。

```go
type ProductDoc struct {
    ID            uint   `json:"id"`
    Name          string `json:"name"`
    Title         string `json:"title"`
    Info          string `json:"info"`
    CategoryID    uint   `json:"category_id"`
    Price         string `json:"price"`
    OnSale        bool   `json:"on_sale"`
    // 其余展示字段省略
}
```

`text` 字段参与分词检索，`keyword` 保留整体值，适合精确匹配或聚合。价格存成 `keyword` 后不适合直接做数值范围查询；如果产品要支持“100 到 300 元”，应先确定金额单位，再调整映射类型。

普通搜索发给 ES 的主体是一个 `multi_match`：

```go
q := map[string]any{
    "from": from,
    "size": size,
    "query": map[string]any{
        "multi_match": map[string]any{
            "query":  keyword,
            "fields": []string{"name^3", "title^2", "info"},
        },
    },
}
```

同一个关键词会检索三个文本字段。`name^3` 和 `title^2` 是 boost；其他条件接近时，名称命中的得分贡献高于标题，标题又高于详情。比如搜索“苹果手机”，商品名就是“苹果手机”的 A 通常应排在标题含“苹果手机配件”的 B 和详情写着“兼容苹果手机”的 C 前面。

`3` 和 `2` 表达业务偏好，不是放之四海皆准的参数。商品标题写法、分词器或样本分布变化后，要用真实查询样本重新评估。

相关性也不能代替过滤。`on_sale=false` 的商品即使名称完全命中，也应该直接排除，而不是降低一点分数。合适的 ES 结构是 `bool` 查询：文本条件放进 `must`，类目和上架状态放进 `filter`。当前普通查询还没这样做。

`internal/product/repo.go` 用相同条件执行两次查询，一次取当前页，一次统计总数：

```go
func (d *ProductDao) SearchProduct(info string, page types.BasePage) (
    products []*Product, count int64, err error,
) {
    page.Normalize()
    err = d.DB.Model(&Product{}).
        Where("name LIKE ? OR info LIKE ?", "%"+info+"%", "%"+info+"%").
        Offset((page.PageNum - 1) * page.PageSize).
        Limit(page.PageSize).
        Find(&products).Error
    if err != nil {
        return
    }

    err = d.DB.Model(&Product{}).
        Where("name LIKE ? OR info LIKE ?", "%"+info+"%", "%"+info+"%").
        Count(&count).Error
    return
}
```

`LIKE '%关键词%'` 可以完成基本的包含匹配和分页，但前置通配符通常让普通 B-Tree 索引难以缩小扫描范围。商品量增大后，搜索压力会回到交易数据库。

| 对比项 | ES 正常路径 | MySQL 降级路径 |
|---|---|---|
| 关键词来源 | `info / title / name` 中第一个非空值 | 只传 `req.Info` |
| 匹配字段 | `name`、`title`、`info` | `name`、`info` |
| 排序 | ES 文本相关性 | 没有显式 `ORDER BY` |
| 类目过滤 | 未使用 | 未使用 |
| 上架过滤 | 未使用 | 未使用 |
| 总数 | ES `hits.total.value` | 单独执行 `COUNT` |

### 想一想

请求只传 `name=咖啡壶`。先沿着两条分支判断：ES 正常和 ES 故障时，后端分别会搜索什么？

<details>
<summary>参考答案</summary>

ES 正常时，`firstNonEmpty` 会选到 `name`，搜索“咖啡壶”。ES 故障后，降级分支只把空的 `req.Info` 传给数据库，条件变成 `LIKE '%%'`。大多数记录都会满足条件，接口可能返回近似全量商品，而不是继续搜索“咖啡壶”。

</details>

这已经超出排序差异，两条路径执行的查询契约并不一致。修复时可以在服务层只计算一次规范化关键词，再把同一个值交给两个仓储实现；类目和上架条件也应有共同定义。

## 四、商品修改后，索引慢在哪儿

商品写入 MySQL 后，`internal/product/service.go` 调用 `emitProductChanged`，向 Outbox 表插入事件。后台发布器把事件投递到 RabbitMQ，`service/search/indexer.go` 再读取 MySQL 中的最新商品并写入 ES。

```text
商品创建 / 修改 / 删除
        │
        ▼
      MySQL
        │ emitProductChanged
        ▼
   Outbox（routing key: product.changed）
        │ 后台发布
        ▼
     RabbitMQ
        │ queue: search.product.indexer
        ▼
 Search Indexer ── 按 product_id 读取 MySQL
        │
        ├─ create / update ──► ES Upsert
        └─ delete ───────────► ES Delete
```

`StartProductIndexer` 把 prefetch 设为 32，并关闭自动确认：

```go
for d := range msgs {
    var ev events.ProductChanged
    if err := json.Unmarshal(d.Body, &ev); err != nil {
        _ = d.Nack(false, false) // 格式错误，不重新入队
        continue
    }
    if err := handleProductChanged(ctx, ev); err != nil {
        _ = d.Nack(false, true)  // 处理失败，重新入队
        continue
    }
    _ = d.Ack(false)
}
```

删除事件调用 `es.DeleteProduct`；创建和更新事件先按商品 ID 查询 MySQL，再调用 `es.UpsertProduct`。ES 文档 ID 就是商品 ID，重复的创建或更新消息会覆盖同一文档。删除已经不存在的文档会返回 404，仓储层把 404 当作成功。RabbitMQ 至少一次投递可能产生重复消息，这两处处理让消费者具备了基本幂等性。

`UpsertProduct` 和 `DeleteProduct` 都设置 `Refresh: "false"`。写请求成功以后，新文档还要等 ES refresh 才能被搜索看见。因此一件商品从 MySQL 变化到可搜索，中间至少要经过事件等待、消息消费和 ES refresh。

### Outbox 仍然留着一个窗口

当前 `ProductCreate`、`ProductUpdate`、`ProductDelete` 先完成商品写入，随后才调用 `emitProductChanged`；商品写入与 Outbox 插入不在同一个数据库事务里。若插入失败，代码只记日志，不会让商品操作回滚。

```text
MySQL 商品已更新
Outbox 插入失败
没有 product.changed 消息
ES 长期保留旧文档
```

发布器只能重试已经进入 Outbox 表的记录，补不出一条从未写成功的事件。严格的事务 Outbox 要让业务写入和事件记录共用一个本地事务，一起提交或一起回滚。

项目还提供全量 backfill：按 ID 升序分批读取 MySQL，再逐条 Upsert 到 ES。默认批大小为 200；单条写入成功后才推进游标，任何一次 loader 失败都会终止本轮回填。它适合初始化索引和修复历史缺口，运行时要留意 MySQL 与 ES 压力，不能拿它代替持续可靠的增量链路。

## 五、怎样判断搜索链路是否健康

接口返回 HTTP 200，不代表索引仍在更新。旧文档照样能被搜到，刚上架的商品却可能一直缺席。排查时沿数据流看：普通搜索的 ES 错误与 MySQL 降级次数、最老 pending Outbox 的年龄、`search.product.indexer` 队列积压和重复重入队、indexer 失败日志，以及商品写入时间到 ES 可搜索时间的差值。还应定期抽样比较 MySQL 商品与 ES 文档，查缺失、残留和字段不一致。

## 沿源码完整走一遍

从 `service/search/routes.go` 开始，依次打开 `handler.go`、`product_query.go`、`service.go`、`repository/es/product_index.go` 和 `internal/product/repo.go`；然后换到写链，追 `emitProductChanged` 与 `service/search/indexer.go`。

用下面四种输入检查分支：

1. ES 客户端存在，但查询返回 500。`ProductSearch` 会记录错误并查数据库；数据库也失败时，接口仍然失败。
2. 删除消息重复投递。第二次 ES Delete 返回 404，仓储层视为成功，消费者 Ack，不会持续重试。
3. 请求为 `title=防水跑鞋&page_num=2&page_size=10`。ES 使用“防水跑鞋”、`from=10`、`size=10`；降级路径读取空的 `req.Info`，查询条件变为 `name LIKE '%%' OR info LIKE '%%'`。
4. 商品更新成功，Outbox 插入失败。重启发布器也补不出消息，因为表中根本没有这条 Outbox 记录；只能依靠对账或回填修复 ES。

## 课后练习

### 1. 固定普通搜索的现有契约

为 ES 正常和 ES 故障设计表驱动测试，覆盖只传 `name`、只传 `info`、同时传多个关键词字段、指定 `category_id` 和分页越界值。除了响应，还要断言仓储层收到的关键词、分页参数、过滤条件与总数。

先用测试记录现状：ES 路径按 `info → title → name` 取值，数据库只接收 `req.Info`，普通 ES 与数据库路径都没有使用 `category_id`。之后再增加一组理想契约的失败用例，避免修改实现时把原问题一并改进测试里。

### 2. 补上结构化过滤

把普通 ES 查询改为 `bool`：`multi_match` 放进 `must`，`category_id` 和 `on_sale=true` 放进 `filter`。数据库降级也要在同一个 GORM 查询上追加对应的 `WHERE`，列表和 `COUNT` 共用相同条件。

### 3. 合并商品写入与事件记录

设计一次商品更新，让商品表修改与 Outbox 插入使用同一个事务：

```go
err := db.Transaction(func(tx *gorm.DB) error {
    if _, err := product.NewProductDaoByDB(tx).UpdateProduct(...); err != nil {
        return err
    }
    return outbox.NewOutboxDaoByDB(tx).Insert(...)
})
```

两个 DAO 都要绑定同一个 `tx`，Outbox 插入失败应使事务回滚。RabbitMQ 发布仍交给事务外的后台发布器，不要把网络请求塞进数据库事务。

### 4. 排查“刚上架却搜不到”

从 MySQL 商品记录开始，依次检查 Outbox、RabbitMQ 队列与消费者日志、ES 文档，以及 refresh 后的查询结果。写清每项证据能排除哪一段故障。
