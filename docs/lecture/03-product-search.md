# 商品搜索（上）：关键词检索与索引同步

用户输入“苹果手机”时，商城不能把整张商品表搬到应用层再筛选。它需要先找出可能相关的商品，再决定排列顺序，并排除不允许展示的商品。这个过程看起来像一次查询，实际牵涉两份数据：MySQL 中的商品事实，以及 Elasticsearch（下文简称 ES）中的搜索副本。

本章沿着一次普通商品搜索向下阅读代码，回答四个问题：请求如何选择 ES 或 MySQL，字段权重如何影响排序，商品修改后怎样进入索引，以及当前实现在哪些情况下会返回口径不同的结果。语义搜索、Milvus 和 Hybrid 融合在[商品搜索（下）](./03-product-search-hybrid.md)中讨论。

顺序阅读正文约需 35 分钟；如果同时打开源码核对，建议预留 50–55 分钟。第 9 节是课后实践，不计入这次阅读时间。

## 阅读前需要知道什么

读者应当能看懂 Go 的函数调用、GORM 链式查询和 JSON。不了解 ES 也可以继续读，先记住下面几个词：

- **召回**：从全部商品中找出一批候选。漏掉的商品无法靠后续排序补回来。
- **排序**：决定候选商品的先后次序。普通搜索主要使用文本相关性。
- **过滤**：执行类目、上下架状态等硬性业务规则。过滤条件不应靠相关性分数表达。
- **索引**：ES 为检索建立的数据结构。项目里的 `product` 索引也是一份可重建的商品副本。
- **最终事实源**：交易环节认可的数据来源。本项目中是 MySQL，不是 ES。

建议按“读请求，再读写同步”的顺序学习：

1. 从 `ProductSearch` 找到 ES 与数据库两条分支。
2. 进入 `SearchProducts`，确认关键词和分页怎样转换。
3. 查看 ES 查询 JSON，再与数据库的 `LIKE` 对照。
4. 最后追踪 `product.changed`，理解搜索副本为何会暂时落后。

涉及的代码集中在这些文件：

| 文件 | 负责什么 |
|---|---|
| `service/search/product_query.go` | 选择 ES 或数据库，并转换响应 |
| `service/search/service.go` | 选择关键词，计算 ES 分页偏移量 |
| `repository/es/product_index.go` | 索引结构、搜索、Upsert 与删除 |
| `internal/product/repo.go` | 数据库 `LIKE` 降级查询 |
| `internal/product/service.go` | 商品写入后产生 `product.changed` 事件 |
| `service/search/indexer.go` | 消费事件并更新 ES |

下面用一条请求贯穿读链：

```http
POST /api/v1/product/search
Content-Type: application/x-www-form-urlencoded

info=露营咖啡壶&category_id=7&page_num=2&page_size=10
```

读到每一层时都可以回来看这四个值。按当前实现，关键词“露营咖啡壶”和分页会进入 ES，`category_id=7` 却不会进入普通搜索条件；如果 ES 报错，数据库继续使用 `info` 做 `LIKE` 查询。

## 1. 先划清搜索与交易的边界

一次搜索可以拆成下面这条链：

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

这里最容易犯的错误，是把 ES 返回的价格和库存当成下单依据。索引通过异步事件更新，天然可能比 MySQL 慢；即使两边暂时一致，客户端也能修改请求中的金额。因此搜索结果只用于展示和引导点击，下单仍要重新从 MySQL 读取商品状态、价格与库存，并在交易逻辑中完成校验。

换句话说，ES 丢失时可以由 MySQL 重建；MySQL 中的商品事实丢失，ES 文档不能反过来充当完整备份。

## 2. 一次普通搜索怎样选择数据源

路由在 `service/search/routes.go` 注册为匿名可访问的 `POST /api/v1/product/search`。`service/search/handler.go` 中的 `SearchProductsHandler` 先把请求绑定为 `ProductSearchReq`，页大小为 0 时补上默认值，然后调用 `ProductSearch`。绑定失败由统一响应模块处理，业务函数不会执行。

真正选择数据源的逻辑位于 `service/search/product_query.go`。下面保留了决定分支的部分，省略响应字段转换：

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

`buildESResponse` 和 `buildDBResponse` 是为了阅读而写的代称，真实文件中直接展开了字段转换。先看 `if` 的含义，再看两个返回点：

- ES 客户端没有初始化，直接查 MySQL。
- ES 客户端存在，先查 ES；请求、解析或 ES 状态码出错时，再查 MySQL。
- ES 正常返回空数组，不触发降级。空结果是一种成功响应。
- 两条路径都失败，接口才向上返回错误。

### 代码走读：为什么不能用 `len(docs) == 0` 触发降级？

