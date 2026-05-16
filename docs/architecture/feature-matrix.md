# gomall 特性矩阵（截至 2026-05-16）

围绕"高并发 + 现代化"两条线，整理 gomall 已有 / 缺失 / 在做 的 feature。
按生产口径描述，不包含项目定位类的形容。

下文表格的 PR / 分支命名沿用现有约定：`feat/*` 为业务特性分支，`docs/*` 为
文档分支，主干为 `main`。每条 feature 都给出关键代码入口与配套 doc（deck 编号
对应 `docs/slides/`，blog 对应 `docs/blog/`）。

## 1. 高并发与系统设计

| Feature | 状态 | 关键代码 | 配套 doc |
|---------|------|----------|---------|
| 雪花算法订单号 | ✅ | `pkg/utils/snowflake/snowflake.go` | — |
| 库存预热 + Redis Lua 原子扣减（两桶） | ✅ | `repository/cache/inventory.go` ReserveStock/Commit/Release | deck 11 |
| IP 维度令牌桶限流 | ✅ | `middleware/ratelimit.go` TokenBucket | deck 05 / deck 10 |
| 用户维度滑动窗口限流 | ✅ | `middleware/ratelimit.go` SlidingWindow | deck 05 / deck 10 |
| 三态熔断器 | ✅ | `middleware/circuitbreaker.go` | deck 05 |
| 幂等中间件 (Idempotency-Key) | ✅ | `middleware/idempotency.go` | deck 01 |
| Outbox 模式 (PR #57) | ✅ | `service/outbox/publisher.go` | deck 04 |
| Saga 取消订单 (PR #58) | ✅ | `service/order_cancel.go` | deck 11 |
| RMQ TTL 延迟队列 (30min 关单) | ✅ | `repository/rabbitmq/order_delay.go` | deck 15 |
| Cron 兜底关单 | ✅ | `service/order_task.go`, `initialize/cron.go` | deck 15 |
| 缓存一致性 (Cache Aside + 双删) | ✅ | `repository/cache/product.go` | deck 03 |
| 游标分页 (订单列表) | ✅ | `repository/db/dao/order.go` | deck 13 |
| 商品搜索 ES + outbox 增量索引 (PR #59) | ✅ | `service/search/`, `repository/es/` | deck 12 |
| **动静分离 / ETag + Cache-Control** | 🚧 | PR `feat/http-cache` | blog 06 |
| **削峰填谷（下单异步化）** | 🚧 | PR `feat/order-async-mq` | blog 07 |
| **削峰填谷 (秒杀异步化)** | ❌ | — | 路线图 |
| CDN 商品静态化 | ❌ | — | 路线图 |

整体观察：写路径（下单 / 扣库存 / 关单）已经覆盖到"最终一致 + 兜底"层级，
读路径（商品 / 列表）也接了缓存和 ES。剩下的硬骨头集中在**入口削峰**——
让突发流量在到达 DB 之前先被 MQ / CDN / HTTP cache 吸收掉。

## 2. Web3 集成

| Feature | 状态 | 关键代码 | 配套 doc |
|---------|------|----------|---------|
| **Escrow 合约 (Solidity)** | 🚧 | PR `feat/web3-escrow` (`contracts/Escrow.sol` + `pkg/web3/escrow/`) | blog 08 |
| **EVM 链上事件监听** | 🚧 | PR `feat/web3-listener` (`service/web3/listener.go`) | blog 09 |
| **钱包签名验证 (EIP-191)** | 🚧 | PR `feat/web3-paydown` (`pkg/web3/signature/`) | blog 09 |
| 多签 / DAO 仲裁 | ❌ | — | 路线图 |
| Stablecoin 直付 (USDC/USDT ERC-20) | ❌ | — | 路线图（接 ERC-20 transfer 流程） |
| Layer 2 (Optimism / Arbitrum) gas 优化 | ❌ | — | 路线图 |

Web3 这条线优先把"钱包付款 → 链上事件 → 订单状态"打通，先解决"链外业务系统如何
信任链上事实"的问题。多签仲裁、稳定币直付、L2 等优化都建立在这条链路上。

## 3. AI 赋能

| Feature | 状态 | 关键代码 | 配套 doc |
|---------|------|----------|---------|
| **Milvus 向量库客户端** | 🚧 | PR `feat/milvus-vector` (`repository/milvus/`) | blog 10 |
| **语义搜索 API (ES + Milvus hybrid)** | 🚧 | PR `feat/semantic-search` (`api/v1/product_semantic.go`) | blog 11 |
| Embedding pipeline (商品上下架 → vector) | ❌ | — | 路线图（outbox `product.changed` consumer 触发） |
| 以图搜图 (CLIP) | ❌ | — | 路线图 |
| LLM 智能客服 / 推荐解释 | ❌ | — | 路线图 |

AI 这条线的支点是**已经落地的 outbox 事件流**——商品上下架已经能发出
`product.changed`，把这条事件接到 embedding pipeline 就能拿到准实时的向量索引。
ES + Milvus 的 hybrid 是第一步，CLIP / LLM 是更上层的应用。

## 4. 可观测性 / 运维

| Feature | 状态 | 关键代码 | 配套 doc |
|---------|------|----------|---------|
| Jaeger 链路追踪 | ✅ | `middleware/track.go`, `pkg/utils/track/` | — |
| Skywalking-go agent | ✅ | go.mod + Makefile | README |
| 结构化日志 (logrus) | ✅ | `pkg/utils/log/` | — |
| Prometheus metrics | ❌ | — | 路线图 |
| 业务 SLO 告警 (error budget) | ❌ | — | deck 10 提及 |

链路追踪和日志已经够用，metrics 是当前最大的缺口——熔断 / 限流 / 异步队列堆积
都需要 Prometheus 这种 pull-based 指标体系才能做趋势告警。

## 5. 路线图（按优先级）

- **P1（下一季）**：CDN 静态化、Embedding pipeline 自动化（接 outbox）、Prometheus 接入
- **P2**：多签 / DAO 仲裁、稳定币直付（ERC-20 USDC/USDT）
- **P3**：CLIP 以图搜图、LLM 智能客服 / 推荐解释

排序原则：先把已经"在做"的几条线（HTTP cache / 异步下单 / Web3 / 向量搜索）合
入主干，再补 P1 的运维侧短板，最后扩 P2/P3 的 AI 与 Web3 深度功能。

## 6. 状态图例

- ✅ 已合入 main
- 🚧 PR 打开中或本地分支待合
- ❌ 未启动

## 7. 参考

- `stressTest/REPORT.md`——既有 feature 的真实压测数字
- `docs/slides/`——5 份业务 deck（11-15），含设计取舍与代码引用
- `docs/blog/`——配套深度文章，每个 🚧 feature 都有独立 blog（编号 06 起）
