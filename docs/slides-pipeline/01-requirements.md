# 需求规格：gomall 5 大特性 LaTeX Beamer Deck

> 流水线 Stage 1 产出物。作为 Stage 2 (方案) / Stage 3 (执行) / Stage 4 (验收) / Stage 5 (slide) 的**唯一执行依据**。
> 范围：基于 `gomall` 项目（Go + Gin 电商，仓库 commits 截至 `037c7b7`）的 5 篇博客 + `stressTest/REPORT.md`，产出 5 份 Beamer 中文 deck。

---

## 1. 目标受众画像

| 维度 | 描述 |
|------|------|
| **岗位** | 后端工程师，准备秋招/社招面试或晋升答辩 |
| **经验** | 1–3 年 Go / Java / Python 后端，写过 CRUD，做过 Redis 和 MySQL，但没系统性写过电商高并发链路 |
| **已有基础** | 懂 HTTP、SQL 索引、事务、Redis 基础命令；听过"分布式锁""消息队列"但没真的拍板选过 |
| **痛点** | 一线业务讲不清"为什么这么做"，面试官追问到"如果……怎么办"就答不上 |
| **要带走** | (a) 一套能讲清楚架构选择背后 trade-off 的口径；(b) 能直接复用到面试的话术 + 数字；(c) 项目里真实可跑的代码片段 |

讲师视角：受众**不缺 API 用法**，**缺的是"为什么这么选"的对比和"踩过哪些坑"的故事**。Deck 必须围绕这两点展开。

---

## 2. 五个 Deck 课题

### Deck 1 · 幂等：让重复请求只生效一次

- **标题**：幂等中间件设计
  - 副标题：从 `Idempotency-Key` 到 Redis Lua 状态机
- **一句话定位**：讲清楚一个 HTTP 中间件如何用 Redis Hash + Lua 把"客户端重试""网关重投""用户狂点"统一收敛成"恰好一次"。
- **学习目标**：
  1. 能画出 `init → processing → done` 三态机及其转换时机
  2. 能解释为什么必须用 Lua 而不是 GET + SET 两步
  3. 能区分 token 不存在 / 处理中 / 已完成三种命中并给出合适的 HTTP 响应
  4. 能在自己项目里加一个 `Idempotency-Key` 中间件并配出合理的 TTL
  5. 能解释幂等与"分布式锁""去重表"的边界
- **面试重点**：
  1. 为什么"先 GET 再 SET"不是幂等？(竞态)
  2. 客户端没拿到响应就重试，服务端如何不重复扣款？
  3. 幂等 token 的生命周期谁来负责，TTL 设多长？
  4. 如果业务处理失败，token 状态怎么回滚？
  5. 幂等 vs 分布式锁的区别？
- **学时**：60 分钟（含 1 次现场画状态机）
- **难度**：进阶
- **必带数字**：`50 VU × 15s = 755,033 次请求 → DB 实际只产生 1 笔订单`（来自 `stressTest/REPORT.md §4`），`p95 2.33ms`

---

### Deck 2 · 防超发：Redis Lua vs DB 悲观锁

- **标题**：防超发与抢购库存
  - 副标题：Redis 原子扣减 vs `SELECT ... FOR UPDATE` 的实战对比
- **一句话定位**：用同一个抢券/扣库存场景，把两种"防超发"方案放在一起对比，给出选型决策树。
- **学习目标**：
  1. 能写出抢券的 Redis Lua 脚本（含库存检查 + 用户限领 + DECR + EXPIRE）
  2. 能写出 `SELECT FOR UPDATE` + count 校验 + 事务的等价 DB 实现
  3. 能解释 `available / reserved` 两桶库存模型为什么需要
  4. 能解释 Redis 数据如何与 DB 最终对账（落库失败 → 回滚 Redis）
  5. 能从延迟分布、扩展性、一致性三个维度给出选型建议
- **面试重点**：
  1. 100 张券，500 个并发，怎么保证不超发？
  2. Redis 扣减成功，但 DB 落库失败，怎么办？
  3. `SELECT FOR UPDATE` 用错索引会发生什么？（行锁升级为间隙锁/表锁）
  4. 为什么不能用 `if stock>0 then DECR` 两条命令？
  5. 热点商品如何分片库存？
