# gomall

Go + Gin 实现的电商后端，覆盖从浏览 / 下单 / 支付 / 履约到营销活动 / Web3 支付 / AI 检索的完整业务链路。

每个模块都配套**真实压测数据**（`stressTest/REPORT.md`）+ **业务侧 Beamer Slide**（`docs/slides/`，12 份 580+ 页）+ **博客长文**（`docs/blog/`）。

---

## 技术覆盖

### 高并发与一致性

| 能力 | 关键代码 |
|------|---------|
| 幂等中间件（Idempotency-Key + Redis Lua 状态机） | `middleware/idempotency.go` · `repository/cache/idempotency.go` |
| 防超发（Redis Lua 两桶库存 available / reserved） | `repository/cache/inventory.go` · `service/inventory/syncer.go` |
| Cache Aside + 延迟双删 + SETNX 回源锁 | `repository/cache/product.go` |
| Transactional Outbox + 协同式 Saga | `service/outbox/publisher.go` · `repository/db/dao/outbox.go` · `service/order_cancel.go` |
| RMQ TTL 延迟队列 + Cron 双保险关单 | `repository/rabbitmq/order_delay.go` · `service/order_task.go` · `initialize/cron.go` |
| 异步下单（削峰填谷 enqueue → consumer → ticket polling） | `service/order_async.go` · `service/order_consumer.go` |
| HTTP cache（ETag + Cache-Control + 304） | `middleware/httpcache.go` |
| 雪花算法订单号 | `pkg/utils/snowflake/` |
| 订单状态机 7 态（WaitPay → WaitShip → WaitReceive → Completed / Closed / Refunding / Refunded） | `consts/order.go` · `service/order_state.go` · `service/order_shipping.go` · `service/refund.go` |

### 流量治理

| 能力 | 关键代码 |
|------|---------|
| 令牌桶（全局 IP 维度 100 RPS / 200 burst） | `middleware/ratelimit.go::TokenBucket` |
| 滑动窗口（用户维度 Redis ZSet Lua 实现） | `middleware/ratelimit.go::SlidingWindow` · `repository/cache/ratelimit.go` |
| 三态熔断器（Closed / Open / HalfOpen） | `middleware/circuitbreaker.go` |

### 鉴权

| 能力 | 关键代码 |
|------|---------|
| JWT 双 token（access 24h + refresh 10d 静默续期） | `middleware/jwt.go` · `pkg/utils/jwt/` |
| RBAC + 30s sync.Map 内存缓存 + 显式失效 | `middleware/rbac.go` |
| admin bootstrap（首位管理员冷启动） | `service/admin.go` |
| AES 金额加密 + 支付密码 | `service/payment.go` · `pkg/utils/encryption/` |

### 搜索 / AI

| 能力 | 关键代码 |
|------|---------|
| ES 关键词检索 + outbox 增量索引 consumer | `service/search/service.go` · `service/search/indexer.go` · `repository/es/` |
| Milvus 向量库 + HNSW 索引（768 dim） | `repository/milvus/product_vector.go` |
| 语义搜索 hybrid（ES + Milvus 50/50 加权 + min-max normalize） | `service/search/semantic.go` · `service/search/embedding.go` |

### Web3 支付

| 能力 | 关键代码 |
|------|---------|
| Escrow 智能合约（Solidity ≥ 0.8.20） | `contracts/Escrow.sol` |
| EIP-191 personal_sign 验签 + nonce 防重放 | `pkg/web3/signature/verify.go` · `repository/cache/web3.go` |
| EVM PaymentConfirmed event 链上监听（catch-up + last_block 持久化 + reorg 幂等） | `service/web3/listener.go` |
| 钱包签名支付 API + 链下 → 链上对账 | `service/payment_crypto.go` · `api/v1/paydown_crypto.go` |

### 营销活动

| 能力 | 关键代码 |
|------|---------|
| 优惠券（Lua 限领 + 防重复） | `repository/cache/coupon.go` · `service/coupon.go` |
| 秒杀（独立 skill_product + SlidingWindow 3/s/用户） | `service/skill_goods.go` |
| 抢红包（二倍均值法 Lua + Saga 入账 + Cron 过期回收） | `repository/cache/redpacket.go` · `service/redpacket.go` |

### 可观测性

