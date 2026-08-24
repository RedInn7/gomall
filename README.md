# gomall

GoMall 是一套用 Go、Gin 和 React 实现的电商教学系统，覆盖商品展示、搜索、购物车、下单、支付、清算、结算、履约、库存和营销活动。React 店面由 Go 服务托管在 `/app/`，可以直接沿着用户操作追到后端代码和资金流水。

项目文档统一使用 Markdown：按学习顺序阅读 `docs/lecture/`，跨课程共用的系统设计说明放在 `docs/architecture/`。总入口见 [`docs/README.md`](docs/README.md)。

---

## 项目定位

项目围绕真实电商交易链路设计，重点是把业务规则、系统实现和工程取舍讲清楚。

一笔订单从下单到收货要经过二十多个技术环节，每一环都回答四件事：业务要什么、系统怎么做、异常如何处理、客服如何向用户解释。代码、Markdown 讲义、自动评测和压测报告共同呈现完整实现。

适合工作一到三年的后端拿来练手，也适合准备答辩、面试，或者面试官拿来看候选人的系统设计深度。

---

## 业务全景

| 角色 | 关心的事 | gomall 提供 |
|------|---------|------------|
| **C 端用户** | 搜得到 / 买得了 / 不超卖 / 钱安全 / 出错有交代 | React 店面 / ES + Milvus 搜索 / 库存预扣 / 抢红包 / AES 金额加密 / 业务错误码 |
| **商家** | 商品怎么卖 / 哪个 SKU 缺货 / 订单怎么履约 / 钱什么时候到 | 商品上架与改价 / BossID 归属校验 / 发货 / 退款审核 / 库存告警 / 托管结算 |
| **运营 / 平台** | 大促能不能扛 / 黑产怎么挡 / GMV 受影响多少 | TokenBucket + SlidingWindow + CircuitBreaker / 双 11 异步下单削峰 |
| **客服** | 用户怒投诉时能不能给个交代 | 完整业务码表 + 客服话术（70001 限流 / 70002 熔断 / 50001 缺货 / RP-EMPTY 红包抢完）|
| **SRE / 法务** | 99.95% 可用、合规、链路追溯 | Jaeger 链路追踪 / Skywalking / 外部依赖隔离 / Web3 链上对账 |

Markdown 课程按这张业务全景拆开讲，统一从 [`docs/lecture/README.md`](docs/lecture/README.md) 进入。

---

## 系统架构

### 整体架构

客户端 → Gin Router + 中间件链（JWT / RBAC / 限流 / 熔断 / 幂等 / Jaeger）→ 按域分组的服务层 → MySQL / Redis / ES / Milvus / Web3，事件经 Outbox 旁路异步散给 RabbitMQ。核心链路同步落库收钱，搜索 / 统计 / 履约走异步，主线不被下游拖慢。

![gomall 整体架构](docs/architecture.svg)

### 领域地图（DDD 垂直切片）

20 个业务域按 bounded context 分组，每个域一个 `internal/<域>/` 包五件套内聚；`shared/outbox`、`money` 台账等横切下沉，基础设施作底座。跨域写一律经属主领域的服务方法落库，`product` 与 `search` 的环已斩为单向。

![gomall 领域地图](docs/architecture-domains.svg)

### 关键域流程图

**4 条支付通道 → 两阶段资金链** —— 内建钱包、Stripe、USDC、ETH 在支付确认时统一清算进 `merchant_escrow`，只推进订单和库存，不提前增加卖家可用余额；买家确认收货或系统自动确认后，`order.completed` 再触发托管放款。清算单、订单状态和 `(order_id, direction, biz_type)` 唯一索引共同保证幂等。

**资金全景：所有动钱路径 → 复式记账台账** —— 不止支付入账，退款、预售（定金/尾款/退款）、红包（发/抢/退）、拼团（加入/成团/散团）等每一处余额变动都在同事务内追加不可变流水。系统账户虽统一使用 `user_id=0`，但通过 `account_code` 区分外部清算、商户托管与旧系统账户；`SUM(debit)=SUM(credit)` 借贷守恒，幂等靠 `(ref_order_id, direction, biz_type)` 唯一索引。

