# gomall 后端实战 · 5 份 Beamer 幻灯片

围绕 `gomall` 项目（Go + Gin 电商）真实代码与压测数据产出的 5 份中文 Beamer Deck。
面向 1–3 年后端工程师：讲清"为什么这么做"的取舍 + 提供能直接用到面试里的话术和数字。

---

## 5 份 Deck 概览

| #  | 标题 / 副标题 | 学时 | 难度 | 一句话定位 | 封面 |
|----|---------------|------|------|------------|------|
| 01 | **幂等中间件设计**<br>从 `Idempotency-Key` 到 Redis Lua 状态机 | 60 min | 进阶 | HTTP 中间件如何用 Redis Hash + Lua 把"重试 / 重投 / 狂点"统一收敛成"恰好一次"。 | `previews/01-idempotency-cover.png` |
| 02 | **防超发与抢购库存**<br>Redis Lua vs `SELECT ... FOR UPDATE` 的实战对比 | 90 min | 挑战 | 三种扣库存方案的并发与尾延迟对比，含 Lua 脚本现场推演。 | `previews/02-anti-oversell-cover.png` |
| 03 | **缓存一致性**<br>Cache Aside、延迟双删与 SETNX 回源锁 | 60 min | 进阶 | 单删为什么不够、延迟双删窗口怎么算、热 key 击穿如何防。 | `previews/03-cache-consistency-cover.png` |
| 04 | **Outbox 模式与 Saga 补偿**<br>从 DB 事务能不能套住 MQ，到一次真实线上事故 | 90 min | 挑战 | 双写问题、Transactional Outbox、协同式 Saga，以及"recover 内再 panic"的事故复盘。 | `previews/04-outbox-saga-cover.png` |
| 05 | **限流与熔断**<br>令牌桶、滑动窗口、三态熔断器一次性梳清 | 60 min | 进阶 | 三种限流算法选型、Redis ZSet 滑动窗口实现、Closed/Open/HalfOpen 状态机。 | `previews/05-ratelimit-circuit-breaker-cover.png` |

> 合订本：`master.pdf`（151 页，约 1 MB），封面预览 `previews/00-master-cover-cover.png`。
> 重新生成预览图：`for f in 0?-*.pdf; do pdftocairo -png -singlefile -r 90 -f 1 -l 1 "$f" "previews/${f%.pdf}-cover"; done`

每份 deck 都遵循同一结构：

- **引子**：从一段真实代码 / 一个 bug / 一次踩坑 切入
- **对比**：候选方案的 trade-off 表（一致性 / 复杂度 / 性能）
- **核心**：TikZ 时序图 + 状态机 + 关键代码段（带 `file:line` 引用）
- **数据**：来自 `stressTest/REPORT.md` 的真实压测数字（RPS / p95 / 误差）
- **Q&A**：6 道面试常问题 + 1 道反追问
- **末页**：「代码位置一览」—— 给学员"接下来去哪查"的索引

---

## 学习目标精炼

- **Deck 1 幂等**：能画三态机 / 解释为什么必须 Lua / 区分三种命中场景 / 设合理 TTL。
- **Deck 2 防超发**：能选型 Redis Lua vs DB 锁 / 写出原子扣减脚本 / 给出抢购热点 key 的兜底。
- **Deck 3 缓存一致性**：能讲清单删的竞态 / 算出延迟窗口 / 防热 key 击穿。
- **Deck 4 Outbox+Saga**：能解释双写问题 / 实现 Outbox publisher / 区分编排 vs 协同 Saga / 复盘初始化顺序事故。
- **Deck 5 限流+熔断**：能对比三种限流算法 / 实现 Redis 滑动窗口 / 推导熔断三态转换。

---

## 文件结构