- **学时**：90 分钟（含一次 Lua 脚本现场调试）
- **难度**：挑战
- **必带数字**：`500 goroutine 抢 100 张券 → 实际成功数恰好 100，零超发`，`Redis Lua max 136ms vs DB FOR UPDATE max 453ms`（REPORT §6）

---

### Deck 3 · 缓存一致性：Cache Aside、延迟双删与击穿保护

- **标题**：缓存一致性
  - 副标题：从 Cache Aside 到延迟双删 + SETNX 回源锁
- **一句话定位**：把"读写顺序怎么排""缓存击穿/穿透/雪崩怎么挡"这三件最容易在面试被追问的事，按从易到难讲透。
- **学习目标**：
  1. 能默写 Cache Aside 的读路径和写路径
  2. 能解释为什么"先删缓存再写库"和"先写库再删缓存"都会有问题，以及延迟双删的窗口
  3. 能用 `SETNX` + 短 TTL 实现回源锁，避免缓存击穿
  4. 能给出热点 key 的预热策略和 TTL 抖动策略
  5. 能解释何时该用 `binlog → MQ → 异步刷缓存`（强一致诉求）
- **面试重点**：
  1. 双写一致性的几种方案，各自的取舍？
  2. 延迟双删的"延迟"该设多少？依据是什么？
  3. 缓存击穿和缓存穿透的区别？怎么治？
  4. 为什么不直接同步更新缓存，而是删除？
  5. TTL 雪崩怎么避免？
- **学时**：60 分钟
- **难度**：进阶
- **必带元素**：现场画一张"写库 → 第一次删缓存 → 业务返回 → 异步延迟 500ms → 第二次删"的时序图（对应 `cache/product.go` 的 `DoubleDeleteAsync`）

---

### Deck 4 · Outbox + Saga：跨服务一致性与一次踩坑实录

- **标题**：Outbox 模式与 Saga 补偿
  - 副标题：从"DB 事务能不能套住 MQ"出发，一直讲到一次真实线上事故
- **一句话定位**：讲清楚为什么不能在事务里直接 publish MQ，以及 Outbox 表 + 轮询发布 + Saga 补偿如何兜底；并复盘一次真实 bug（`util.LogrusObj` 在 InitLog 前被使用，导致 RabbitMQ recover 二次 panic 把进程拖死）。
- **学习目标**：
  1. 能画出 Outbox 表结构（事件 id / routing key / payload / state / attempts）
  2. 能解释"事务提交 + MQ ack"非原子带来的所有失败组合
  3. 能写出一个 batch poller，含失败重试、attempt 上限、毒消息策略
  4. 能解释 Saga 编排式 vs 协同式的差别，并按"订单创建-扣库存-扣余额-发券"画出补偿链
  5. 能从 bug 复盘里抽出"初始化顺序"和"recover 内再 panic"两条通用教训
- **面试重点**：
  1. 为什么不能"`tx.Commit()` 之后再 publish MQ"？
  2. Outbox 表挤压怎么办？（消费者慢 / MQ 挂了）
  3. Saga 的补偿可能再失败，怎么办？
  4. 至少一次 vs 恰好一次，消费端怎么做幂等？
  5. 一次"初始化顺序错误导致进程崩溃"的事故，你怎么定位和修？
- **学时**：90 分钟（含 10 分钟事故复盘环节）
- **难度**：挑战
- **必带元素**：必须包含真实 bug 故事（REPORT §"已修：util.LogrusObj 在 InitLog 前被使用"），讲师以此带出"日志/MQ/recover 三者顺序"

---

### Deck 5 · 限流 + 熔断：让系统在过载时优雅退化

- **标题**：限流与熔断
  - 副标题：令牌桶、滑动窗口、三态熔断器一次性梳清
- **一句话定位**：把"挡住流量"和"挡住故障"这两件事，用一个 deck 讲清楚边界、算法选择和参数怎么拍板。
- **学习目标**：
  1. 能解释令牌桶 vs 漏桶 vs 滑动窗口 vs 固定窗口的差别
  2. 能写出 Redis ZSet 实现的滑动窗口 Lua 脚本
  3. 能画出熔断器 closed / open / half-open 三态及触发条件
  4. 能给一个新接口拍板：放在哪一层、用哪种算法、阈值怎么选
  5. 能区分"限流"是入口治理、"熔断"是出口治理