**支付清算与结算细节** —— 钱包支付锁买家并扣款，Stripe/Web3 以外部清算账户作为资金来源，三条路径都先进入商户托管；履约完成后由独立消费者给卖家入账。结算前退款从托管退，结算后退款才从卖家退；跨渠道重复实收会进入 `payment_anomaly` 待退款审核，不会被当成普通重放吞掉。完整设计见 [`docs/architecture/PAYMENT_CLEARING_SETTLEMENT.md`](docs/architecture/PAYMENT_CLEARING_SETTLEMENT.md)。

**订单生命周期** —— 下单（库存预扣 + snowflake + Outbox）→ 支付 → 履约状态机 → 关单，RabbitMQ TTL 延迟关单 + Cron 双保险，失败消息按投递次数进 DLQ。

![订单生命周期流程](docs/flow-order.svg)

**库存两桶防超卖** —— `available` / `reserved` 两桶，Redis Lua 原子预扣加速、MySQL 为最终真相，下单预扣 / 支付提交 / 关单归还，启动从 DB 重建 + 定时对账。

![库存两桶模型](docs/flow-inventory.svg)

**搜索：LIKE → ES → 混合召回实验链路** —— 商品变更经 Outbox 增量同步到 ES；查询侧保留 ES 与 Milvus 融合排序能力，便于在商品向量索引接入后比较关键词召回与语义召回。

![搜索与增量索引流程](docs/flow-search.svg)

> 图为原生 SVG，源文件在 `docs/`；对应讲解统一收录在 Markdown 课程和架构文档中。

---

## 典型用户旅程

一个完整电商订单沿着下面的业务链路推进：

```
注册登录(讲义 01) → 浏览商品(讲义 06) → 搜索发现(讲义 07-08) → 加购、选地址、创建订单(讲义 09)
   → 支付确认(讲义 02-03 / 10-11) → 资金清算(讲义 04)
   → 商家发货 → 用户收货 / 自动确认 → 卖家结算或退款(讲义 05)
```

库存贯穿下单、支付和关单：下单预留、支付提交、取消释放，对应讲义 12。

并行业务：

- 营销活动（优惠券 / 秒杀 / 抢红包）——对应讲义与代码模块
- 流量治理（限流 / 熔断 / 削峰）——讲义 14-15
- 最终一致性（Outbox / Saga）——讲义 00（下）与架构文档
- 预售与阶段性付款——讲义 13
- 商家操作与可观测性——架构文档与对应代码模块

---

## 业务承诺（SLO）

| 业务等级 | 例子 | 可用率 | p99 延迟 |
|---------|------|--------|---------|
| P0 核心交易 | 下单 / 支付 | **99.95%**（年宕 4h22min） | < 500ms |
| P1 核心读 | 商品详情 / 订单列表 | 99.9% | < 200ms |
| P2 营销秒杀 | 抢券 / 秒杀 / 抢红包 | 99.9%（年宕 8h46min） | < 200ms |
| P3 信息类 | 轮播 / 分类 | 99.5%（年宕 1d19h） | < 1s |

实测对照：基线 `/ping` 64K RPS / p95 3.5ms，全链路 `/orders/list` 58K RPS / p95 5ms，留 5-10× 余量给 GC / 网络抖动。

当前入口统一经过全局 IP 令牌桶（100 RPS / 200 burst）。异步下单用于削平写入峰值；实际部署时应根据核心交易与普通读取的容量分别配置限流策略。

阶梯是故意压出来的：可用率每多一个 9，冗余成本近乎翻番——P3 轮播挂一会儿不影响成交，不为它堆冗余；秒杀被限流返回的是 HTTP 200 + 业务码、**不计入错误预算**，"抢光了"≠"挂了"，所以 P2 也能保 99.9%。P0 刻意停在 99.95% 而非 99.99%：单地域 + MySQL 主从下，一次主从切换或坏发布就能吃掉 52min/年 的预算，**写得出且赔得起**才是承诺。

---

## 技术难点与亮点

挑几个硬的讲：难在哪、怎么解的、压出来多少、为什么这么取舍。

### 1 · 幂等 Lua 三态机：755K 次请求 = 1 笔订单

**难**：`GET → check → SET` 两步在高并发下必有竞态（两个请求同时 GET 都拿到"未处理"）。
**解**：用 Lua 把"状态判断 + 写入响应"做成单条原子操作，三态 `init → processing → done`：
- 首次：`init` → 抢到锁开干，写中间结果
- 处理中：返回 `60002`，让客户端等
- 已完成：直接 replay 上一次响应体（responseRecorder 拦截过的）