假设用户输入了一个确实不存在的型号，ES 正确结果就是空数组。如果空数组触发数据库查询，每次无结果搜索都会给交易库增加一次模糊匹配，而且两套引擎可能给出不同答案。降级应针对“搜索服务不可用”，不应把“没有匹配商品”误判成故障。

### 降级不等于等价替换

数据库分支让接口在 ES 故障时继续工作，但它并没有复刻 ES 的全部语义。服务降级时，产品通常接受“搜索能力变弱”，不能假装用户完全无感。后面的对照会看到，本项目两条路径连关键词来源都不相同。

## 3. 请求字段和分页怎样传入 ES

`ProductSearchReq` 定义了不少字段：

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

字段出现在 DTO 中，只代表框架能够接收它，不代表搜索实现已经使用它。普通 ES 路径真正读取的是 `Info`、`Title`、`Name` 和分页字段：

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

`firstNonEmpty` 按 `info → title → name` 的顺序取第一个非空字符串。若请求同时传入：

```text
info=露营
title=咖啡壶
name=手冲壶
```

ES 收到的关键词只有“露营”。这不是把三个条件组合查询。

`Normalize` 会把小于 1 的页码改为 1，把非正数页大小换成默认值，并限制最大页大小。随后用常见的 offset 分页公式计算 `from`：

```text
from = (page_num - 1) × page_size
```

例如第 3 页、每页 20 条，`from` 为 40，ES 跳过前 40 条后再取 20 条。浅分页足够直观；页数很深时，ES 需要维护并丢弃大量前置结果，通常要改用 `search_after` 等方式。本章不展开深分页实现，因为当前项目没有使用它。

### 代码走读：`category_id` 去了哪里？

答案是没有进入普通 ES 查询。`ProductSearchReq` 虽然接收 `category_id`，`SearchProducts` 却没有把它传给仓储层。`on_sale` 也一样。于是下架商品、其他类目商品仍可能出现在普通搜索结果中。

下半讲使用的 `SearchProductsWithScore` 是另一条实现，它会把可选的 `category_id` 放入 ES 的 `bool.filter`。不能据此推断普通搜索已经具备相同过滤能力。

## 4. ES 文档与 `multi_match`

项目创建名为 `product` 的索引。`name`、`title` 和 `info` 使用 `text` 类型与 `standard` analyzer；`category_id`、`num`、`boss_id` 等字段用于结构化数据，价格目前以 `keyword` 保存。

```go
type ProductDoc struct {
    ID            uint   `json:"id"`
    Name          string `json:"name"`
    Title         string `json:"title"`
    Info          string `json:"info"`
    CategoryID    uint   `json:"category_id"`
    Price         string `json:"price"`
    DiscountPrice string `json:"discount_price"`
    OnSale        bool   `json:"on_sale"`
    Num           int    `json:"num"`
    BossID        uint   `json:"boss_id"`
    ImgPath       string `json:"img_path"`
    CreatedAt     int64  `json:"created_at"`
}
```

`text` 字段参与分词检索，`keyword` 字段保留整体值，适合精确匹配或聚合。价格保存为 `keyword` 意味着它并不适合直接做数值范围查询；如果要支持“100 到 300 元”，应先明确金额存储单位，再改用合适的数值映射。

普通搜索最终发给 ES 的查询主体如下：

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

`multi_match` 让同一个关键词同时检索多个文本字段。字段名后的 `^3`、`^2` 是 boost：其他条件接近时，名称命中得到的贡献高于标题命中，标题又高于详情命中。

可以用下面三件商品理解它：

| 商品 | `name` | `title` | `info` |
|---|---|---|---|
| A | 苹果手机 | 新款智能终端 | 支持快充 |
| B | 透明保护壳 | 苹果手机配件 | 防摔材质 |
| C | 数据线 | 编织充电线 | 兼容苹果手机 |

搜索“苹果手机”时，A 的名称直接命中，通常应排在 B、C 前面。boost 表达的正是这个业务偏好，但它不是可靠性证明。商品标题的写法、分词器和样本分布变化后，`3` 与 `2` 也需要通过查询样本重新评估。

### 文本相关性不能代替业务过滤

`on_sale=false` 的商品即使名称高度匹配，也不该靠一个较低分数沉到列表末尾，而应直接排除。比较稳妥的 ES 结构是 `bool` 查询：把文本条件放进 `must`，把类目与上架状态放进 `filter`。过滤不参与相关性打分，语义也更清楚。

当前普通查询还没这样做。读代码时要区分“仓储文档里有字段”和“查询真正使用字段”，二者差一段明确的实现。

## 5. 数据库降级能保住什么

ES 不可用时，`internal/product/repo.go` 执行两次相同条件的查询，一次取当前页，一次统计总数：

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