```
docs/slides/
├── README.md                          ← 本文件
├── build.sh                           ← 一键编译脚本（带页数检查 + 合订本拼接）
├── preamble.tex                       ← 共享 preamble（字体 / 颜色 / TikZ / listings）
├── 00-master-cover.tex                ← 合订本封面（仅 master.pdf 用，3 页）
├── 01-idempotency.tex                 ← Deck 1 源码
├── 02-anti-oversell.tex               ← Deck 2 源码
├── 03-cache-consistency.tex           ← Deck 3 源码
├── 04-outbox-saga.tex                 ← Deck 4 源码
├── 05-ratelimit-circuit-breaker.tex   ← Deck 5 源码
├── 0?-*.pdf                           ← 编译产物（约 200 KB 一份）
├── master.pdf                         ← 合订本（5 份 deck + 封面，151 页）
└── previews/                          ← 每份 deck 第 1 页的 PNG 预览
```

配套阅读：

- `docs/blog/0?-*.md` —— 5 篇博客长文，是 deck 内容的展开版
- `docs/slides-pipeline/01-requirements.md` —— 需求规格（受众、学习目标、必带数字）
- `docs/slides-pipeline/02-proposal.md` —— 方案设计（结构 / 视觉 / 取舍）
- `docs/slides-pipeline/04-review.md` —— 验收报告
- `stressTest/REPORT.md` —— 真实压测数据（k6 + 单元测试）

---

## 如何编译

### 一键编译

```bash
cd docs/slides
./build.sh
```

脚本会：

1. 对每份 deck 跑 `xelatex` 两遍（解 TOC / appendixnumberbeamer 引用）
2. 任一份失败立即退出（`set -euo pipefail`）
3. 输出每份 PDF 的页数（要求落在 [20, 30]）
4. 清理 `.aux / .log / .toc / .nav / .snm / .out / .vrb` 中间产物

只编译某一份：`./build.sh 03`
拼接合订本：`./build.sh --master`（需 `pdfunite`）
仅清理中间产物：`./build.sh --clean`

### 手动编译

```bash
xelatex -interaction=nonstopmode 01-idempotency.tex
xelatex -interaction=nonstopmode 01-idempotency.tex   # 第二遍解 TOC
```

### 依赖

| 工具 | 用途 | macOS | Linux (Debian/Ubuntu) |
|------|------|-------|------------------------|
| `xelatex` | 编译 (支持中文 + OpenType 字体) | MacTeX 或 `brew install --cask mactex-no-gui` | `apt install texlive-xetex texlive-lang-chinese` |
| Beamer + metropolis | 主题 | MacTeX 自带 | `apt install texlive-latex-extra` |
| `pdfinfo` | 页数检查（可选） | `brew install poppler` | `apt install poppler-utils` |
| `pdfunite` | 拼接 master.pdf（可选） | `brew install poppler` | `apt install poppler-utils` |

### 字体（必备）

| 字体 | 用途 | macOS | Linux |
|------|------|-------|-------|
| PingFang SC / HK | 中文正文（首选） | 系统自带 | 没有，会 fallback 到 Source Han Sans / Noto Sans CJK |
| Helvetica Neue | 西文正文（首选） | 系统自带 | 没有，会 fallback 到 Inter / Arial |
| JetBrains Mono | 代码（首选） | `brew install --cask font-jetbrains-mono` | `apt install fonts-jetbrains-mono` |
| Menlo | 代码（fallback） | 系统自带 | 无 |

Linux 推荐安装：

```bash
sudo apt install fonts-noto-cjk fonts-jetbrains-mono
# 或者：
# fc-cache -fv 之后用 fc-list | grep -i "noto sans cjk" 确认
```

preamble 里写了 4 级中文 fallback、3 级西文 fallback、2 级 monospace fallback，
即使没装这些字体也能编出 PDF（视觉略损）。

---

## 如何使用

### 场景 A · 一节课讲一个主题

直接全屏放对应的 `0?-*.pdf`。每份 deck 控制在 **29–30 页 / 60–90 分钟**，
含 5–8 个 TikZ 图、3–5 段关键代码、10+ 个 `file:line` 源码引用、6 道 Q&A。

讲师 cheat sheet：

- **第 1 页（cover）**：自我介绍 + 一句话引子（30s）
- **第 2 页（TOC）**：今天讲什么 + 不讲什么（1min）
- **第 3–4 页**：从一段代码 / 一个 bug 切入（5min）
- **中段**：核心机制 + TikZ 推演（30–60min，逐页讲解）
- **倒数第 3–4 页**：压测数字 / 选型表（5min）
- **末 3 页（appendix）**：现场 Q&A，鼓励翻"代码位置一览"页