实测：50 VU × 15s 持续打 `/orders/create` 同 Idempotency-Key → 累计 **755,033 次请求 → DB 实际 1 笔订单**。
`middleware/idempotency.go` + `repository/cache/idempotency.go`，详见 [`docs/lecture/15-middleware-transaction.md`](docs/lecture/15-middleware-transaction.md)。

### 2 · 两桶库存 + Saga 回滚：500 抢 100 零超发

**难**：抢券抢库存最容易超卖；Redis 扣成功但 DB 落库失败会导致库存"凭空消失"。
**解**：
- Redis 两桶 `available` / `reserved`，下单时 Lua 原子 `available -= n; reserved += n`
- 支付时 Lua `reserved -= n`；取消时 Lua `reserved → available`
- DB 事务失败 → defer 调 release Lua 把扣的退回

实测：1000 goroutine 抢 100 张券 → **成功数恰好 100**；Redis Lua max 136ms vs DB `SELECT FOR UPDATE` max 453ms。
`repository/cache/inventory.go` 4 个 Lua 脚本 + `internal/order/cancel.go` Saga 回滚。

### 3 · Outbox + 协同 Saga：解双写问题

**难**：业务写 DB + 发 MQ 通知下游，两步不可能同时成功（DB 成功 / MQ 失败 = 下游漏消息；反之 = DB 没改但下游已动）。
**解**：Transactional Outbox 模式
- 业务 tx 内同时写主表 + outbox 行（保证原子）
- 单独 publisher 进程轮询 outbox → 发 MQ → mark sent / dead
- 状态机 `pending → sent → dead`，至少一次语义，下游必须幂等

`internal/shared/outbox/publisher.go` + `internal/shared/outbox/repo.go`。事故案例：fix/init-log-order-before-rmq —— InitLog 必须在 RMQ 之前否则启动 panic。

### 4 · RMQ TTL + Cron 双保险关单：单一不可靠

**难**：30 分钟未付款自动关单，单靠 RMQ 延迟队列 → 进程重启 / 消息丢 = 漏关；单靠 Cron 5min 跑 → 实时性差。
**解**：双保险
- 下单时发 RMQ TTL 延迟消息（精确到秒）
- Cron 每 5min 兜底扫超时 UnPaid
- 共用 `CancelUnpaidOrder` 入口，通过条件 UPDATE 兜底幂等（至少 4 个调用方：RMQ / Cron / 客服 / 用户）

`internal/order/cancel.go` + `internal/order/task.go` + `initialize/cron.go`。

### 5 · 抢红包二倍均值法：拆包公平 + 总额精确

**难**：N 个红包总额固定，每份必须 ≥ 0.01、随机有惊喜、最后一份不能超额。
**解**：二倍均值法 —— 第 i 份从 `[1, 2*avg-1]` 随机，avg = `remain / (count-i)`，最后一份兜底剩余
- 拆包是创建红包时一次性算好的 `[]int64` 数组 → RPUSH 进 Redis LIST
- 抢红包 Lua：`HEXISTS 防重领 + LPOP 拿一份 + HSET 记账`，全原子

`SplitRedPacket` + `claimRedPacketScript` 在 `repository/cache/redpacket.go`。

### 6 · SlidingWindow ZSet：比 fixed window 准

**难**：fixed window 限流在窗口边界有 2× 突发（59 秒 + 0 秒并发）。
**解**：Redis ZSet score = 时间戳，Lua 一次性 `ZREMRANGEBYSCORE 清过期 + ZCARD 数当前 + ZADD 加新` → 精确滑动窗口
- 秒杀场景 `Scope:seckill, Limit:3, Window:1s, ByUser:true`
- 30VU × 15s 实测：通过 46 次（期望 45）、限流 781,624 次，**误差 2.2%**

`middleware/ratelimit.go::SlidingWindow` + `repository/cache/ratelimit.go` Lua。

### 7 · EVM 链上监听：reorg 幂等 + 断线 catch-up

