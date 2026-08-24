# 07 商品搜索（上）：关键词检索与索引同步

# 什么是es

**Elasticsearch (ES)** 是一个基于 Lucene 的分布式、RESTful 风格的开源搜索引擎与数据分析引擎。它专门用于**海量文本检索、日志分析、实时数据聚合**等场景。



# es和db的区别

| **维度**       | **传统数据库 (LIKE '%abc%')**                     | **Elasticsearch (ES)**                                   |
| -------------- | ------------------------------------------------- | -------------------------------------------------------- |
| **底层索引**   | B+ Tree（前缀模糊会使索引失效，导致**全表扫描**） | **倒排索引（Inverted Index）**（专为文本搜索设计）       |
| **查询性能**   | 数据量越大越慢，千万级数据直接卡死/打满 I/O       | 毫秒级响应，数据量增加对查询耗时影响极小                 |
| **分词能力**   | 不支持智能分词，只能做死板的字符匹配              | 支持专业分词器（如 IK 分词），能理解词组与语境           |
| **相关度排序** | 只有“匹配”或“不匹配”，无法按相关性评分            | 内置 BM25/TF-IDF 算法，按内容**匹配度高低**自动打分排序  |
| **高级搜索**   | 极差，拼写纠错/高亮/同义词需业务层手写大量逻辑    | 原生支持**拼写纠错、高亮显示、同义词、自动补全、同音字** |
| **扩展性**     | 单机瓶颈明显，分库分表后跨库模糊查询极难维护      | 原生分布式架构，天然支持分片与横向水平扩容               |



**为什么不用 DB 的 LIKE？**

- **索引完全失效：** 在 MySQL 等数据库中，`LIKE '%关键字%'` 包含左通配符时，B+ Tree 无法按前缀定位，必须逐行扫描整张表。几百万条数据就会引发严重的性能事故。
- **倒排索引的降维打击：**
  - **正排（DB）：** 文档 ID $\rightarrow$ 内容文本（找词需要遍历所有文本）。
  - **倒排（ES）：** 关键词 $\rightarrow$ [文档 ID 1, 文档 ID 3, ...]（直接根据词精准命中对应文档）。
- **业务需求不仅是“包含字符”：** 真正的搜索场景通常需要**中文分词**（搜“苹果手机”能匹配“苹果 iPhone 15”）、**纠错**（搜“华唯”能找到“华为”）和**权重排序**，这些在关系型数据库中几乎无法高效实现。





用户只传 `name=咖啡壶`，ES 正常时能搜到咖啡壶；ES 一旦故障，同一请求却可能从 MySQL 返回近似全量商品。接口没有换，请求也没有换，答案为什么变了？这处真实偏差正好能把商品搜索的读链和降级边界串起来。

商城搜索要从大量商品中找出候选，按相关性排列，并排除不该展示的商品。gomall 为此保留了两份数据：MySQL 记录商品事实，Elasticsearch（下文简称 ES）保存用于检索的副本。

这份讲义从 `POST /api/v1/product/search` 开始，顺着读请求追到 ES 和 MySQL，再回头看商品修改后怎样进入索引。语义召回、Milvus 和 Hybrid 融合留到[商品搜索（下）](./08-product-search-hybrid.md)。

学完这一讲，先不追求背下 ES 参数，而是要能回答四个业务问题：用户输入什么、系统实际搜索什么、ES 故障时体验会怎样变化，以及商品修改后多久能被搜到。

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

路由在 `service/search/routes.go` 注册，`POST /api/v1/product/search` 可以匿名访问。

数据源选择位于 `service/search/product_query.go`。下面只保留分支，响应字段转换在真实文件中直接展开：