- **面试重点**：
  1. 令牌桶为什么允许"突发"？业务上需要吗？
  2. 单机限流 vs 分布式限流，分别用什么？
  3. 滑动窗口为什么用 ZSet 不用 List？
  4. 熔断器的 half-open 探测请求数怎么选？
  5. 限流后是 429 还是 200 + 业务码？怎么和前端约定？
- **学时**：60 分钟
- **难度**：进阶
- **必带数字**：`30 VU × 15s 打秒杀，Limit=3/s → 期望 45 次通过 / 实际 46 次（误差 <3%）`（REPORT §5），熔断单测三态全 PASS

---

## 3. 视觉与交互要求

### 3.1 必须出现的图表类型（每份 deck 至少满足一项）

| 类型 | 用途 | 例子 |
|------|------|------|
| **TikZ 时序图** | 客户端 ↔ 网关 ↔ Redis ↔ DB 交互 | Deck 1 幂等、Deck 3 双删、Deck 4 Outbox |
| **TikZ 状态机** | 多态切换 | Deck 1 (init/processing/done)、Deck 5 (closed/open/half-open) |
| **TikZ 架构图** | 模块层次 + 数据流向 | Deck 2 (Redis ↔ 业务 ↔ DB ↔ 对账)、Deck 4 (App → Outbox 表 → Poller → MQ → Consumer) |
| **代码块** | Lua 脚本 / Go handler | listings 包，配色克制，行号开 |
| **数据表格** | 压测对比 / 选型矩阵 | Deck 2 (Redis vs DB)、Deck 5 (限流算法对比) |

### 3.2 颜色风格

- **主色**：靛蓝 `#1F4E79`（深沉，正文/标题）
- **强调色**：暖橙 `#E07A1F`（仅用于关键数字、警示、错误路径）
- **辅助灰**：`#595959`（次级文本）
- **背景**：纯白；代码块底用 `#F4F4F4`
- **禁忌**：不用红绿对比（色盲不友好），不用渐变，不用阴影

### 3.3 字体

| 用途 | 字体 |
|------|------|
| 中文正文 | Source Han Sans SC / Noto Sans CJK SC |
| 中文标题 | Source Han Serif SC（与正文区隔） |
| 英文/数字 | Inter 或 Latin Modern Sans |
| 代码 | JetBrains Mono（首选）或 Fira Code |

---

## 4. LaTeX 排版要求

- **引擎**：必须 `xelatex`（中文 + Source Han 必需）
- **文档类**：`\documentclass[aspectratio=169,11pt]{beamer}`
- **核心宏包**：
  - `ctex`（中文）
  - `tikz` + `positioning` + `arrows.meta` + `shapes.geometric`（图）
  - `listings` + `xcolor`（代码高亮，Go / Lua / SQL 三种语言均需配色）
  - `booktabs`（三线表）
  - `fontspec`（指定 Source Han / JetBrains Mono）
- **主题**：
  - 首选 **Metropolis**（`\usetheme{metropolis}`）—— 干净，与"工程师向"气质契合
  - 备选 **Madrid**（仅在 Metropolis 在 ctex 下出 bug 时回退）
- **每页规则**：
  - 顶部：deck 标题（page footer 自动） + 章节
  - 主区：1 个核心点（一页一论点，不堆字）
  - 底部：来源标注（`code: middleware/idempotency.go:35` / `report: §4` / `bug: PR#51`），灰色 6pt
- **页码格式**：`当前页 / 总页数`，右下
- **每个 deck 控制在 20–30 页**：封面 1 + 大纲 1 + 正文 16–26 + 总结 + 面试题 + Q&A

---

## 5. 质量门槛与红线

### 5.1 必须包含

- **每个 deck 至少 3 张 TikZ 图**（不允许全是文字 + 代码）
- **每个 deck 至少 1 段真实压测数字**（直接引用 `stressTest/REPORT.md` 的表）
- **每个 deck 至少 1 段从仓库直接引用的代码**（带文件路径 + 行号）
- **每个 deck 末尾 2 页"面试 Q&A"**：5 个高频题 + 一句话答案 + 反问追问点
- 所有数字必须可追溯到 `stressTest/REPORT.md` 或代码