**难**：以太坊链 reorg 会撤回已发出的事件，监听器重启会漏掉断线期间的事件。
**解**：
- `last_block` 持久化到 Redis，重启时 `FilterLogs(last+1, head)` 先 catch-up，再 `SubscribeFilterLogs` 实时跟
- 事件级幂等：`web3:event:{txhash}:{logindex}` SetNX TTL 72h，重复事件直接跳过
- 写 outbox `web3.payment.confirmed`，下游接现有最终一致体系
- Web3 监听作为可选初始化模块启动，不影响未配置链上支付时的订单主链路

`service/web3/listener.go::StartPaymentListener`。

### 8 · ES + Milvus Hybrid 检索实验：召回率 vs 准确率

**难**：ES 关键词搜不到"苹果手机" → "iPhone 13"，纯向量搜可能召回不准。
**解**：
- query → embedding → Milvus 拿 top-K 语义匹配（HNSW M=16 efConstruction=200 efSearch=64，768 dim）；使用前需要注入 Milvus searcher，并先建立商品向量索引
- 同时 ES 关键词搜 top-K
- 两边 min-max normalize 后 50/50 加权融合
- env `EMBEDDING_API_URL` 切换模型；未设走 SHA-256 stub 让链路跑通

`service/search/semantic.go` + `repository/milvus/product_vector.go`。

### 9 · 异步下单削峰：双 11 0 点不转圈

**难**：双 11 0 点 100k QPS 涌 `/orders/create`，同步链路 DB 顶不住 → 用户看到 5 秒转圈 → 放弃下单 → GMV 损失。
**解**：保留同步 `/orders/create` 兼容老客户端；新增异步路径
- `POST /orders/enqueue`：reserve 库存 + 写 RMQ + 立即返回 ticket（<10ms）
- `internal/order/consumer.go`：消费 MQ 慢慢落 DB（沿用同事务 + outbox）
- `GET /orders/status?ticket=`：前端轮询，0.5s × 6 次后切换"长等待"文案
- 失败 → consumer 调 release 释放 reserved（Saga）

`internal/order/async.go` + `internal/order/consumer.go` + `repository/rabbitmq/order_async.go`。

### 10 · 订单 7 态机：兼容存量 + 业务对齐

**难**：原系统只有 3 态（WaitPay/Paid/Cancelled），且 consts 里两套常量数值重叠（`Cancelled=3` = `OrderTypeShipping=3` 撞值不同义，是 bug）。
**解**：升到业界标配 7 态：`WaitPay → WaitShip → WaitReceive → Completed | Closed | Refunding | Refunded`
- 数值兼容：1/2/3 保留原义（存量数据零迁移），4-7 新增
- 旧名 `UnPaid/Paid/Cancelled` 标 Deprecated 别名保留
- 状态机校验 `CanTransition(from, to)` + DAO 条件 UPDATE 双兜底幂等
- 7 天 Cron 自动确认收货
- 单测 17 例覆盖所有合法 / 非法转换

`consts/order.go` + `internal/order/state.go` + `internal/order/shipping.go` + `internal/refund/service.go`。

### 11 · 外部依赖隔离：交易、搜索和异步任务独立启动

RMQ、ES、Web3 和 Milvus 分别由 `cmd/main.go` 中的 `tryInitX` 入口按配置初始化。未配置可选组件时，商品、订单和支付等同步交易能力仍可沿各自的数据路径启动；当前这些组件与主服务运行在同一进程中。

---

## 技术覆盖

### 高并发与一致性

| 能力 | 关键代码 |
|------|---------|
| 幂等中间件（Idempotency-Key + Redis Lua 状态机） | `middleware/idempotency.go` · `repository/cache/idempotency.go` |
| 防超发（Redis Lua 两桶库存 available / reserved） | `repository/cache/inventory.go` · `service/inventory/syncer.go` |
| Cache Aside + 延迟双删 + SETNX 回源锁 | `repository/cache/product.go` |
| Transactional Outbox + 协同式 Saga | `internal/shared/outbox/publisher.go` · `internal/shared/outbox/repo.go` · `internal/order/cancel.go` |
| RMQ TTL 延迟队列 + Cron 双保险关单 | `repository/rabbitmq/order_delay.go` · `internal/order/task.go` · `initialize/cron.go` |
| 异步下单（削峰填谷 enqueue → consumer → ticket polling） | `internal/order/async.go` · `internal/order/consumer.go` |
| HTTP cache（ETag + Cache-Control + 304） | `middleware/httpcache.go` |
| 雪花算法订单号 | `pkg/utils/snowflake/` |
| 订单状态机 7 态（WaitPay → WaitShip → WaitReceive → Completed / Closed / Refunding / Refunded） | `consts/order.go` · `internal/order/state.go` · `internal/order/shipping.go` · `internal/refund/service.go` |

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
| admin bootstrap（首位管理员冷启动） | `internal/admin/service.go` |
| AES 金额加密 + 支付密码 | `internal/payment/service.go` · `pkg/utils/encryption/` |

