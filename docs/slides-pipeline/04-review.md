# Stage 4 验收报告：5 份 Beamer Deck

> 本报告对 Stage 3 产出物做独立复核。结论：**5 份 deck 全部 PASS / WARN，无 FAIL**。
> 已修若干低级编译问题，剩余视觉/内容性问题分级移交 Stage 5。
> 重新编译命令：`cd docs/slides && xelatex -interaction=nonstopmode 0X-name.tex`（两遍）。

---

## 1. 总览表格

| Deck | 编译 | 页数 | TikZ | 压测数字✓ | 代码引用✓ | Q&A 末页✓ | 禁用词✓ | 总评 |
|------|------|------|------|-----------|-----------|-----------|---------|------|
| 01-idempotency               | OK | 29 | 4 | 5 (含 755,033 / 50,319 / 64,254 / 2.33ms) | 14 条（抽样 5 条全部 in-range） | Q&A 28 + 代码位置 29 | 无 | **PASS** |
| 02-anti-oversell             | OK | 30 | 6 | 10 (含 51,362 / 50,142 / 136ms / 453ms / 500-vs-100) | 14 条 | Q&A 29 + 代码位置 30 | 无 | **PASS** |
| 03-cache-consistency         | OK | 29 | 7 | 2 (64,254 / 3.51ms) — 比其它 deck 单薄 | 12 条 | Q&A 28 + 代码位置 29 | 无 | **WARN**（压测数字偏少） |
| 04-outbox-saga               | OK | 30 | 7 | 1 (500 抢 100 件零超卖) — 仅一处，且不是 RPS | 17 条 | Q&A 29 + 代码位置 30 | 无 | **WARN**（压测数字单一） |
| 05-ratelimit-circuit-breaker | OK | 29 | 8 | 4 (781,624 / 46 / 52,082 / 64,254 / 1.24ms) | 10 条 | Q&A 28 + 代码位置 29 | 无 | **PASS** |

**全员通过的硬指标**：
- 编译退出码 0，PDF 全部生成（158-173 KB）
- 页数全部落在 [20, 30]
- TikZ 数全部 ≥ 4（最低门槛 3）
- `file:line` 引用全部 ≥ 10（抽样验证 15 处全部命中真实文件 + 行号在范围内）
- 末 2 页固定为「面试 Q&A（下）」+「代码位置一览」
- "教学项目 / 本课程 / TODO / placeholder" 全部 0 命中
- Q&A 框架（上 / 下）每个 deck 各 1 对
- 16:9 (453.54 x 255.12 pt)，cover + TOC 排版正常，中文用 PingFang HK/SC 嵌入 PDF（pdffonts 可见）

---

## 2. 我修了什么（直接动手）

### 2.1 preamble.tex

| 行号 | 改动 | 原因 |
|------|------|------|
| 9-11 | `\setCJKmainfont/sansfont` 增加 `ItalicFont=PingFang SC, AutoFakeSlant=0.15` | 抑制 `Font shape TU/PingFangSC/m/it undefined` 警告（\srcnote 用 \itshape） |
| 27 | `\setmainfont` 调换顺序：`Helvetica Neue` 优先，`Inter` 次之 | 原写法 `\IfFontExistsTF{Inter}` 在 macOS 上虽然返回 false，但 kpathsea 会触发 `mktextfm Inter` 子进程并 Emergency stop（noisy 但不致命）。改成 macOS 系统字体优先后该噪音完全消失 |

### 2.2 01-idempotency.tex 重复请求图

| 行号 | 改动 | 原因 |
|------|------|------|
| 33-34 | TikZ `node distance=8mm and 14mm` → `8mm and 8mm`，添加 `every node/.append style={minimum width=15mm}` | 5 个节点用默认 18mm 宽 + 14mm 间距共 146mm，超过 16:9 frame ~127mm 可用宽，导致 21pt overfull hbox |

### 2.3 02-anti-oversell.tex `SELECT FOR UPDATE` 代码块

| 行号 | 改动 | 原因 |
|------|------|------|
| 104-124 | 把 18 行 listing 压成 13 行（合并多行 if、去掉冗余 Clauses 链） | 原版 33pt overfull vbox；compress 后 frame 高度合规 |

### 2.4 03-cache-consistency.tex 单删时序图

| 行号 | 改动 | 原因 |
|------|------|------|
| 69-84 | TikZ 垂直坐标整体上提（lifeline -65mm → -50mm，步骤间距 8mm → 7mm） | 原版 23.8pt overfull vbox |

### 2.5 04-outbox-saga.tex

