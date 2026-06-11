# DDD 按领域分包重构 — 迁移手册

> 分支：`refactor/ddd-domain-packages`　目标：把"按技术分层"（api/service/repository/types）重排成"按领域垂直切片"。
> 务实型：保留 Gin + GORM，不引入 entity/值对象/聚合根那套重武器。

## 目标结构

```
internal/
  <domain>/            # 单一 Go package <domain>
    handler.go         # 原 api/v1/<domain>.go
    service.go         # 原 service/<domain>*.go
    repo.go            # 原 repository/db/dao/<domain>.go
    model.go           # 原 repository/db/model/<domain>.go
    dto.go             # 原 types/<domain>.go
  shared/
    response/          # ErrorResponse（原 api/v1/common.go）
    outbox/            # 跨领域事件发件箱（原 dao+model outbox）  [待迁移]
  migrate/             # 组合包：AutoMigrate 全量表，import 所有领域 model
```

**保持原位（横切关注点，不属于任何领域）**：`consts/ pkg/ config/ middleware/ routes/ initialize/ cmd/ types/(公共信封) cache/ repository/{es,milvus,kafka,rabbitmq} service/{search,web3,grpc,inventory,events}`。

`repository/db/dao` 退化为**纯基础库**：仅保留 `init.go`（`_db`、`NewDBClient`、`InitMySQL`、`SetTestDB`）+ outbox（迁移前）。各领域 repo 通过 `dao.NewDBClient(ctx)` 拿连接。

## 单领域迁移配方（已在 address 验证）

对每个领域 D：
1. `mkdir -p internal/D`，`git mv` 五层文件到 `internal/D/{handler,service,repo,model,dto}.go`。
2. 五个文件 `package xxx` → `package D`。
3. **领域内自引用去前缀**：`model.D结构` → `D结构`；`dao.NewDDao` → `NewDDao`；`types.D请求` → `D请求`。
4. **共享符号保留前缀并保留 import**：`types.DataListResp/BasePage`（公共信封）、`outbox.*`、`consts.*`。
5. repo.go 的 `NewDBClient` → `dao.NewDBClient`，import `repository/db/dao`；方法接收器 `dao *XxxDao` 改名 `d` 避免与包名相撞。
6. handler.go 的 `ErrorResponse` → `response.ErrorResponse`，import `internal/shared/response`；`service.GetDSrv()` → `GetDSrv()`。
7. **跨领域引用按约定改写**：别的领域引用 D 的符号 → `D.符号`（如 `product.Product`、`product.NewProductDao`、`product.GetProductSrv`）。
8. 更新两个组合根：`internal/migrate/migrate.go`（`model.D{}` → `D.D{}` + import）、`routes/router.go`（`api.DHandler()` → `D.DHandler()` + import）。
9. `go build ./...` 必须绿；`grep` 确认无 `model.D|types.D|api.*DHandler|service.GetDSrv` 残留。
10. 顺手清注释（见 MEMORY：禁"教学/演示"、删死代码、修复名实不符的 doc 注释）。

## 依赖顺序（DAG，跨领域边极少）

- 跨领域 service 调用：**仅** `order / order_cancel / refund → promo`。
- 跨领域 model 嵌入：**仅** `favorite → {user, product}`（GORM ForeignKey）。
- service 跨领域用别人 dao：cart→product；favorite→product,user；order→product;payment/preorder→order,product,user;groupbuy→order；refund→order。
- 几乎所有领域都用 `outbox`（infra）→ **先迁 outbox 到 internal/shared/outbox**。

**建议顺序**：先 `outbox`(infra) → 上游 `user`、`product`(+product_img) → `order`(+shipping/async/cancel/consumer/state/task) → `promo` → 下游 `cart favorite payment refund preorder groupbuy skill coupon redpacket category carousel admin money notice`。
上游先迁，可在消费者仍处旧包时一次性 sed 改引用。

## 进度

- [x] Phase 1：抽 `internal/migrate`，dao 解除对 model 反向依赖（commit 70f3475）
- [x] `internal/shared/response`（commit df73569）
- [x] `address`（样板，commit df73569）
- [ ] outbox → internal/shared/outbox
- [ ] 其余 ~17 个领域
- [ ] 删除空的 `api/v1`、`service`、`repository/db/{dao,model}`、`types/<domain>` 残壳
- [ ] slide deck 路径引用同步（重构稳定后单独做）

## 备注 / 坑

- model 包混用 `github.com/jinzhu/gorm`（旧）与 `gorm.io/gorm`（新），按原文件保留，勿统一。
- `internal/shared/response` 里 `fmt.Sprintf("%s", fieldError.Field)` 是**沿用原 common.go 的既有 bug**（Field 是方法值未调用），为保持行为一致暂未改，重构收尾再定。
- 白盒测试（`package dao` 里直接用 `_db`）保持在 dao 包内，勿外迁。