```go
func ProductSearch(ctx context.Context, req *product.ProductSearchReq) (
    resp *types.DataListResp, err error,
) {
    // 正常情况下优先使用 ES：它负责关键词召回和相关性排序。
    if es.EsClient != nil {
        docs, total, esErr := SearchProducts(ctx, req)
        if esErr == nil {
            // 搜索成功但结果为空也是正常答案，不能因此改查 MySQL。
            return buildESResponse(docs, total), nil
        }
        // 只有 ES 真正出错时才降级，避免一次故障让搜索接口完全不可用。
        log.LogrusObj.Errorf("ES search failed, fall back to DB: %v", esErr)
    }

    // MySQL 只提供基础包含匹配，保住可用性，不保证与 ES 排序一致。
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

空数组表示“没有匹配商品”，不是搜索服务故障。

MySQL 在这里保住的是接口可用性，不是完整的搜索体验。它没有复刻 ES 的分词、字段权重和相关性排序；后面还会看到，两条路径目前连关键词来源都不完全相同。



| **维度**            | **MySQL**                                           | **Redis**                                          | **Elasticsearch (ES)**                                       |
| ------------------- | --------------------------------------------------- | -------------------------------------------------- | ------------------------------------------------------------ |
| **定位类型**        | 关系型数据库 (RDBMS)                                | 内存键值缓存 / 数据库 (Key-Value)                  | 分布式搜索与分析引擎                                         |
| **核心存储介质**    | 磁盘为主（配合 Buffer Pool 缓存）                   | **纯内存**（异步/持久化机制刷盘）                  | 磁盘为主（深度依赖 OS Filesystem Cache）                     |
| **核心数据结构**    | **B+ Tree**、Hash 索引                              | String, Hash, List, Set, ZSet, Stream 等           | **倒排索引 (Inverted Index)**、BKD Tree、Doc Values          |
| **事务支持 (ACID)** | **原生支持强事务 (ACID)**，支持回滚                 | 支持弱事务（Multi/Exec，无原子回滚支持）           | **不支持传统事务**，仅保证单文档操作原子性                   |
| **读写延迟**        | 毫秒级 (ms)                                         | **亚毫秒级 / 微秒级 ($\mu$s)**                     | 毫秒级 (ms)（写入有近实时延迟 ~1s）                          |
| **擅长场景**        | 核心业务强一致存储、订单交易、转账、精确增删改查    | 热点数据缓存、高频计数、分布式锁、会话管理、排行榜 | **海量全文检索、多条件复杂组合筛选、日志监控、数据聚合分析** |
| **不擅长场景**      | 大规模模糊检索 (`%like%`)、多维度非固定字段动态筛选 | 复杂关系关联查询、海量冷数据低成本持久化           | 高频单行频繁更新、强事务交易、超低延迟点查                   |
| **数据一致性**      | 强一致性 (Strong Consistency)                       | 最终一致性 / 内存主从异步复制                      | **准实时 (Near Real-Time, NRT)**，默认 1s 刷新入段           |



## 二、DTO 能接收，不等于查询会使用

`ProductSearchReq` 看起来字段很多：

```go
type ProductSearchReq struct {
    ID         uint   `form:"id" json:"id"`                   // 商品精确定位字段
    Name       string `form:"name" json:"name"`               // 商品名称关键词
    CategoryID int    `form:"category_id" json:"category_id"` // 类目属于硬过滤条件
    Title      string `form:"title" json:"title"`             // 营销标题关键词
    Info       string `form:"info" json:"info"`               // 搜索框当前主要使用的关键词
    OnSale     bool   `form:"on_sale" json:"on_sale"`         // 是否只展示上架商品
    types.BasePage                                             // 页码与每页数量
}
```

框架能绑定这些字段，查询实现却只读取 `Info`、`Title`、`Name` 和分页参数。`service/search/service.go` 的处理如下：

```go
func SearchProducts(ctx context.Context, req *product.ProductSearchReq) (
    []*es.ProductDoc, int64, error,
) {
    // 三个字段同时出现时只取第一个，优先级是 info > title > name。
    kw := firstNonEmpty(req.Info, req.Title, req.Name)
    // 统一修正非法页码、默认页大小和最大页大小。
    req.BasePage.Normalize()
    // ES 使用 from 表示跳过多少条，而不是当前页码。
    from := (req.PageNum - 1) * req.PageSize
    return es.SearchProducts(ctx, kw, from, req.PageSize)
}
```

`firstNonEmpty` 按 `info → title → name` 取第一个非空字符串。请求若同时携带 `info=露营`、`title=咖啡壶`、`name=手冲壶`，ES 最终只搜索“露营”，不会组合三个条件。

`Normalize` 把小于 1 的页码改为 1，把非正数页大小换成默认值，并限制最大页大小。之后计算 offset：

```text
from = (page_num - 1) × page_size
```

第 3 页、每页 20 条时，`from=40`，ES 跳过前 40 条再取 20 条。页数很深时，ES 需要维护并丢弃大量前置结果，通常会改用 `search_after`。



## 三、ES 排序与 MySQL 降级

先从用户体验理解这一节。用户搜“苹果手机”时，希望先看到手机本身，而不是手机壳；筛选“在售”后，也不应该看到已经下架的爆款。前者是排序问题，后者是过滤问题。

| 用户动作 | 业务期望 | 系统职责 |
|---|---|---|
| 输入“苹果手机” | 先找到可能相关的商品 | 召回 |
| 手机排在手机壳前面 | 更符合购买意图的结果靠前 | 排序 |
| 勾选“仅看在售” | 下架商品完全不出现 | 过滤 |
| ES 暂时故障 | 页面仍能返回基本结果 | 降级 |

项目创建名为 `product` 的索引。`name`、`title` 和 `info` 使用 `text` 类型与 `standard` analyzer；`category_id`、`num`、`boss_id` 等保存结构化数据，价格目前以 `keyword` 保存。

```go
type ProductDoc struct {
    ID            uint   `json:"id"`          // 与 MySQL 商品 ID 对齐，用于覆盖更新
    Name          string `json:"name"`        // 权重最高：名称命中最接近用户意图
    Title         string `json:"title"`       // 权重次高：承载营销标题
    Info          string `json:"info"`        // 商品详情也参与召回，但权重最低
    CategoryID    uint   `json:"category_id"` // 用于类目硬过滤
    Price         string `json:"price"`       // 当前是字符串，不适合直接做价格区间
    OnSale        bool   `json:"on_sale"`     // 下架商品应通过 filter 排除
    // 其余展示字段省略
}
```

`text` 字段参与分词检索，`keyword` 保留整体值，适合精确匹配或聚合。价格存成 `keyword` 后不适合直接做数值范围查询；如果产品要支持“100 到 300 元”，应先确定金额单位，再调整映射类型。

普通搜索发给 ES 的主体是一个 `multi_match`：

```go
q := map[string]any{
    "from": from, // 跳过前面页的结果
    "size": size, // 本页最多返回多少条
    "query": map[string]any{
        "multi_match": map[string]any{
            "query": keyword, // 用户输入的同一个关键词
            // 名称、标题、详情都能命中，但业务上名称最重要。
            "fields": []string{"name^3", "title^2", "info"},
        },
    },
}
```

同一个关键词会检索三个文本字段。`name^3` 和 `title^2` 是 boost；其他条件接近时，名称命中的得分贡献高于标题，标题又高于详情。比如搜索“苹果手机”，商品名就是“苹果手机”的 A 通常应排在标题含“苹果手机配件”的 B 和详情写着“兼容苹果手机”的 C 前面。

`3` 和 `2` 表达业务偏好，不是放之四海皆准的参数。商品标题写法、分词器或样本分布变化后，要用真实查询样本重新评估。

评估时不要只问“接口快不快”，还要拿真实搜索词观察：目标商品是否进入前几名、无关商品是否混入、零结果率是否异常，以及用户搜索后是否继续点击和下单。

相关性也不能代替过滤。`on_sale=false` 的商品即使名称完全命中，也应该直接排除，而不是降低一点分数。合适的 ES 结构是 `bool` 查询：文本条件放进 `must`，类目和上架状态放进 `filter`。当前普通查询还没这样做。

`internal/product/repo.go` 用相同条件执行两次查询，一次取当前页，一次统计总数：

```go
func (d *ProductDao) SearchProduct(info string, page types.BasePage) (
    products []*Product, count int64, err error,
) {
    // 先修正分页，避免 page_num=0 或超大 page_size 直接进入数据库。
    page.Normalize()
    // 第一条 SQL 只取当前页，满足接口展示。
    err = d.DB.Model(&Product{}).
        Where("name LIKE ? OR info LIKE ?", "%"+info+"%", "%"+info+"%").
        Offset((page.PageNum - 1) * page.PageSize).
        Limit(page.PageSize).
        Find(&products).Error
    if err != nil {
        return
    }

    // 第二条 SQL 统计总数，前端据此计算总页数。
    // 列表和总数必须使用完全相同的过滤条件，否则分页会“少货”或“多页”。
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

从业务上看，降级不是“换个数据库继续查”这么简单。降级前后至少要约定关键词、过滤条件和分页含义；排序可以变朴素，但不能把“搜咖啡壶”变成“返回所有商品”。

## 四、商品修改后，索引慢在哪儿

假设运营在 10:00 把一款咖啡壶从下架改成上架。MySQL 写成功只说明后台修改完成，不代表消费者立刻能搜到。商品还要经历事件入库、消息发布、索引消费和 ES refresh 四段路程。

因此，“后台显示已上架”和“前台可以搜索”是两个不同的业务时刻。产品需要给这段延迟设目标，例如 99% 的商品修改在 30 秒内可搜索；否则技术链路即使没有报错，也无法判断体验是否合格。

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
        // 消息格式已经损坏，重试不会变好；拒绝并停止重新入队。
        _ = d.Nack(false, false)
        continue
    }
    if err := handleProductChanged(ctx, ev); err != nil {
        // MySQL 或 ES 暂时失败时重新入队，稍后再次处理。
        _ = d.Nack(false, true)
        continue
    }
    // 只有 ES 已成功同步，才确认这条消息消费完成。
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