| 能力 | 关键代码 |
|------|---------|
| Jaeger 链路追踪 | `middleware/track.go` · `pkg/utils/track/` |
| Skywalking-go agent | `Makefile` · `cmd/main.go` |
| 结构化日志（logrus） | `pkg/utils/log/` |

### 静默降级

启动期 RMQ / ES / Web3 / Milvus 任一不可用，主流程不阻塞（参 `cmd/main.go::tryInitX`）。

---

## 业务侧 Deck（12 份，按业务域拆）

| # | 主题 | 文件 |
|---|------|------|
| 01 | 用户与鉴权 | `docs/slides/01-user-auth.{tex,pdf}` |
| 02 | 商品展示 | `docs/slides/02-product-display.{tex,pdf}` |
| 03 | 商品搜索 | `docs/slides/03-product-search.{tex,pdf}` |
| 04 | 购物车 → 下单 | `docs/slides/04-cart-to-order.{tex,pdf}` |
| 05 | 支付（法币） | `docs/slides/05-payment.{tex,pdf}` |
| 06 | Web3 支付 | `docs/slides/06-payment-web3.{tex,pdf}` |
| 07 | 库存与防超发 | `docs/slides/07-inventory.{tex,pdf}` |
| 08 | 营销活动 | `docs/slides/08-marketing.{tex,pdf}` |
| 09 | 订单生命周期（7 态） | `docs/slides/09-order-lifecycle.{tex,pdf}` |
| 10 | 流量治理 | `docs/slides/10-traffic-governance.{tex,pdf}` |
| 11 | Outbox 与一致性 | `docs/slides/11-consistency.{tex,pdf}` |
| 12 | 商家后台 + 可观测性 | `docs/slides/12-merchant-ops.{tex,pdf}` |

每份 deck 约 40-50 页、≥ 6 TikZ、≥ 18 处 `file:line` 真实代码引用、≥ 5 段关键代码 + 逐行讲解、≥ 2 处来自 `stressTest/REPORT.md` 的实测数字。

体例：业务 70% / 代码 30%，**业务困局 → 流程 → 关键代码 → 业务码 / 客服话术 / 各角色视角 → 路线图**。

编译：

```bash
cd docs/slides
./build.sh             # 全量
./build.sh 03          # 只编 deck 03
./build.sh --master    # 合订本 master.pdf
```

配套博客（11 篇）：`docs/blog/01-*.md` ~ `11-*.md`。
特性现状路线图：`docs/architecture/feature-matrix.md`。

---

## 真实压测数据（`stressTest/REPORT.md`）

| 链路 | RPS | p95 | 备注 |
|------|----:|----:|------|
| `/ping` 基线 | 64,254 | 3.51ms | 裸 gin 链路上限 |
| `/product/show`（无缓存 + PK 查询）| 62,226 | 3.01ms | 接近裸 ping |
| `/orders/list`（游标分页 + 缓存）| 58,406 | 5.00ms | PR #38 7000× 提升 |
| `/orders/create`（幂等 50 VU × 15s）| 50,319 | 2.33ms | **755,033 次请求 → 1 笔订单** |
| `/coupon/claim` Redis Lua | 51,362 | 3.52ms | 500 抢 100 张零超发 |
| `/coupon/claim` DB FOR UPDATE | 50,142 | 3.65ms | max 453ms（vs Lua max 136ms）|
| `/skill_product/skill` + SlidingWindow | 52,082 | 1.24ms | 30VU 通过 46 / 限流 781,624 / 误差 2.2% |
| `/orders/old/list`（旧深分页 反例）| 8.3 | 15.95s | OFFSET 1999999 必扫全表 |
| `/product/list`（COUNT 全表 反例） | 24.5 | 2.50s | 956K 行 product 表 |

数据规模：order 表 ~6M 行 / 653 MB，product 表 ~956K 行 / 364 MB。

---

## 运行

### 手动

```bash
cd ./cmd && go run main.go
```

或二进制：

```bash
go mod tidy
cd ./cmd && go build -o ../main && ./main
```

手动方式不带 Skywalking。要带 Skywalking 看 `Makefile`。

### Makefile

```bash
make tools          # 编 Skywalking Agent 二进制
make                # 编二进制并自动运行
make build          # 仅编二进制
make env-up         # 拉起依赖（MySQL / Redis / RMQ / ES）
make env-down       # 关依赖
make docker-up      # 容器化拉起项目
make docker-down    # 关容器
```

第一次跑：

```bash
# 1. 调 Makefile 顶部的 ARCH / OS
make env-up tools build
./main
```

