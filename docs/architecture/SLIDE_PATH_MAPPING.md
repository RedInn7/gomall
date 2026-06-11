# Slide / Blog 旧路径 → DDD 新路径映射

重构后所有领域代码从「按层」搬到 `internal/<domain>/`。decks 里 `\srcnote{...}` 引用、`caption={...}`、`lstlisting` 代码片段里的旧路径/旧符号据此同步。

## 文件路径映射（用于 \srcnote / caption 里的 `路径:行号 说明`）

| 旧路径 | 新路径 |
|---|---|
| `api/v1/<d>.go` | `internal/<d>/handler.go` |
| `api/v1/order.go` | `internal/order/handler.go` |
| `api/v1/order_shipping.go` | `internal/order/handler_shipping.go` |
| `api/v1/pay.go` | `internal/payment/handler.go` |
| `api/v1/paydown_crypto.go` | `internal/payment/handler_crypto.go` |
| `api/v1/product.go` | `internal/product/handler.go` |
| `api/v1/product_semantic.go` | `service/search/handler.go`（语义检索 handler 迁入 search 包） |
| `api/v1/idempotency.go` | `internal/idempotency/handler.go` |
| `api/v1/common.go` | `internal/shared/response/response.go`（ErrorResponse） |
| `service/<d>.go` | `internal/<d>/service.go` |
| `service/order_async.go` | `internal/order/async.go` |
| `service/order_cancel.go` | `internal/order/cancel.go` |
| `service/order_consumer.go` | `internal/order/consumer.go` |
| `service/order_shipping.go` | `internal/order/shipping.go` |
| `service/order_state.go` | `internal/order/state.go` |
| `service/order_task.go` | `internal/order/task.go` |
| `service/payment_crypto.go` | `internal/payment/service_crypto.go` |
| `service/favories.go` | `internal/favorite/service.go` |
| `service/skill_goods.go` | `internal/skill/service.go` |
| `service/redpacket_consumer.go` | `internal/redpacket/consumer.go` |
| `service/redpacket_task.go` | `internal/redpacket/task.go` |
| `service/product.go`（其中 ProductSearch/搜索编排部分） | `service/search/product_query.go` |
| `service/outbox/publisher.go` | `internal/shared/outbox/publisher.go` |
| `repository/db/dao/<d>.go` | `internal/<d>/repo.go` |
| `repository/db/dao/product_img.go` | `internal/product/repo_img.go` |
| `repository/db/dao/outbox.go` | `internal/shared/outbox/repo.go` |
| `repository/db/dao/init.go` | **不变**（仍是 DB 基座） |
| `repository/db/model/<d>.go` | `internal/<d>/model.go` |
| `repository/db/model/product_img.go` | `internal/product/model_img.go` |
| `repository/db/model/outbox.go` | `internal/shared/outbox/model.go` |
| `types/<d>.go` | `internal/<d>/dto.go` |
| `types/shipping.go` | `internal/order/dto_shipping.go` |
| `types/money.go` | `internal/money/dto.go` |
| `types/common.go` | **不变**（BasePage/DataListResp 共享信封） |

**原位不动**（基础设施/横切，路径不变）：`initialize/*`、`repository/rabbitmq/*`、`repository/es/*`、`repository/milvus/*`、`repository/cache/*`、`service/{search,web3,grpc,events,inventory}/*`、`consts/*`、`pkg/*`、`middleware/*`、`config/*`、`proto/*`、`cmd/*`。

## 代码符号映射（用于 lstlisting 代码片段）

- 跨域引用加包名前缀：`model.X` → `<域>.X`（如 `model.Order`→`order.Order`、`model.Product`→`product.Product`、`model.User`→`user.User`）；`dao.NewXDao` → `<域>.NewXDao`（如 `dao.NewProductDao`→`product.NewProductDao`）。
- **outbox 专属**：`dao.NewOutboxDao`/`dao.NewOutboxDaoByDB` → `outbox.NewOutboxDao*`；`model.OutboxEvent`/`model.OutboxStatus*` → `outbox.OutboxEvent`/`outbox.OutboxStatus*`。
- **同域内部片段**：若该 lstlisting 标注的就是某域自己的文件（如 `internal/order/cancel.go`），该域自身符号用**裸名**（`NewOrderDao`、`Order`，不加 `order.` 前缀），只有跨域符号才加前缀。
- **order 簇消费者别名**：payment/refund/groupbuy 内部以 `orderpkg` 别名引入 order，片段里写 `orderpkg.Order`、`orderpkg.NewOrderDaoByDB`。
- `ErrorResponse(...)` → `response.ErrorResponse(...)`（handler 片段）。
- `GetXSrv()` 跨域调用 → `<域>.GetXSrv()`（如 order 里调满减 → `promo.GetPromoSrv()`）。

## 行号说明

代码基本是「整体平移 + 去前缀」搬过去的，函数名不变、相对顺序基本不变，但**绝对行号普遍偏移**。同步以**路径正确 + 代码符号正确**为第一优先级；`:行号` 范围按「能快速在新文件里核到就修，核不到就保留原范围作近似」处理——`\srcnote` 里的函数名/说明才是锚点。