它能保住基本的包含匹配和分页，却没有 ES 的分词、字段权重与相关性排序。`LIKE '%关键词%'` 前面带通配符，普通 B-Tree 索引往往难以有效缩小扫描范围；数据量增大后，它会把搜索压力带回交易数据库。

两条路径的实际差异如下：

| 对比项 | ES 正常路径 | MySQL 降级路径 |
|---|---|---|
| 关键词来源 | `info / title / name` 中第一个非空值 | 只传 `req.Info` |
| 匹配字段 | `name`、`title`、`info` | `name`、`info` |
| 排序 | ES 文本相关性 | 没有显式 `ORDER BY` |
| 类目过滤 | 未使用 | 未使用 |
| 上架过滤 | 未使用 | 未使用 |
| 总数 | ES `hits.total.value` | 单独执行 `COUNT` |

### 代码走读：只传 `name=咖啡壶` 会发生什么？

ES 正常时，`firstNonEmpty` 会选到 `name`，搜索“咖啡壶”。ES 故障后，降级分支只把空的 `req.Info` 传给数据库，条件变成 `LIKE '%%'`。大多数记录都会满足条件，接口可能返回近似全量商品，而不是继续搜索“咖啡壶”。

这是一处真实的契约偏差。修复时应先在服务层计算一次规范化关键词，再把同一个值交给两个仓储实现；类目和上架条件也应定义成共同的搜索契约，而不是分别猜测。

## 6. 商品为什么不会修改后立刻可搜

### 主线：从商品表到搜索索引

商品写入 MySQL 后，服务调用 `emitProductChanged` 向 Outbox 表插入一条事件。后台发布器把事件投递到 RabbitMQ，搜索索引消费者再读取 MySQL 的最新商品并写入 ES。