### 场景 B · 一整门课串讲

按 01 → 02 → 03 → 04 → 05 顺序连讲，约 6 小时（可拆 2 天）。
推荐节奏：

| 时间 | 内容 |
|------|------|
| Day 1 上午 | Deck 1 幂等（60min）+ Deck 2 防超发（90min） |
| Day 1 下午 | Deck 3 缓存一致性（60min）+ Deck 5 限流熔断（60min） |
| Day 2 上午 | Deck 4 Outbox + Saga（90min，含事故复盘讨论） |

5 份 deck 设计成可\textbf{独立}也可\textbf{串讲}：

- Deck 4 末尾的"至少一次"语义会显式引用 Deck 1 的幂等中间件
- Deck 5 的中间件链顺序会复习 Deck 1 + Deck 2 + Deck 5 三者关系
- Deck 3 的回源锁可以作为 Deck 5 限流的过渡

### 场景 C · 单页打印学员讲义

```bash
# 一份 deck 打 4-up（4 页/A4 横）
pdftops -level3 01-idempotency.pdf - | \
  ps2pdf -dPDFSETTINGS=/prepress -sPAPERSIZE=a4 -dNUP=4 - 01-idempotency-handout.pdf

# 或者用 pdfjam（来自 texlive-extra-utils）
pdfjam --nup 2x2 --landscape --frame true 01-idempotency.pdf \
       -o 01-idempotency-handout.pdf
```

学员讲义建议附上：

- 5 份 deck 末页的「代码位置一览」（合订）
- `stressTest/REPORT.md` 的链路压测表
- `docs/blog/*.md` 5 篇博客（深度补充）

### 场景 D · 合订本 master.pdf

仓库里已经预编译好 `master.pdf`（封面 3 页 + 5 份 deck = 151 页 / ~1 MB），可直接用。
重新生成：

```bash
cd docs/slides
./build.sh --master
```

合订本结构：

| 页范围   | 内容 |
|----------|------|
| 1–3      | 合订本封面 + 目录 + 用法 |
| 4–32     | Deck 01 幂等中间件 |
| 33–62    | Deck 02 防超发 |
| 63–92    | Deck 03 缓存一致性 |
| 93–122   | Deck 04 Outbox + Saga |
| 123–151  | Deck 05 限流熔断 |

### 场景 E · 面试前自查

5 份 deck 的每份末 3 页是「面试 Q&A（上）/（下）/ 代码位置一览」。
建议面试前一天：

1. 把每份 deck 末 3 页打印出来当 cheat sheet
2. 每道 Q 自己先说一遍，再翻 A 校对
3. 翻不出来的题去对应博客 `docs/blog/0?-*.md` 补课

---

## 设计原则（给后续维护者）

- **数据驱动**：每份 deck 至少 ≥ 1 段直接来自 `stressTest/REPORT.md` 的真实数字。
- **源码可追溯**：所有"我们这样实现"的论断必须附 `file:line` 引用，方便学员现场翻代码。
- **TikZ 优先**：能画图就别堆字。每份 deck ≥ 4 个 TikZ 图（时序、状态机、链路、对比）。
- **色盲友好**：状态机三态用蓝/橙/灰，避免红+绿（详见 `preamble.tex` 中的 `node-state-normal/alert/transition` 风格）。
- **末页固定**：倒数第 3-2 页 = 「面试 Q&A（上）/（下）」，末页 = 「代码位置一览」。
- **页数硬约束**：每份 deck 落在 [20, 30] 页，`build.sh` 会自动检查。

---

## 修改 deck 的建议工作流

1. 改 `0?-*.tex`（或 `preamble.tex`）
2. `./build.sh 0?` 重新编译目标 deck（约 5–10s）
3. `pdfinfo 0?-*.pdf | grep Pages` 确认页数仍在 [20, 30]
4. `pdftotext 0?-*.pdf -` 抽文本检查"教学项目"等禁用词没出现
5. 改完全部 5 份后 `./build.sh` 跑全量回归

---

## License

源代码与文档同仓库 `gomall` 项目一致（见根 `LICENSE`）。