监控最终要回答业务问题，而不只是展示组件状态：

| 业务问题 | 建议观察的信号 |
|---|---|
| 用户是否经常搜不到商品 | 零结果率、热门词零结果、搜索后退出率 |
| 搜索结果是否有用 | 前几位点击率、搜索到加购率、搜索到下单率 |
| ES 是否正在悄悄失效 | MySQL 降级率、ES 错误率、搜索延迟 |
| 新商品多久能被搜到 | 商品更新时间到 ES 可见时间的 P95/P99 |
| 两份数据是否逐渐分叉 | 缺失文档、残留文档、关键字段不一致数量 |

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
    // 商品修改和 Outbox 事件必须使用同一个事务对象 tx。
    if _, err := product.NewProductDaoByDB(tx).UpdateProduct(...); err != nil {
        return err
    }
    // 事件写入失败时返回错误，数据库会把商品修改一并回滚。
    return outbox.NewOutboxDaoByDB(tx).Insert(...)
})
```

两个 DAO 都要绑定同一个 `tx`，Outbox 插入失败应使事务回滚。RabbitMQ 发布仍交给事务外的后台发布器，不要把网络请求塞进数据库事务。

### 4. 排查“刚上架却搜不到”

从 MySQL 商品记录开始，依次检查 Outbox、RabbitMQ 队列与消费者日志、ES 文档，以及 refresh 后的查询结果。写清每项证据能排除哪一段故障。
