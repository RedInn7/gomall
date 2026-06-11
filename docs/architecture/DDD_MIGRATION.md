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
- [x] 叶子领域 8 个：`address carousel cart category`（批1）+ `money admin notice favorite`（批2）
- [x] 带 cache 领域 3 个：`coupon redpacket skill`（批3，cache 留 repository/cache，cron 重接线）
- [x] **hub `user`**（被 13 个文件引用）：全局 sed 改名 model.User/dao.NewUserDao→user.*，逐文件补 import、清无用 import；修局部变量 user 遮蔽包名
- [x] **hub `product`**（被 21 个文件引用，最难，有 product↔search 环）：
  - 先斩环：把 ProductSearch 编排 + 两个搜索 handler 迁到 service/search 包（product 不再 import search/es），只留 search→product 单向边
  - 再迁 product 8 件套到 internal/product（agent 做受体改名等细活），全局 sed 改名 model.Product(Img)/dao.NewProduct*Dao/types.Product*→product.*
  - 修 payment.go 局部变量 product 遮蔽包名（→prod）

### 剩余（order 簇，互相耦合，建议顺序）
1. [x] `promo` 已完成（含 CartItem/ErrPromoBudgetExhausted 内聚；两个白盒测试迁入；repo 测试自带 initPromoTestDB）
2. `order` 核心（order + order_async/cancel/consumer/shipping/state/task 共 7 个 service 文件 + dao + model + types*2 + api*2），被 payment/refund/preorder/groupbuy/cancel 引用 model.Order/dao.NewOrderDao
3. order 的下游：`payment`(payment+crypto)、`refund`、`preorder`(routes)、`groupbuy`(routes)
4. `idempotency`（api handler，偏共享，考虑放 shared 或 order）
5. 收尾：删空的 api/v1（连 common.go→已无 handler 用就删）、service、repository/db/{dao,model} 残壳；最后抽 outbox→internal/shared/outbox

### Hub 迁移办法（user 已用，product/order 照此）
1. agent/手工先迁该域 5 件套到 internal/<hub>，de-prefix 自有符号。
2. 全局 sed：`model.<Sym>`/`dao.New<Sym>Dao`→`<hub>.<Sym>`（用 `\b` 边界，别误伤 model.UserCoupon 等）。
3. `go build ./...` 看报错，逐文件加 `internal/<hub>` import、删变为无用的 dao/model import（无 goimports，手工）。
4. 注意**局部变量与包同名遮蔽**（favorite/preorder_test 里的 `user` 变量 → 改 curUser/buyer）；`&pkg.Type{}` 在局部变量声明后会被解析成字段访问而报错。
5. 白盒 `_test.go`（如 model/user_test.go）随领域迁走，改 package + 修相对路径（深度变了：repository/db/model 的 ../../../ → internal/user 的 ../../）。
6. 跨测试共享 helper（如 service 包的 initLogForTest）若随某域迁走，要在原包补一份。

### 教训
- 后台 agent 可能中途 API 掉线（FailedToOpenSocket）。hub 这种"必须全量 build 绿"的大改交给单 agent 时，**完成后务必自己 build/grep 复核**；中断了就接力手工补全（handler/router/import）。
- **sed/re.sub 改名坑**：用 Python `re.sub` 时 `GetPromoSrv()` 的 `()` 是正则空分组，会把 `GetPromoSrv().X` 改成 `promo.GetPromoSrv()().X`（双括号）。改函数调用名要么用 `str.replace`，要么把 `()` 写成 `\(\)` 或干脆只匹配函数名 `GetPromoSrv`。
- 跨包共享的 sentinel error（如 `dao.ErrPromoBudgetExhausted`）随领域迁走后变 `promo.ErrPromoBudgetExhausted`，consumer 里 `errors.Is(err, ErrPromoBudgetExhausted)` 的裸引用也要改。
- 白盒 repo 测试若依赖 dao 包私有 `_db`/`initDBForTest`，迁入领域包后要自带一个 init helper（用 `dao.NewDBClient(ctx)==nil` 判活 + `dao.InitMySQL` + `AutoMigrate(&本域model{})`，skip-if-no-mysql）。
- [ ] **outbox 放到最后**：各领域迁移期间继续以 `dao.NewOutboxDao`/`model.OutboxEvent` 限定引用；待 model/dao 清空后再抽到 internal/shared/outbox
- [ ] 剩余领域（顺序敏感，会回touch已迁移领域）：
  - 叶子但带 cache：`coupon`(cache/coupon.go)、`redpacket`(consumer+task+cache/redpacket.go)、`skill`(skill_goods，用 product)
  - 中间：`promo`（被 order/refund 调用 + 自带 routes/promo_routes.go）
  - hub（很多领域依赖，迁移时要 sed 改所有消费者）：`product`(+product_img,cache/product.go,语义检索)、`user`
  - order 簇：`order`(7+ service 文件)、`payment`(+crypto)、`refund`、`preorder`(routes)、`groupbuy`(routes)
- [ ] 删除空的 `api/v1`、`service`、`repository/db/{dao,model}`、`types/<domain>` 残壳；删 `api/v1/common.go`（迁完后所有 handler 都用 shared/response）
- [ ] slide deck 路径引用同步（重构稳定后单独做）

### 已踩坑补充
- router.go 里有局部路由组变量与领域同名（如 `admin := authed.Group("/admin")`）→ 该领域包用别名引入（`adminapi`）。后续 `order/product/user/promo/preorder/groupbuy` 若也有同名路由组变量，同样处理。
- cache 耦合领域（coupon/redpacket/product）注意 service↔cache 不要成环：cache 文件目前不 import model，迁移时让 cache 保持在 repository/cache 包并按需 import internal/<domain>，但领域 service 不要反过来被 cache 依赖成环。
- 并行 agent 各自只动自己 internal/<domain>/ 五件套，**不碰 router.go / migrate.go**；由编排者统一改这两个组合根并跑 `go build ./...`。

## 备注 / 坑

- model 包混用 `github.com/jinzhu/gorm`（旧）与 `gorm.io/gorm`（新），按原文件保留，勿统一。
- `internal/shared/response` 里 `fmt.Sprintf("%s", fieldError.Field)` 是**沿用原 common.go 的既有 bug**（Field 是方法值未调用），为保持行为一致暂未改，重构收尾再定。
- 白盒测试（`package dao` 里直接用 `_db`）保持在 dao 包内，勿外迁。