### 5.2 不允许出现

- "教学项目"四字 / "本课程"等元描述
- placeholder（`TODO` / `xxx` / `Lorem ipsum`）
- 复制网上博客的错误说法（如"Redis 单线程所以一定线程安全"这类不准确表述）
- 表情符号
- 用红绿色作主色
- 一页超过 6 行正文（Beamer 不是 Word）
- 截图代替 TikZ 画图
- 不带来源标注的数字（"延迟从 10s 降到 5ms" 必须能在 REPORT 找到）

### 5.3 内容正确性红线

- Lua 脚本必须可以直接 `redis-cli EVAL` 跑通
- Go 代码片段必须与仓库当前 `main` 分支一致，截断要标 `// ...`
- 时序图箭头必须区分同步 / 异步（实线 vs 虚线）
- 状态机必须覆盖所有合法转换（不允许漏掉 "processing → init 回滚" 这种边）

---

## 6. 交付清单（后续 4 个阶段）

### Stage 2 · 方案 agent → `02-design.md`

- 5 个 deck 各自的**详细页面提纲**：每页标题 + 一句话要点 + 图/表/代码占位
- 每个 TikZ 图的**结构草图**（节点 + 边的伪代码描述，不画实际 TikZ）
- 每个代码块要从仓库哪个文件哪几行截
- 每个压测数字对应 REPORT 哪一节
- 选型表格的列定义（如"延迟、扩展性、复杂度、运维成本"）

### Stage 3 · 执行 agent → `slides/deckN/main.tex` × 5

- 真正可编译的 `.tex` 文件（每个 deck 独立目录，含 `main.tex` + 资源）
- 通用 `preamble.tex`（字体、颜色、TikZ 库）
- 所有 TikZ 图源码
- 所有 listings 代码块（Go / Lua / SQL）
- 一个 `build.sh`：`xelatex main.tex` × 2 次（解决目录引用）

### Stage 4 · 验收 agent → `04-review.md`

- 逐 deck 检查：是否满足"3 图 + 1 压测 + 1 代码 + 2 页 Q&A"
- 编译产物 PDF 截图（每个 deck 抽 3 页）
- 红线 checklist（每项 √ / ×）
- 修改清单（输出回 Stage 3 修复）

### Stage 5 · slide agent → `05-final.md` + 最终 PDF × 5

- 5 份 PDF（每份 20–30 页，文件名 `deckN-<topic>.pdf`）
- 一份合并的"讲师手册"`teacher-notes.md`：每页一段口语化讲解，标注"现场互动点"
- 一份"学员讲义"`handout.md`：精简版，去掉过场页

---

## 附录 A · 5 个 deck 与代码 / 压测来源对应表

| Deck | 主要代码 | 测试 | 压测节 |
|------|----------|------|--------|
| 1 幂等 | `middleware/idempotency.go`、`repository/cache/idempotency.go` | `repository/cache/idempotency_test.go` | §4 |
| 2 防超发 | `repository/cache/coupon.go`、`repository/cache/inventory.go` | `repository/cache/coupon_test.go` | §6 + 单测 500 并发 |
| 3 缓存一致性 | `repository/cache/product.go` (TryProductLock / DoubleDeleteAsync) | — | §1 (基线 vs 业务) |
| 4 Outbox+Saga | `service/outbox/publisher.go`、`service/events/events.go`、`repository/db/model/outbox.go` | — | bug 复盘 (REPORT §"已修") |
| 5 限流+熔断 | `middleware/ratelimit.go`、`middleware/circuitbreaker.go`、`repository/cache/ratelimit.go` | `middleware/circuitbreaker_test.go`、`middleware/ratelimit_test.go` | §5 |

## 附录 B · 名词中英对照（不翻译，统一英文）

Idempotency、Cache Aside、SETNX、Outbox、Saga、Token Bucket、Sliding Window、Circuit Breaker、Half-Open、`SELECT FOR UPDATE`、Available / Reserved。