### 搜索 / AI

| 能力 | 关键代码 |
|------|---------|
| ES 关键词检索 + outbox 增量索引 consumer | `service/search/service.go` · `service/search/indexer.go` · `repository/es/` |
| Milvus 向量存储与 HNSW 索引实现（768 dim） | `repository/milvus/product_vector.go` |
| Hybrid 融合与 embedding 查询链路 | `service/search/semantic.go` · `service/search/embedding.go` |

### Web3 支付

| 能力 | 关键代码 |
|------|---------|
| Escrow 智能合约（Solidity ≥ 0.8.20） | `pkg/web3/contracts/Escrow.sol` |
| EIP-191 personal_sign 验签 + nonce 防重放 | `pkg/web3/signature/verify.go` · `repository/cache/web3.go` |
| EVM PaymentConfirmed event 链上监听（catch-up + last_block 持久化 + reorg 幂等） | `service/web3/listener.go` |
| 钱包签名支付 API + 链下 → 链上对账 | `internal/payment/service_crypto.go` · `internal/payment/handler_crypto.go` |

### 营销活动

| 能力 | 关键代码 |
|------|---------|
| 优惠券（Lua 限领 + 防重复） | `repository/cache/coupon.go` · `internal/coupon/service.go` |
| 秒杀（独立 skill_product + SlidingWindow 3/s/用户） | `internal/skill/service.go` |
| 抢红包（二倍均值法 Lua + Saga 入账 + Cron 过期回收） | `repository/cache/redpacket.go` · `internal/redpacket/service.go` |

### 可观测性

| 能力 | 关键代码 |
|------|---------|
| Jaeger 链路追踪 | `middleware/track.go` · `pkg/utils/track/` |
| Skywalking-go agent | `Makefile` · `cmd/main.go` |
| 结构化日志（logrus） | `pkg/utils/log/` |

### 外部依赖隔离

RMQ、ES、Web3 与 Milvus 使用独立初始化入口，同步交易链路和异步能力可以分别运行（参 `cmd/main.go::tryInitX`）。

---

## 教学文档

教学材料以 Markdown 为唯一源稿，不再同时维护 TeX 和生成 PDF：

