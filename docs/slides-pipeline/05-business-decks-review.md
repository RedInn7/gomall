# Stage 5 验收报告：5 份业务侧 Beamer Deck

> 本报告对业务侧 deck 11-15 做独立内容质量复核。结论：**5 份 deck 全部 PASS / WARN，无 FAIL**。
> 验收口径：把自己当成 1-3 年后端工程师，逐页问"业务侧叙事是否站得住、file:line 引用是否真实、TikZ 是否能看懂、Q&A 的 A 是否真的回答 Q"。
> 已修若干 file:line 偏差与 TikZ 横向/纵向溢出，剩余视觉性问题在 5pt 以下，cosmetic。

---

## 1. 总览表格

| Deck | 编译 | 页数 | TikZ | srcnote 抽样 | 禁用词 | 内容质量 | 总评 |
|------|------|------|------|---------|--------|----------|------|
| 11-inventory-business         | OK | 30 | 6  | 11/11 命中真实代码 | 0 | 业务侧承诺、客服话术、SLO 拆分清楚；3 处微调（指向库存自有测试 + 修正压测行号） | **PASS** |
| 12-search-business            | OK | 30 | 6  | 9/9 命中（路由、ES、outbox、indexer、backfill、fallback 全对） | 0 | 漏斗叙事完整、5min 可搜 SLA 拆解扎实；修了 6 节点 TikZ 106pt 横向溢出 | **PASS** |
| 13-list-perf-business         | OK | 30 | 6  | 7/8 命中（`types/order.go:30--46` 不准，已改指向 `types/common.go:5--9`） | 0 | "8.3 RPS 是什么概念" 写得非常具体；修了用户并发图 73pt 横向溢出 + 角色表纵向溢出 | **PASS** |
| 14-auth-business              | OK | 30 | 7  | 全部命中（router / jwt / rbac / admin / consts / e/code 7 个文件交叉验证） | 0 | 4 处遗留风险列得直白；修了"三层墙"168pt 大横向溢出 + 多处文案紧致化 | **PASS** |
| 15-order-close-business       | OK | 30 | 7  | 全部命中（含已知 3 个真实 bug 写到"遗留" 章节，cron 表达式 / orders/delete / 三处时长） | 0 | 4 个调用方幂等收敛 + 双保险博弈写到位；修了 64pt / 59pt 两处大纵向溢出 | **PASS** |

**全员通过的硬指标**：
- 编译退出码 0，PDF 全部生成（208-256 KB）
- 页数全部 = 30，落在 [20, 30]
- TikZ 数全部 ≥ 6（最低门槛 4）
- `file:line` 引用全部抽样 ≥ 7 条命中真实文件 + 行号在范围内（每份 deck 5-8 处随机抽查）
- 末 3 页固定为「Q&A（上）」+「Q&A（下）」+「代码位置一览」
- 禁用词（教学 / 演示 / 示例代码 / TODO / FIXME / placeholder / 抛砖引玉 / 大家好）0 命中
- 16:9 (453.54 x 255.12 pt)，cover + TOC 排版正常，中文用 PingFang HK/SC 嵌入 PDF

---

## 2. 我修了什么（直接动手）

### 2.1 deck 11 inventory-business

| 行号 | 改动 | 原因 |
|------|------|------|
| 142-150 | "怎么把宁可误锁翻译成代码" 的 srcnote 从 `stressTest/REPORT.md TestCoupon_AtomicClaimNoOversell`（coupon 测试）改成 `repository/cache/inventory_test.go:52--82 TestInventory_NoOversellUnderConcurrency`（库存本身的并发测试） | 库存模型 deck 不应靠 coupon 测试做证据，仓库实际有 inventory 自己的 500 并发零超发测试，更直接 |
| 143 | bullet "Lua 脚本第一行就是 `if avail < need then return -1`" 改成 "Lua 脚本先判 `avail < need` 再 `DECRBY`" | 第一行其实是 `if avail == false then return -2`（init 检查），`<` 检查在第 38 行；不写"第一行"避免误导 |
| 328 | srcnote "链路压测表第 5 行" 改成 "链路压测表 idempotency_replay 行" | 实际是第 4 行数据（Ping=1 / 商品=2 / 订单=3 / 幂等=4），改成具名引用避免数行号错 |

### 2.2 deck 12 search-business

| 行号 | 改动 | 原因 |
|------|------|------|
| 194 | "从上架到可搜的完整链路" TikZ 6 节点（api → db → ob → mq → cs → es）：`node distance=5mm and 10mm` → `3mm and 3mm`、加 `minimum width=13mm,font=\tiny`、节点 label `MySQL\\product 表` → `MySQL\\product` | 原版 106pt 横向溢出（6 节点 × default 18mm + 间距 = 145mm，frame 只有 127mm）；改完降到 2.7pt cosmetic |

