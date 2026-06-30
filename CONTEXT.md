# gomall 工程布局约定（CONTEXT）

> 本文件只回答一个问题：**新写的代码该放哪个目录？**
> 评审发现历史上是"按手感"放置——同样是业务逻辑，`order` 落在 `internal/`、`inventory` 却落在 `service/`。这里把隐含规则显式化，避免随团队/功能扩张演变成大泥球（Big Ball of Mud）。

## 落点决策树

写一段新代码时从上往下匹配，命中第一条即落点：

1. **是某个有界业务域、拥有自己的数据表、对外有 HTTP handler 吗？**
   → `internal/<domain>/`（order、payment、cart、coupon、redpacket …）
   域内固定分层：`handler.go`(HTTP 入口) · `service.go`(业务编排) · `repo.go`(DAO，仅本域表) · `model.go`(GORM 模型) · `dto.go`(请求/响应) · `routes.go`(路由注册) · `consumer.go`(MQ 消费，按需)

2. **是跨多个业务域、或本身不拥有业务表的"服务"吗？**
   → `service/<name>/`
   判据：被两个以上 internal 域依赖，或它不 own 任何业务表。
   现有：`search`(跨商品读模型) · `inventory`(被 order/skill/groupbuy 共用) · `web3`(链上监听) · `events`(领域事件契约) · `grpc`(传输)

3. **是基础设施客户端 / 存储适配器吗？**
   → `repository/<tech>/`（db、cache、es、milvus、rabbitmq）
   只封装"怎么连、怎么读写"，不含业务规则。

4. **是与业务无关、可被任意项目复用的通用库吗？**
   → `pkg/<name>/`（utils、snowflake、web3/signature、web3/escrow 绑定、web3/contracts Solidity 源）
   **pkg 内禁止 import internal/。**

5. 其余固定位置：`consts/`(常量与状态码) · `middleware/`(Gin 中间件) · `config/`(配置) · `initialize/`(启动装配) · `cmd/`(入口) · `routes/`(顶层路由聚合)

## 跨域依赖规则

- `internal/<A>` 需要 `internal/<B>` 的能力时，**优先在 A 内定义"消费方接口"并注入**（参照 `internal/order` 的 `PromoCalculator` + `NewOrderSrv` 装配缝），而非直接 import B 的具体 DAO。好处：B 的实现演进不波及 A，且 A 能用替身做单元测试。
- 依赖只能从外向内：`pkg/`、`repository/` **不得** import `internal/`。
- 领域事件 payload 统一放 `service/events`，禁止 A、B 各自定义同一事件结构。

## 命名注意

- `pkg/web3/contracts/` 是 **Solidity 智能合约源码**（`Escrow.sol`），**不是** Go interface 契约。
  它曾叫顶层 `contracts/`，会被 Go 工程师误当成"接口契约目录"，已迁移至 `pkg/web3/` 下与 Go 绑定 `pkg/web3/escrow/` 同根，消除歧义。