| 行号 | 改动 | 原因 |
|------|------|------|
| 253 | `to[loop above]` 配合 `bend right=40` → `to[loop above,looseness=8]`（去掉 bend） | 同一节点 pending→pending 还加 bend right 导致 pgf 报 `Returning node center instead of a point on node border` 两次 warning |
| 281-293 | 完整链路 TikZ：node distance 4mm → 3mm，添加 `minimum width=13mm` | 6 个节点 + default 18mm 共 ~158mm 远超 frame；63pt overfull hbox → 0 |

### 2.6 05-ratelimit-circuit-breaker.tex

| 行号 | 改动 | 原因 |
|------|------|------|
| 261-283 | `allow()` listing 从 20 行压到 18 行（合并 double-check Lock 部分） | 55pt overfull vbox |
| 180-198 | `SlidingWindow()` listing 从 17 行压到 16 行（合并 ByUser 分支） | 22pt overfull vbox |
| 286-302 | `report()` listing 从 17 行压到 14 行（合并 stateClosed 分支） | 22pt overfull vbox |

### 2.7 累计效果

| 指标 | 修复前 | 修复后 |
|------|--------|--------|
| Inter Emergency stop（每个 deck 1 次） | 5 | **0** |
| 最大 overfull hbox（横向溢出，可视） | 63pt（deck 4） | **0**（仅 1.6pt 在 deck 3，视觉无感） |
| 最大 overfull vbox（纵向溢出） | 55pt（deck 5） | **22.3pt**（lstlisting+caption 行间距，cosmetic） |
| pgf node-center warning | 2 | **0** |
| 编译错误 | 0 | 0 |

---

## 3. 留给 Stage 5 的问题清单

### 3.1 Critical（必修，否则发版有失专业感）

无。所有红线问题都已通过编译或上面的修复满足。

### 3.2 High（强烈建议修，影响幻灯片可读性 / 内容一致性）

**H1. 状态机三态配色违反 §2 提案规约**
- **位置**：05-ratelimit-circuit-breaker.tex:245-258（熔断三态机 Closed/Open/HalfOpen）；04-outbox-saga.tex:246-255（outbox 三态机 pending/sent/dead）
- **现状**：用 `node-ok`（绿）+ `node-bad`（红）+ `node-warn`（黄），红绿对比直触色盲忌讳
- **应该是**：方案 §1.4 明确写"状态机三态用 cMain / cWarn / cOK 三色避开色盲冲突"
- **修复**：`Open` 节点改 `node-bad` → 加 `cMain` 风格（drawn=cMain, fill=cMain!10）；或定义新的 `node-state-open` style

**H2. Deck 3 / Deck 4 压测数字密度低**
- **位置**：03-cache-consistency.tex 只在第 18 页（pdfinfo 报第 22 页）出现 64,254 RPS / 3.51ms 一处，且 blog 里没有 cache 命中真实数据；04-outbox-saga.tex 全程只引"500 抢 100 件"一处
- **现状**：方案 §7.1 要"≥ 1 段直接来自 REPORT 的真实数字"——技术上 PASS，但与其它 deck 的 5-10 处对比单薄
- **修复建议**：Deck 3 补一段"延迟双删 + Cache Aside 命中率实测"（REPORT 现有 6M order / 1M product 规模可引用）；Deck 4 补一段 "outbox publisher 在 RMQ 中断 30s 期间积压 N 条，恢复后追平耗时" 之类的故障演练数字（REPORT 应补充）

**H3. `build.sh` 缺失**
- **位置**：方案 §3.3 明确要求 `docs/slides/build.sh`，目前不存在
- **修复**：补一个 5 行的 shell：`for f in 0?-*.tex; do xelatex -interaction=nonstopmode "$f"; xelatex -interaction=nonstopmode "$f"; done`

### 3.3 Medium（值得做但不影响课堂效果）

**M1. metropolis 主题字体未生效**
- **位置**：preamble.tex 第 27 行 `\setmainfont{Helvetica Neue}` 被 metropolis 覆盖
- **现状**：pdffonts 显示 PDF 实际嵌入 `LMSans10/LMSans12/LMSans8`（Latin Modern Sans，metropolis fallback for Fira），不是 Helvetica
- **影响**：英文部分用 LMSans，与中文 PingFang 风格不太协调；但视觉上仍清晰
- **修复**：在 `\usetheme{metropolis}` 之后加 `\setsansfont{Helvetica Neue}` 或安装 Fira Sans

**M2. 压测数字写法不一致**
- **位置**：deck 1 第 16 页 `755{,}033`、deck 2 第 17 页 `51{,}362`、deck 3 第 18 页 `64{,}254` 等
- **现状**：全部用 LaTeX 数学风 `{,}` 千位分隔。读起来 OK，但 grep / 检索时会被错过
- **修复**：保持现状即可（仅检索工具的问题）