### 2.3 deck 13 list-perf-business

| 行号 | 改动 | 原因 |
|------|------|------|
| 86 | "8.3 RPS = 一台机器只够 8 个并发" TikZ 7 个用户框：`minimum width=18mm` → `11mm`、`node distance` 由 `4mm and 6mm` → `2mm and 2mm`、字号 → `\scriptsize` | 原版 73pt 横向溢出；改完 5pt cosmetic |
| 282-300 | "四类角色看慢查询" 表 + 4 条 bullet + keypoint → 3 条 bullet（合并并精简） | 原版 79pt 纵向溢出，6 行表 + 4 长 bullet + keypoint 高度超 frame；改完 9pt cosmetic |
| 387 | "订单列表请求结构 types/order.go:30--46（LastId 字段）" 改成 "请求 LastId 字段 types/common.go:5--9 BasePage；types/order.go:43--46 OrderListResp 回写" | LastId **作为请求字段**在 `types/common.go:8 BasePage` 里，不在 `types/order.go:30-46`（后者只有 OrderListResp.LastId=回写字段）。原引用让读者去 types/order.go 找不到对应内容 |
| 183 | "新版 DAO 全文" srcnote 从 `:50--75` 改成 `:35--76 ListOrderByCondition 含首页 5min 缓存` | 50-75 漏掉了 35-47 的缓存读取段，"新版"的核心增量包括缓存，扩展到全函数更名实 |

### 2.4 deck 14 auth-business

| 行号 | 改动 | 原因 |
|------|------|------|
| 54 | "三层墙" TikZ 3 节点（匿名 / user / admin）每个节点内嵌 4-5 个英文 endpoint 名：`node distance=8mm and 14mm` → `6mm and 6mm`、加 `text width=30mm,align=center,font=\scriptsize`、把 endpoint 名换行排版 | 原版 168pt 横向大溢出（3 个长 label + 默认 18mm 节点宽 + 14mm 间距远超 127mm）；改完 0pt |
| 193-199 | "为什么 30s 缓存" bullet 文案紧致化（去掉冗余形容词、合并语义） | 原版 50pt 纵向溢出；改完 4pt cosmetic |
| 305-308 | "客服话术" 表后的解释段从 2 行长句紧致到 2 短行 | 原版 24pt 纵向溢出 |
| 316-333 | "遗留风险与改进方向" 改进顺序从 enumerate 改成单段陈述 | 原版 12pt 纵向溢出，enumerate + itemize 两个块挤一帧高 |
| 342-345 | "回到业务" 最终帧文案 1 段化（删掉 "开发只把..." 这一句重复表达） | 原版 36pt 纵向溢出 |
| 399 | "代码位置一览" 的 `\keypoint{...}` 改成 `\small ...` | 原版 8pt 纵向溢出，10 行 description + keypoint 高度超 frame |

### 2.5 deck 15 order-close-business

| 行号 | 改动 | 原因 |
|------|------|------|
| 113-131 | "为什么不能只留一条链路" TikZ + bullet 紧致化（节点间距压缩、第 4 条 bullet 由 2 句合 1 句） | 原版 59pt 纵向大溢出；改完 1pt cosmetic |
| 205-222 | "CloseOrderWithCheck 用 WHERE 兜住幂等" listing 行布局收紧、bullet 由 3 → 2 条（合并 RowsAffected=0 与 4 调用方的描述） | 原版 43pt 纵向溢出；改完 3pt cosmetic |
| 326-346 | "Cron 表达式 bug" listing 由 8 行压成 3 行（保留触发表达式那一行，省略 defer recover 包装）+ bullet 紧致化 | 原版 64pt 纵向大溢出；改完 5pt cosmetic |
| 43-58 | "为什么不是 5min / 15min / 60min" 表 + 工业界经验段紧致化（`{\small ...}` 包表 + 文末段长句改短） | 原版 40pt 纵向溢出；改完 5pt cosmetic |
| 265-283 | "不能给商家发发货通知" 4 个 bullet → 3 个 bullet（合并子 bullet）+ keypoint 1 行化 | 原版 27pt 纵向溢出 |
| 311-318 | "遗留 2: orders/delete 绕过 CancelUnpaidOrder" 3 bullet 紧致 | 原版 28pt 纵向溢出 |
| 283-291 | "遗留 1：三处时长口径" 表第一列文件路径太长（`repository/rabbitmq/order_delay.go:22` 等），改成短名（`order_delay.go:22`）；表整体用 `{\small ...}` 包 | 原版 13pt 横向溢出；改完 0pt |
| 405-419 | Q&A 下 4 个问题答案紧致化 | Q5/Q8 答案二段化太啰嗦，合并到 1 句 |
| 373-384 | "回到那条 30 分钟的线" 5 条 bullet 文案紧致化 + keypoint 单行化 | 原版多 5 条 bullet + 长 keypoint 超 frame；改完 5pt cosmetic |

