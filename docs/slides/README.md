# gomall Deck 体系（v2）

围绕 gomall 真实代码与压测数据产出的中文 Beamer Deck。
按**业务域**拆分 12 份，每份 25-35 页，业务 70% + 代码 30%（关键函数逐行讲解）。

---

## 12 份 Deck 索引

| # | 主题 | 业务域 | 状态 |
|---|------|--------|------|
| 01 | 用户与鉴权 | 注册 / 登录 / JWT 双 token / RBAC / admin bootstrap | 待写 |
| 02 | 商品展示 | list / show / category / carousel + Cache Aside + HTTP cache | 待写 |
| 03 | 商品搜索 | LIKE → ES → ES + Milvus hybrid + outbox 增量索引 | 待写 |
| 04 | 购物车 → 下单 | 加购 / 地址 / snowflake 订单号 / 库存预扣 / 事务 + outbox | 待写 |
| 05 | 支付（法币） | AES 金额加密 / 支付密码 / 第三方调用 / 熔断 + 幂等 | 待写 |
| 06 | Web3 支付 | Escrow / nonce / EIP-191 / EVM 监听 / 链上对账 | 待写 |
| 07 | 库存与防超发 | 两桶 Lua (available/reserved) / Saga 回滚 / 启动同步 / 关单释放 | 待写 |
| 08 | 营销活动 | 优惠券 / 秒杀 / 抢红包（三个 Lua 套路对照） | 待写 |
| 09 | 订单生命周期 | 状态机 / RMQ TTL 延迟 / Cron 双保险 / 履约 gap 路线图 | 待写 |
| 10 | 流量治理 | TokenBucket / SlidingWindow / CircuitBreaker / 异步削峰 | 待写 |
| 11 | Outbox 与一致性 | 双写问题 / Transactional Outbox / Saga / 缓存一致性 / 启动顺序 | 待写 |
| 12 | 商家后台 + 可观测性 | BossID / merchant 三层鉴权 / Jaeger / Skywalking / 静默降级 | 待写 |

合订本 `master.pdf` 由 12 份 deck 全合后 `build.sh --master` 重新拼接。

---

## 编译

```bash
cd docs/slides
./build.sh              # 全量编译
./build.sh 03           # 只编译某份
./build.sh --master     # 拼合订本
```

页数硬指标：旧 `[20, 30]`，新 deck 期望 `25-35`。超 30 build.sh 会输出提醒但不阻塞。

---

## 体例（强约束）

- 第一行 `\input{preamble}`，不加新包
- ≥ 5 TikZ 图、≥ 12 处 `\srcnote{file:line}`、≥ 3 段关键代码 `lstlisting`（带逐行讲解）
- ≥ 2 处来自 `stressTest/REPORT.md` 的数字
- 末 3 页固定：Q&A（上）/ Q&A（下）/ 代码位置一览
- 禁用词：`教学 / 本课程 / TODO / FIXME / placeholder / 演示 / 示例代码 / 抛砖 / 大家好` 一个不准
- 业务 70% + 代码 30%（业务困局 → 流程 → 关键代码 → 业务码 / 客服 / 路线图）

配套：

- `preamble.tex` 共享样式（字体 / TikZ 节点 / listings / srcnote / keypoint）
- `build.sh` 一键编译 + 页数检查
- `docs/blog/` 同主题博客（深度补充阅读）
- `stressTest/REPORT.md` 压测数据源
- `docs/slides-pipeline/` 历史需求 / 方案 / 验收文档（仅参考）