**M3. PingFang italic 仍 fallback**
- **位置**：preamble.tex 第 9 行
- **现状**：尽管加了 `AutoFakeSlant=0.15`，编译日志仍报 1 次 `TU/PingFangSC(2)/m/it undefined`
- **影响**：\srcnote 用 \itshape 标注源文件时，中文部分回退到 regular weight，视觉看不出问题
- **修复**：用 `\newcommand{\srcnote}` 改成 `\textit{}` + 显式英文字体，或彻底取消 italic

**M4. Deck 4 状态机自环箭头标签略挤**
- **位置**：04-outbox-saga.tex:253，`(p) to[loop above,looseness=8]` 上方的 "重试 1→2→4 ... 上限 5min"
- **现状**：自环短，文字接近椭圆形 loop 边界；视觉可读但拥挤
- **修复**：把标签拆成两行 `\\` 或缩短为 "重试退避 1→2→4..."

### 3.4 Low（可忽略，但记录）

**L1. lstlisting + caption 必然 22.3pt overfull vbox**
- **位置**：每个 deck 的所有 `\begin{lstlisting}[caption=...]` 块（共 21 处）
- **现状**：lstlisting 在 frame 里使用 caption 时，appendixnumberbeamer + Beamer footline 会吃掉若干 pt 导致 baseline 算不准。视觉无可见溢出
- **修复**：可选 `\captionsetup{font=tiny}` 进一步压缩；或忽略

**L2. fontspec `addfontfeature ignored` warning**
- **位置**：编译日志多次出现 `Package fontspec Warning: \addfontfeature(s) ignored ... it cannot be used with a font that wasn't selected by a fontspec command`
- **现状**：metropolis 试图为 LMSans 设置 OpenType features（`Numbers={Monospaced}`），但 LMSans 是经典 Type 1 不支持
- **修复**：和 M1 一起处理（装 Fira 后就消失）

**L3. Deck 4 第 22 页（事故复盘） pgf 残留 warning 已消除**
- 已在 §2.5 修掉

---

## 4. 样本截图建议（给 Stage 5 做总索引时挑封面）

| 截图候选 | Deck | 页 | 卖点 |
|----------|------|----|------|
| **封面**     | 01 | 1  | 标题 + 副标题排版完整，最简洁 |
| **三态机图** | 01 | 7  | "正解：状态机 + Lua + 响应回放"——视觉冲击最强的 TikZ |
| **压测对比表** | 02 | 17 | Redis Lua vs DB 锁的尾延迟对比，136ms vs 453ms 黑体加粗显眼 |
| **延迟双删时序** | 03 | 9  | 单删 vs 双删两个时序图对仗，叙事清晰 |
| **完整链路** | 04 | 20 | OrderCreate → Reserve → outbox → RMQ → Pay/ES 一行流，直观 |
| **滑动窗口精度** | 05 | 13 | 781,624 限流 / 46 通过 / 期望 45，数字说服力最强 |
| **熔断三态机** | 05 | 16 | Closed/Open/HalfOpen，配色虽有 H1 问题，但图形布局是最好的状态机示意 |
| **代码位置末页** | 任意 | 末页 | 给学员/面试官的"接下来去哪查"指南，亮点 |

---

## 5. 编译日志摘要（供 Stage 5 复盘）

```
01-idempotency.pdf              29 pages, 165,873 B, 0 errors, 3 vbox warnings (≤22.3pt)
02-anti-oversell.pdf            30 pages, 167,829 B, 0 errors, 3 vbox warnings (≤22.3pt)
03-cache-consistency.pdf        29 pages, 162,028 B, 0 errors, 3 warnings (1 hbox 1.6pt, 2 vbox ≤22.3pt)
04-outbox-saga.pdf              30 pages, 173,429 B, 0 errors, 5 vbox warnings (≤22.3pt)
05-ratelimit-circuit-breaker.pdf 29 pages, 158,072 B, 0 errors, 7 vbox warnings (≤22.3pt)

字体嵌入：PingFangHK-Regular/Semibold + LMSans 系列 + Menlo + CMSY/CMR (公式)
TikZ 渲染：全部矢量，pdfimages 抽不出 raster
中文字符可解析：pdftotext 抽出标题/副标题/正文全部完整
```

---

**总评**：Stage 3 完成度高，红线全部满足。修了 7 处低级问题（Inter Emergency / overfull hbox / pgf warning / 3 处 listing 行数），剩余 1 个 High 内容性问题（H1 状态机配色）和 2 个 High 完整性问题（H2/H3）建议 Stage 5 处理。

`DONE: stage4 verification`