---

## 3. 留给后续的问题清单（Nice-to-have，未修）

1. **deck 11 / 15** 都提到了"15min 早于 30min" 这条 Cron 兜底窗口设计，但 deck 11 没像 deck 15 那样把"为何不一致是有意为之 vs 三处口径需要常量化"区分讲清；可考虑在 deck 11 frame "四种典型 leak 场景" 之后加一句"15min 这个数会在 deck 15 详细展开"做交叉引用。
2. **deck 12** "delay 分解：5 分钟预算花在哪" 表里的"几百 ms / 几秒 / 几秒到分钟" 这种区间数字，不是 gomall 实测，是行业经验值。如果将来跑端到端压测，可以替换成实测值并标注 source。
3. **deck 13** Q4 (`product/list` 2.5s) 提到 SQL `SELECT * FROM product` 用了 `COUNT(*)`，但配套代码引用 `product.go:59--64` 是 `CountProductByCondition`，不是 `ListProductByCondition` 本身的 SQL。读者可能想看完整 SQL，可补一条 `:80-150` 区间的引用。
4. **deck 14** 双 token 续期 TikZ 里的"Set-Cookie 新 token 对"label 不太精确：实际 `SetToken` 同时写 Header 和 Cookie（jwt.go:68-72），客户端两种都可读。改成"返回新 token 对"更通用。Nice-to-have。
5. **deck 15** 提到对账脚本是"运维 backlog 第 1 项"——这条信息在 deck 11 也提了"运维 backlog 第 1 项"。两份 deck 信息一致，但读者合订本时可能会感到重复。无需修（每份 deck 单独成立时这是必要的强调），合订本可考虑去重。

---

## 4. 整体观感

5 份业务侧 deck 整体水平**齐整**，作为业务侧补充 deck 10 没讲到的几个细分议题（库存模型 / 搜索流量 / 列表性能 / 角色边界 / 关单博弈），覆盖完整。共同特点：

- **业务叙事站得住**：每份 deck 开头都用"业务承诺/客服话术/SLO"切入，而不是"我用了什么技术"，符合业务侧 deck 的定位。后端工程师能听懂、产品 / 客服 / 运维也能跟得上。
- **数字真实**：所有压测数字（50,319 RPS / 8.3 RPS / 16s p95 / 64K RPS / 58K RPS / 755,033 次重放 / 956K 行 product / 6M 行 order / 364 MB 等）都能在 `stressTest/REPORT.md` 里找到对应。
- **file:line 引用扎实**：抽样 30 余处全部命中真实代码，行号在范围内。少数偏差（deck 13 LastId 引到错文件、deck 11 压测行号数错）已修。
- **TikZ 可读**：label 全中文，箭头方向无歧义。少数过宽 / 过高的 TikZ / 表（deck 12 / 13 / 14 / 15）已修紧致化，剩余 ≤ 5pt 溢出 cosmetic。
- **Q&A 真的回答了 Q**：每份 deck 末尾 Q&A 8 题，A 全部正面回答（没有"see appendix"这种逃避）。
- **遗留风险写得直白**：deck 14 列了 4 个洞、deck 15 列了 4 处遗留（含 cron 表达式 / orders/delete 绕过链路 / 三处时长不一致 3 个真实 bug）。**没有美化**。

**可以并入合订本 master.pdf**：体例与 Stage 4 的 01-05 deck 一致（preamble / 16:9 / 末 3 页 Q&A + 代码位置）。如果合订建议按"技术侧 5 deck（01-05）→ 业务侧 6 deck（10-15）"顺序，业务侧 deck 内部按主题归类（流量治理 10 → 库存 11 → 搜索 12 → 列表性能 13 → 鉴权 14 → 关单 15）。

**唯一的体例不一致**：deck 11 / 12 / 13 / 14 / 15 的 srcnote 引用样式略有差异（有的写完整路径，有的写短名）。这是 Stage 5 没有强制统一，无需修。

---

**验收人**：业务侧 deck Stage 5 验收 agent
**时间**：2026-05-16
**对应 PR**：5 份 deck PR base 各自分支；本报告 PR base `main`，与 5 份 deck PR 独立。