```text
商品创建 / 修改 / 删除
        │
        ▼
      MySQL
        │ 商品写入后调用 emitProductChanged
        ▼
   Outbox 记录（routing key: product.changed）
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

`StartProductIndexer` 将队列的 prefetch 设为 32，并关闭自动确认。消息处理规则值得逐行看：

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

删除事件直接调用 `es.DeleteProduct`；创建与更新事件先按商品 ID 查询 MySQL，再调用 `es.UpsertProduct`。ES 文档 ID 使用商品 ID，所以重复收到同一创建或更新事件时，后一次 Index 会覆盖同一文档。删除一个已经不存在的 ES 文档会得到 404，仓储层把它视为成功。两项处理让消费者具备了基本幂等性，符合 RabbitMQ 至少一次投递可能重复消息的现实。

`UpsertProduct` 和 `DeleteProduct` 都设置 `Refresh: "false"`。写请求成功不代表下一次搜索马上看见变更；ES 会按照自己的 refresh 周期让新文档进入可搜索状态。因此链路中至少有事件等待、消息消费和 ES refresh 三段延迟。

### 进阶观察：Outbox 还没有封住的窗口

`ProductCreate`、`ProductUpdate`、`ProductDelete` 先完成商品写入，随后调用 `emitProductChanged`。商品写入与 Outbox 插入不在同一个数据库事务里；而且 `emitProductChanged` 插入失败时只记录日志，不让商品操作失败。

于是存在下面这种状态：

```text
MySQL 商品已更新
Outbox 插入失败
没有 product.changed 消息
ES 长期保留旧文档
```

Outbox 能重试已经写入表中的事件，无法发布一条从未成功落盘的事件。严格的事务 Outbox 应让业务写入和事件记录使用同一个本地事务，二者一起提交或一起回滚。

### 为什么还需要全量回填

增量事件只覆盖事件机制启用之后的变化，也可能受上述窗口影响。项目提供 backfill：按 ID 升序分批从 MySQL 扫描商品，再逐条 Upsert 到 ES。默认批大小为 200；单条写入成功后才推进游标，任何一次 loader 失败都会终止本轮回填。

回填适合初始化索引和修复历史缺口，但不能替代稳定的增量链路。运行它时还要留意对 MySQL 与 ES 的压力。

## 7. 如何判断搜索系统是否健康

只看接口 HTTP 状态码不够。ES 中的旧文档仍能正常返回 200，用户却搜不到刚上架的商品。排查时可以沿数据流观察：

- 普通搜索的 ES 错误率，以及触发 MySQL 降级的次数；
- Outbox 中最老 pending 记录的年龄，而不只是记录条数；
- `search.product.indexer` 的队列积压和重复重入队情况；
- indexer 处理失败日志，以及商品写入时间到 ES 可搜索时间的差值；
- 定期抽样比较 MySQL 商品与 ES 文档，检查缺失、残留和字段不一致。

第一项反映读链故障，后四项用来发现“接口活着，但索引不再更新”。

## 8. 完整代码走读

读下面的场景，先自己写出答案，再展开提示。

### 场景 A：ES 客户端存在，但查询返回 500

调用链会怎样走？最终响应一定成功吗？

<details>
<summary>答案</summary>

`ProductSearch` 记录 ES 错误，然后调用 `ProductDao.SearchProduct`。数据库查询成功才返回商品列表；数据库也失败时，错误继续向上传递。因此“有降级”不等于“一定成功”。

</details>

### 场景 B：商品删除事件被重复投递两次

第二次删除 ES 文档得到 404，消费者会持续重试吗？

<details>
<summary>答案</summary>

不会。`DeleteProduct` 把 404 当作幂等成功，`handleProductChanged` 返回 `nil`，消费者 Ack 第二条消息。

</details>

### 场景 C：请求为 `title=防水跑鞋&page_num=2&page_size=10`

ES 正常时使用什么关键词和分页参数？ES 失败后又会搜索什么？

<details>
<summary>答案</summary>

ES 路径选中 `title` 的“防水跑鞋”，`from=10`、`size=10`。降级路径只读取空的 `req.Info`，数据库条件成为 `name LIKE '%%' OR info LIKE '%%'`。这再次说明两条路径目前没有共享同一套查询契约。

</details>

### 场景 D：商品更新成功，Outbox 插入失败

稍后重启 Outbox 发布器能自动补出这条消息吗？

<details>
<summary>答案</summary>

不能。发布器只能扫描已经存在的 Outbox 记录。若没有对账或回填，ES 可能长期保留旧值。

</details>

## 9. 课后实践（不计入本次阅读时间）

### 练习一：写出普通搜索的契约测试

为 ES 正常和 ES 故障两种情况设计测试，至少覆盖：只传 `name`、只传 `info`、同时传多个关键词字段、指定 `category_id`、分页越界值。检查项除了状态码，还包括传给仓储层的关键词、分页参数、过滤条件和结果总数。

<details>
<summary>最小参考骨架</summary>

至少准备两组表驱动用例：ES 成功时断言 `info → title → name` 的关键词优先级、`from/size` 和返回 `total`；ES 失败时断言 DAO 只接收 `req.Info`。另加 `category_id` 用例，确认普通 ES 与 DB 路径当前都没有使用它。测试先固定现状，再单独写理想契约的失败用例，避免一边改实现一边改掉证据。

</details>

### 练习二：补上结构化过滤

把普通 ES 查询改成 `bool`：文本 `multi_match` 放入 `must`，`category_id` 和 `on_sale=true` 放入 `filter`。思考数据库降级应该怎样保持相同语义。

**答案提示：** `on_sale` 属于结构化过滤条件，应当放入 `bool.filter`，而非 `multi_match`。数据库侧应在同一个 GORM 查询上追加结构化 `WHERE`，列表查询与 `COUNT` 必须共用相同条件。

### 练习三：消除 Outbox 丢事件窗口

设计一次商品更新，使商品表修改与 Outbox 插入在同一个事务里完成。列出需要改变的 DAO 构造方式与错误传播方式。

<details>
<summary>参考事务骨架</summary>

```go
err := db.Transaction(func(tx *gorm.DB) error {
    if _, err := product.NewProductDaoByDB(tx).UpdateProduct(...); err != nil {
        return err
    }
    return outbox.NewOutboxDaoByDB(tx).Insert(...)
})
```

商品 DAO 与 Outbox DAO 必须绑定同一个 `tx`；Outbox 插入失败应让事务回滚。RabbitMQ 发布仍由事务外的后台发布器完成，不能把网络请求塞进数据库事务。

</details>

### 练习四：解释一次“搜索不到刚上架商品”的排查顺序

写出你会查看的四类证据，并说明每项能排除哪一段故障。

**答案提示：** 从 MySQL 商品记录开始，依次检查 Outbox、RabbitMQ 队列与消费者日志、ES 文档和 refresh 后的查询结果。按链路排查比反复调用 HTTP 接口更容易定位断点。

## 本章结论

普通商品搜索有一条清楚的主路径：ES 可用时执行多字段相关性查询，ES 出错时退到 MySQL `LIKE`。这条退路保证了部分可用性，却没有保证查询等价；关键词选择、匹配字段与排序都可能改变，类目和上架过滤则在两条路径中都缺失。

写链通过 Outbox、RabbitMQ 和 indexer 把 MySQL 变化同步到 ES，所以搜索副本允许短暂落后。当前实现用商品 ID Upsert、删除 404 成功和手动 Ack 处理重复消息，但业务写入与 Outbox 插入不在同一事务，仍可能永久漏掉事件。理解这些边界后，再进入 Hybrid 搜索会轻松许多：它增加了新的召回方式，却仍要服从同一套商品事实与过滤规则。