- [课程总目录](docs/lecture/README.md)：按录制和学习顺序阅读；
- [架构文档](docs/README.md#架构与设计)：查看普通订单的清算、结算与退款；
- [文档总入口](docs/README.md)：不知道从哪里开始时只看这一页。

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
# 启动数据库与缓存
docker compose up -d mysql redis

# 启动 Go 服务
SNOWFLAKE_ALLOW_DEFAULT=true go run ./cmd
```

服务启动后访问：

- 店面：`http://127.0.0.1:5003/app/`
- 健康检查：`http://127.0.0.1:5003/api/v1/ping`

前端源码位于 `web/`。修改页面后重新构建：

```bash
cd web
npm ci
npm run build
```

构建 Go 二进制：

```bash
go mod tidy
go build -o main ./cmd
SNOWFLAKE_ALLOW_DEFAULT=true ./main
```

普通构建默认不带 SkyWalking。需要探针时，先初始化子模块并单独构建 Agent。

### Makefile

```bash
make                # 编二进制并自动运行
make build          # 仅编二进制
make tools          # 编 SkyWalking Agent（需要先初始化子模块）
make build-agent    # 使用 SkyWalking Agent 构建
make env-up         # 拉起 docker-compose 中的完整依赖与观测组件
make env-down       # 关依赖
make docker-up      # 容器化拉起项目
make docker-down    # 关容器
```

第一次跑：

```bash
# 普通本地开发只启动必要依赖
docker compose up -d mysql redis
make

# 需要 SkyWalking 时再执行
git submodule update --init --recursive
make tools build-agent
```

### 可选集成

本地启动 MySQL 和 Redis 就能体验商品、购物车、订单与钱包支付主链路；按需接入下面的组件，可以继续演示异步消息、搜索、链上支付和语义召回。

| 配置 | 启用的能力 |
|-----|-----------|
| RabbitMQ | Outbox 事件发布与订单延迟关单 |
| ElasticSearch | 商品关键词检索与增量索引 |
| `WEB3_RPC_URL` / `WEB3_ESCROW_ADDR` | Web3 托管合约监听与链上支付确认 |
| `MILVUS_ADDR` | 连接 Milvus 并创建向量集合；还需注入 searcher 和建立商品向量索引 |
| `EMBEDDING_API_URL` | 为查询生成 embedding；商品向量需要另行建立索引 |

---

## 项目结构

按领域垂直切片（DDD 分包）：每个业务域一个 `internal/<域>/` 包，handler / service / repo / model / dto 五件套同包内聚；基础设施与横切关注点保持独立目录。

```
gomall
├── cmd                 # main 入口 + 启动顺序
├── config              # 配置加载
├── consts              # 全局常量（订单状态机 / 业务码 等）
├── docs
│   ├── architecture    # 资金、订单、搜索等跨课程架构说明
│   └── lecture         # Markdown 课程讲义与唯一学习顺序
├── initialize          # cron / inventory / outbox / search / web3 启动
├── internal            # 领域代码（每域一包：handler / service / repo / model / dto）
│   ├── address · admin · carousel · cart · category · coupon · favorite
│   ├── groupbuy · idempotency · money · notice · order · payment · preorder
│   ├── product · promo · redpacket · refund · skill · user
│   ├── shared
│   │   ├── response    # 统一错误响应
│   │   └── outbox      # 事务发件箱（model / repo / publisher）
│   └── migrate         # AutoMigrate 组合包，聚合全部领域 model
├── middleware          # cors / jwt / rbac / track / idempotency / ratelimit / circuitbreaker / httpcache
├── pkg
│   ├── web3/contracts  # Solidity 合约（Web3 Escrow）
│   ├── e               # 业务错误码
│   ├── utils           # ctl / email / encryption / jwt / log / snowflake / track / upload
│   └── web3            # Web3 escrow / signature 工具
├── proto               # gRPC proto
├── repository
│   ├── cache           # Redis（coupon / idempotency / inventory / product / ratelimit / redpacket / web3 / key）
│   ├── db
│   │   └── dao         # DB 基座（连接 / InitMySQL；领域 repo / model 在 internal/<域>/）
│   ├── es              # ElasticSearch
│   ├── milvus          # Milvus 向量库
│   └── rabbitmq        # RMQ（domain / order_async / order_delay）
├── routes              # 路由 + 中间件链
├── service             # 横切服务子包（events / grpc / inventory / search / web3）
├── static              # 静态资源
├── stressTest          # k6 压测脚本 + REPORT.md
├── types               # 公共信封（BasePage / DataListResp）
└── web                 # React + Vite 店面，构建产物由 Go 托管在 /app/
```

---

## 配置

`config/locales/config.yaml`（拷贝 `config.example.yaml` 改）。

```yaml
system:
  domain: mall
  appEnv: "dev"
  httpPort: ":5003"
  host: "localhost"
  uploadModel: "local"        # 或 oss

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
  redisPassword: ""
  redisDbName: 4

es:
  EsHost: 127.0.0.1
  EsPort: 9200

rabbitMq:
  rabbitMQHost: localhost
  rabbitMQPort: 5672

encryptSecret:
  jwtSecret: "FanOne666Secret"
  sessionSecret: "SessionSecret"
  moneySecret: "MoneySecret"
  emailSecret: "EmailSecret"
  phoneSecret: "PhoneSecret"
```

完整字段看 `config/locales/config.example.yaml`。

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
| golang-jwt/jwt | v4.5.2 |
| crypto | v0.48.0 |
| logrus | v1.9.3 |
| go-ethereum | v1.17.3 |
| milvus-sdk-go | v2.4.2 |
| rabbitmq/amqp091-go | v1.8.1 |
| elastic/go-elasticsearch | v0.0.0 |
| Skywalking-go | v0.0.0-20230511 |