### 静默降级 env vars

| env | 不设的话 |
|-----|---------|
| 自动接 RMQ | 跳过 outbox publisher + 关单延迟队列，Cron 仍跑 |
| 自动接 ES | 商品搜索退化到 DB 路径 |
| `WEB3_RPC_URL` / `WEB3_ESCROW_ADDR` | Web3 listener 不启动 |
| `MILVUS_ADDR` | Milvus 不连，语义检索关闭，ES 关键词仍可用 |
| `EMBEDDING_API_URL` | embedding 走 SHA-256 stub（接口可跑） |

---

## 项目结构

```
gomall
├── api/v1              # HTTP handler（按业务域分文件）
├── cmd                 # main 入口 + 启动顺序
├── config              # 配置加载
├── consts              # 全局常量（订单状态机 / 业务码 等）
├── contracts           # Solidity 合约（Web3 Escrow）
├── doc                 # 项目说明 / 截图
├── docs
│   ├── architecture    # feature matrix / 路线图
│   ├── blog            # 11 篇博客长文
│   ├── slides          # 12 份 Beamer Deck（v2 expanded, 580+ 页）
│   └── slides-pipeline # 历史需求 / 方案 / 验收文档
├── initialize          # cron / inventory / outbox / search / web3 启动
├── middleware          # cors / jwt / rbac / track / idempotency / ratelimit / circuitbreaker / httpcache
├── pkg
│   ├── e               # 业务错误码
│   ├── utils           # ctl / email / encryption / jwt / log / snowflake / track / upload
│   └── web3            # Web3 escrow / signature 工具
├── proto               # gRPC proto
├── repository
│   ├── cache           # Redis（coupon / idempotency / inventory / product / ratelimit / redpacket / web3 / key）
│   ├── db
│   │   ├── dao         # GORM dao 层（含 outbox）
│   │   └── model       # GORM model
│   ├── es              # ElasticSearch
│   ├── kafka           # Kafka（备用）
│   ├── milvus          # Milvus 向量库
│   └── rabbitmq        # RMQ（domain / order_async / order_delay）
├── routes              # 路由 + 中间件链
├── service             # 业务逻辑（含 events / grpc / inventory / outbox / search / web3 子包）
├── static              # 静态资源
├── stressTest          # k6 压测脚本 + REPORT.md
└── types               # 请求 / 响应 DTO
```

---

## 配置

`config/locales/config.yaml`（拷贝 `config.yaml.example` 改）。

```yaml
system:
  domain: mall
  env: "dev"
  HttpPort: ":5001"
  Host: "localhost"
  UploadModel: "local"        # 或 oss

mysql:
  default:
    dialect: "mysql"
    dbHost: "127.0.0.1"
    dbPort: "3306"
    dbName: "mall_db"
    userName: "mall"
    password: "123456"

redis:
  redisHost: 127.0.0.1
  redisPort: 6379
  redisPassword: 123456

es:
  EsHost: 127.0.0.1
  EsPort: 9200

rabbitMq:
  rabbitMQHost: localhost
  rabbitMQPort: 5672

encryptSecret:
  jwtSecret: "FanOne666Secret"
  emailSecret: "EmailSecret"
  phoneSecret: "PhoneSecret"
```

完整字段看 `config/locales/config.yaml.example`。

---

## Postman

`doc/` 下有截图导入步骤：

1. 打开 Postman → Import → 选择 `doc/` 下的接口文件
2. 在 Collection（gin-mall）的 Variables 中加 `url` = `localhost:5001/api/v1/`
3. 跑接口

---

## 主要依赖

| 名称 | 版本 |
|------|------|
| golang | 1.25.0 |
| gin | v1.9.0 |
| gorm | v1.25.0 |
| mysql driver | v1.5.0 |
| redis | v9.0.4 |
| dbresolver | v1.4.1 |
| jwt-go | v3.2.0 |
| crypto | v0.48.0 |
| logrus | v1.9.3 |
| go-ethereum | v1.17.3 |
| milvus-sdk-go | v2.4.2 |
| rabbitmq/amqp091-go | v1.8.1 |
| elastic/go-elasticsearch | v0.0.0 |
| Skywalking-go | v0.0.0-20230511 |

---

## 开源合作

欢迎 PR。规则：

1. 从最新版本切分支，不要直接合 main
2. 自测通过再提 PR
3. CR 通过后合 main
