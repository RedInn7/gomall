# 方案文档：gomall 5 大特性 Beamer Deck 工程蓝图

> 流水线 Stage 2 产出物。Stage 3 执行 agent 凭此即可完成 5 份 `.tex` 的撰写，不再需要"创造"。
> 上游：`01-requirements.md` + 5 篇博客 + `stressTest/REPORT.md` + 仓库 `main` 分支代码。
> 下游：`docs/slides/preamble.tex` + `docs/slides/0X-{topic}.tex` × 5 + `docs/slides/build.sh`。

---

## 0. 阅读地图

| 章节 | 内容 | 给 Stage 3 用来…… |
|------|------|---------------------|
| §1 | LaTeX 包选型 + 主题决策 | 写 preamble 前确认每个 `\usepackage` 都有理由 |
| §2 | 完整可粘贴 preamble | 一字不改放进 `preamble.tex` |
| §3 | 文件结构 + 编译流程 | 创建目录、`build.sh`、CI 钩子 |
| §4 | TikZ 图模板（5 种） | 每张图按伪代码草图改一改即可 |
| §5 | listings 配置（Go/Lua/SQL） | 复制 + 调整 `firstline`/`lastline` |
| §6 | 5 个 deck 的 page-by-page 表格 | 逐页翻译，不再思考结构 |
| §7 | 验收 checklist | Stage 4 直接对照 |

---

## 1. LaTeX 包选型与决策

### 1.1 引擎与文档类

| 项 | 选择 | 为什么 |
|----|------|--------|
| 引擎 | `xelatex` | 中文字体（Source Han / 系统 CJK）需要 OpenType；`pdflatex` 无法接 `fontspec` 的真字体 |
| 文档类 | `\documentclass[aspectratio=169,11pt]{beamer}` | 投影仪/笔记本均为 16:9；11pt 在 169 模式下行距正好不挤 |
| 主题 | `metropolis` 主 / `madrid` 备 | Metropolis 极简、无装饰、工程师气质；`ctex` 下个别版本 footline 会和中文字体打架，备 Madrid 兜底 |
| 颜色主题 | 自定义 + `\setbeamercolor` 覆盖 | 主色 `#1F4E79` 与 Metropolis 默认蓝有违和，统一收口 |

### 1.2 必装宏包

| 包 | 用途 | 备注 |
|----|------|------|
| `ctex` | 中文 | 必须放在 beamer 之后、`fontspec` 之前；`UTF8` 选项默认 |
| `fontspec` | 指定中英文字体 | `ctex` 已隐式加载，但显式声明以便 fallback |
| `tikz` | 所有图 | 加 5 个常用 library |
| `listings` | 代码块 | 中文行号、Go/Lua/SQL 三套配色 |
| `xcolor` | 自定义颜色 | `\definecolor` |
| `booktabs` | 选型对比表 | 不要 `\hline`，统一 `\toprule/\midrule/\bottomrule` |
| `tcolorbox` | 关键论点框 | 在"问题/解答/坑"页用；可省，备用 |
| `appendixnumberbeamer` | 末尾 Q&A 页不进总页码 | 让 `当前页/总页数` 更准 |

TikZ libraries：`positioning, arrows.meta, fit, shapes.geometric, calc, decorations.pathmorphing, backgrounds`

### 1.3 字体策略（macOS 优先 + 跨平台 fallback）

| 角色 | 首选 | macOS fallback | Linux fallback |
|------|------|----------------|----------------|
| 中文正文 sans | PingFang SC | Source Han Sans SC | Noto Sans CJK SC |
| 中文标题 serif | Songti SC | Source Han Serif SC | Noto Serif CJK SC |
| 英文正文 | Inter / system | Helvetica Neue | Latin Modern Sans |
| 等宽 / 代码 | JetBrains Mono | Menlo | DejaVu Sans Mono |

**fallback 写法**：用 `\IfFontExistsTF{NAME}{...}{...}` 包裹 `\setmainfont`，让缺字体时不报错。

### 1.4 颜色 token

| 名字 | hex | 用途 |
|------|-----|------|
| `cMain` | `#1F4E79` | 标题、主轴 |
| `cAccent` | `#E07A1F` | 强调数字、警示 |
| `cGray` | `#595959` | 次级文本、来源标注 |
| `cMute` | `#F4F4F4` | 代码块底色、表头底 |
| `cOK` | `#2E7D32` | 状态机 "done" / success path |
| `cBad` | `#B23A3A` | 错误路径，**禁止**和 cOK 同图直接对比（色盲） |
| `cWarn` | `#C8A300` | "processing" 中间态 |

红 + 绿不同时出现；状态机三态用 `cMain / cWarn / cOK` 三色避开色盲冲突。

---

## 2. preamble.tex 完整模板（直接粘贴）

```latex
% docs/slides/preamble.tex —— 五份 deck 共享
\documentclass[aspectratio=169,11pt]{beamer}

% ==== 中文与字体 ====
\usepackage[UTF8,fontset=none]{ctex}
\usepackage{fontspec}

% macOS 默认字体优先；缺失时按顺序回退
\IfFontExistsTF{PingFang SC}{%
  \setCJKmainfont{PingFang SC}[BoldFont=PingFang SC,ItalicFont=Songti SC]
  \setCJKsansfont{PingFang SC}
  \setCJKmonofont{PingFang SC}
}{%
  \IfFontExistsTF{Source Han Sans SC}{%
    \setCJKmainfont{Source Han Sans SC}
    \setCJKsansfont{Source Han Sans SC}
  }{%
    \setCJKmainfont{Noto Sans CJK SC}
    \setCJKsansfont{Noto Sans CJK SC}
  }
}

\IfFontExistsTF{Inter}{\setmainfont{Inter}}{\setmainfont{Helvetica Neue}}
\IfFontExistsTF{JetBrains Mono}{\setmonofont{JetBrains Mono}[Scale=0.85]}%
  {\setmonofont{Menlo}[Scale=0.85]}

% ==== 颜色 ====
\usepackage{xcolor}
\definecolor{cMain}{HTML}{1F4E79}
\definecolor{cAccent}{HTML}{E07A1F}
\definecolor{cGray}{HTML}{595959}
\definecolor{cMute}{HTML}{F4F4F4}
\definecolor{cOK}{HTML}{2E7D32}
\definecolor{cBad}{HTML}{B23A3A}
\definecolor{cWarn}{HTML}{C8A300}

% ==== 主题 ====
\usetheme{metropolis}
\metroset{progressbar=frametitle,numbering=fraction,block=fill}
\setbeamercolor{frametitle}{bg=cMain,fg=white}
\setbeamercolor{progress bar}{fg=cAccent,bg=cMute}
\setbeamercolor{alerted text}{fg=cAccent}
\setbeamercolor{title}{fg=cMain}

% ==== TikZ ====
\usepackage{tikz}
\usetikzlibrary{positioning,arrows.meta,fit,shapes.geometric,calc,%
  decorations.pathmorphing,backgrounds}

% 常用样式宏
\tikzset{
  node-box/.style={draw=cMain,thick,rounded corners=2pt,
    minimum height=8mm,minimum width=18mm,align=center,font=\small},
  node-state/.style={draw=cMain,thick,circle,minimum size=14mm,
    align=center,font=\small},
  node-ok/.style={draw=cOK,thick,fill=cOK!10},
  node-warn/.style={draw=cWarn,thick,fill=cWarn!15},
  node-bad/.style={draw=cBad,thick,fill=cBad!10},
  arrow/.style={-{Stealth[length=2mm]},thick,draw=cMain},
  arrow-async/.style={-{Stealth[length=2mm]},thick,dashed,draw=cGray},
  arrow-bad/.style={-{Stealth[length=2mm]},thick,draw=cBad},
  lifeline/.style={dashed,thick,draw=cGray},
}

% ==== 代码 ====
\usepackage{listings}
\lstdefinelanguage{Go}{
  morekeywords={break,case,chan,const,continue,default,defer,else,
    fallthrough,for,func,go,goto,if,import,interface,map,package,range,
    return,select,struct,switch,type,var,nil,true,false,iota},
  morekeywords=[2]{string,int,int64,uint,uint64,bool,byte,error,context},
  sensitive=true,
  morecomment=[l]//,morecomment=[s]{/*}{*/},
  morestring=[b]",morestring=[b]`,
}
\lstdefinelanguage{Lua-redis}[]{Lua}{
  morekeywords=[2]{redis,call,KEYS,ARGV,tonumber,HGET,HSET,GET,SET,
    DECR,DECRBY,INCR,INCRBY,EXPIRE,PEXPIRE,ZADD,ZCARD,ZREMRANGEBYSCORE,
    EVAL,EVALSHA},
  sensitive=true,
}

\lstdefinestyle{base}{
  basicstyle=\ttfamily\scriptsize,
  numbers=left,numberstyle=\tiny\color{cGray},
  keywordstyle=\color{cMain}\bfseries,
  keywordstyle=[2]\color{cAccent},
  stringstyle=\color{cOK},
  commentstyle=\color{cGray}\itshape,
  backgroundcolor=\color{cMute},
  frame=single,framerule=0pt,
  showstringspaces=false,
  breaklines=true,
  tabsize=2,
  columns=fullflexible,
  literate={→}{{$\rightarrow$}}1
           {▶}{{$\blacktriangleright$}}1,
}
\lstset{style=base}

% ==== 三线表与附录页码 ====
\usepackage{booktabs}
\usepackage{appendixnumberbeamer}

% ==== 页脚来源标注 ====
\newcommand{\srcnote}[1]{{\color{cGray}\tiny\textit{#1}}}

% 关键论点框
\newcommand{\keypoint}[1]{%
  \begin{tcolorbox}[colback=cMute,colframe=cMain,boxrule=0.5pt,arc=2pt]%
  #1\end{tcolorbox}}
\usepackage{tcolorbox}
```

**为什么这么排版**：

- `ctex` 在 `\documentclass` 之后立即加载，`fontset=none` 让我们自己接管字体（默认会强塞旧版 Adobe Song）。
- `\IfFontExistsTF` 来自 `iftex` 系列宏，xelatex 自带，**不需要额外 `\usepackage`**。
- `\metroset` 启用 Metropolis 的进度条；`numbering=fraction` 自动给出"7 / 24"格式。
- `tikzset` 把所有图共用的样式写成宏，每张图只关心节点位置，**降低 Stage 3 的画图门槛**。

---

## 3. 文件结构与编译流程

### 3.1 目录布局

```
docs/slides/
├── preamble.tex                 # §2 整段
├── 01-idempotency.tex           # Deck 1
├── 02-anti-oversell.tex         # Deck 2
├── 03-cache-consistency.tex     # Deck 3
├── 04-outbox-saga.tex           # Deck 4
├── 05-ratelimit-circuit.tex     # Deck 5
├── build.sh                     # 一键编译
└── out/                         # PDF 产物（gitignore）
```

**约定**：TikZ 图、listings 全部 **inline** 在 root .tex 里，不拆外部 `.tikz` / `.go`。理由：deck 是讲义不是项目，inline 让一个文件就能讲清一个主题，**讲师改一处就能动**。

### 3.2 每个 root tex 头部模板

```latex
\input{preamble}

\title{幂等中间件设计}
\subtitle{从 Idempotency-Key 到 Redis Lua 状态机}
\author{gomall · 后端实战}
\date{}

\begin{document}
\maketitle

% 第 2 页：TOC
\begin{frame}{今日大纲}\tableofcontents\end{frame}

\section{为什么需要幂等}
% ...

\appendix
\section*{面试 Q\&A}
% ...
\end{document}
```

### 3.3 `build.sh`

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p out
for f in 0?-*.tex; do
  name="${f%.tex}"
  xelatex -interaction=nonstopmode -output-directory=out "$f"
  xelatex -interaction=nonstopmode -output-directory=out "$f"  # 第二次解 TOC + 总页数
done
ls -1 out/*.pdf
```

**为什么编译两次**：第一次让 `\tableofcontents`、`appendixnumberbeamer` 的总页数写进 `.aux`，第二次才能正确渲染。

### 3.4 CJK 冒烟测试（在 Stage 3 完成时跑）

```bash
# 在 docs/slides 目录下
echo '\input{preamble}\begin{document}\begin{frame}{测试}中文 + English + 0.91ms\end{frame}\end{document}' > /tmp/smoke.tex
xelatex -output-directory=/tmp /tmp/smoke.tex && open /tmp/smoke.pdf
```

肉眼看：中文不出方块、等宽字体不错位、metropolis 标题色为 `cMain`。

---

## 4. TikZ 图模板（5 个标准草图）

### 4.1 三态机（Deck 1 主图）

```latex
\begin{tikzpicture}[node distance=22mm]
  \node[node-state] (init) {init};
  \node[node-state,node-warn,right=of init] (proc) {processing};
  \node[node-state,node-ok,right=of proc] (done) {done};

  \draw[arrow] (init) -- node[above,font=\scriptsize]{acquire (Lua)} (proc);
  \draw[arrow] (proc) -- node[above,font=\scriptsize]{commit + cache body} (done);
  \draw[arrow,bend left=30] (proc) to node[below,font=\scriptsize]{release (4xx/5xx)} (init);
  \draw[arrow-async,bend left=40] (done) to node[above,font=\scriptsize]{TTL expire} ([yshift=8mm]init.north);

  \node[font=\scriptsize\color{cGray},below=2mm of proc]
    {返回 1=拿到锁 / 2=回放 done / 3=processing 拒绝 / 0=token 不存在};
\end{tikzpicture}
```

### 4.2 时序图：双线程 + DB + Redis（Deck 3 双删 / Deck 1 重放）

```latex
\begin{tikzpicture}[node distance=12mm]
  % 四条 lifeline
  \foreach \name/\x in {A 线程/0, B 线程/30, Redis/60, DB/90} {
    \node[node-box,minimum width=14mm] at (\x mm,0) (\name) {\name};
    \draw[lifeline] (\x mm,-2mm) -- (\x mm,-55mm);
  }
  % 步骤 1: A miss
  \draw[arrow] (0,-8mm) -- node[above,font=\tiny]{GET miss} (60mm,-8mm);
  % 步骤 2: B UPDATE
  \draw[arrow] (30mm,-16mm) -- node[above,font=\tiny]{UPDATE} (90mm,-16mm);
  % 步骤 3: B DEL
  \draw[arrow] (30mm,-24mm) -- node[above,font=\tiny]{DEL} (60mm,-24mm);
  % 步骤 4: A SELECT (slave 旧数据)
  \draw[arrow-bad] (0,-32mm) -- node[above,font=\tiny\color{cBad}]{SELECT → 旧} (90mm,-32mm);
  % 步骤 5: A SET 回填
  \draw[arrow-bad] (0,-40mm) -- node[above,font=\tiny\color{cBad}]{SET 旧值} (60mm,-40mm);
  % 步骤 6: 异步第二次删
  \draw[arrow-async] (30mm,-50mm) -- node[above,font=\tiny]{DEL again (500ms 后)} (60mm,-50mm);
\end{tikzpicture}
```

### 4.3 架构图：HTTP → 中间件链 → service → repo → MQ（Deck 4/5 通用）

```latex
\begin{tikzpicture}[node distance=10mm and 12mm]
  \node[node-box] (cli) {Client};
  \node[node-box,right=of cli] (mw1) {RateLimit};
  \node[node-box,right=of mw1] (mw2) {CircuitBreak};
  \node[node-box,right=of mw2] (mw3) {Idempotency};
  \node[node-box,below=of mw2] (svc) {Order Service};
  \node[node-box,left=of svc] (redis) {Redis (Lua)};
  \node[node-box,right=of svc] (db) {MySQL\\(order + outbox)};
  \node[node-box,below=of svc] (pub) {Outbox Publisher};
  \node[node-box,right=of pub] (mq) {RabbitMQ};

  \draw[arrow] (cli) -- (mw1); \draw[arrow] (mw1) -- (mw2);
  \draw[arrow] (mw2) -- (mw3); \draw[arrow] (mw3) |- (svc);
  \draw[arrow] (svc) -- (redis); \draw[arrow] (svc) -- (db);
  \draw[arrow-async] (pub) -- (mq);
  \draw[arrow] (db) -- node[right,font=\tiny]{poll} (pub);
\end{tikzpicture}
```

### 4.4 熔断三态机（Deck 5 主图）

```latex
\begin{tikzpicture}[node distance=26mm]
  \node[node-state,node-ok] (cl) {Closed};
  \node[node-state,node-bad,right=of cl] (op) {Open};
  \node[node-state,node-warn,below=20mm of op] (ho) {HalfOpen};

  \draw[arrow] (cl) -- node[above,font=\tiny]{failures \(\geq\) threshold} (op);
  \draw[arrow] (op) -- node[right,font=\tiny]{wait OpenTimeout} (ho);
  \draw[arrow,bend left=20] (ho) to node[right,font=\tiny]{探测全成功} (cl);
  \draw[arrow-bad,bend left=20] (ho) to node[below,font=\tiny]{任一失败} (op);
\end{tikzpicture}
```

### 4.5 双桶库存（Deck 2 + Deck 4）

```latex
\begin{tikzpicture}[node distance=28mm]
  \node[node-box,minimum width=30mm,minimum height=18mm,node-ok] (av) {available\\\(\mathtt{stock:available:\{pid\}}\)};
  \node[node-box,minimum width=30mm,minimum height=18mm,node-warn,right=of av] (rv) {reserved\\\(\mathtt{stock:reserved:\{pid\}}\)};
  \node[font=\scriptsize,below=8mm of av] (sold) {真正消耗};

  \draw[arrow,bend left=18] (av) to node[above,font=\tiny]{reserve(n) Lua} (rv);
  \draw[arrow,bend left=18] (rv) to node[below,font=\tiny]{release(n) 取消/超时} (av);
  \draw[arrow] (rv) -- node[right,font=\tiny]{commit(n) 支付成功} ($(rv)+(0,-18mm)$);
\end{tikzpicture}
```

### 4.6 Outbox 流水线（Deck 4 第二主图）

```latex
\begin{tikzpicture}[node distance=14mm and 16mm]
  \node[node-box] (biz) {OrderCreate};
  \node[node-box,right=of biz,minimum width=30mm] (tx)
    {TX:\\order.insert\\outbox.insert};
  \node[node-box,below=of tx,node-warn] (poll) {Publisher\\(goroutine)};
  \node[node-box,right=of poll] (mq) {RabbitMQ};
  \node[node-box,below=of poll] (cons) {Consumer\\(ES indexer)};

  \draw[arrow] (biz) -- (tx);
  \draw[arrow] (tx) -- node[right,font=\tiny]{FetchBatch} (poll);
  \draw[arrow] (poll) -- node[above,font=\tiny]{publish + MarkSent} (mq);
  \draw[arrow-async] (mq) -- (cons);
  \draw[arrow-bad,bend left=30] (poll.east) to node[above,font=\tiny]{fail → MarkFailed (退避)} ([yshift=-4mm]poll.east);
\end{tikzpicture}
```

---

## 5. listings 配置示例（Go / Lua）

### 5.1 Go 代码块（带行号映射）

```latex
\begin{lstlisting}[language=Go,firstnumber=35,
  caption={\srcnote{middleware/idempotency.go:35--70}}]
func Idempotency() gin.HandlerFunc {
    return func(c *gin.Context) {
        key := c.GetHeader(IdempotencyHeader)
        if key == "" { c.Next(); return }
        userKey := fmt.Sprintf("idemp:%d:%s", uid(c), key)

        state, cached, _ := cache.AcquireIdempotencyLock(c, userKey)
        switch state {
        case 2: // done → 回放
            c.Header("X-Idempotent-Replay", "true")
            c.String(http.StatusOK, cached); c.Abort(); return
        case 3: c.JSON(409, gin.H{"msg":"处理中"}); c.Abort(); return
        case 0: c.JSON(400, gin.H{"msg":"token 无效"}); c.Abort(); return
        }
        // state == 1: 拿到锁，包 recorder
        rec := &responseRecorder{ResponseWriter:c.Writer, body:&bytes.Buffer{}}
        c.Writer = rec
        c.Next()
        if rec.Status() >= 400 || len(c.Errors) > 0 {
            cache.ReleaseIdempotencyLock(c, userKey); return
        }
        cache.CommitIdempotencyResult(c, userKey, rec.body.String())
    }
}
\end{lstlisting}
```

### 5.2 Lua 代码块

```latex
\begin{lstlisting}[language=Lua-redis,
  caption={\srcnote{repository/cache/coupon.go:23--49 抢券 Lua}}]
-- KEYS[1]=stock  KEYS[2]=user-flag
-- ARGV[1]=perUser  ARGV[2]=user-flag TTL
local stock = tonumber(redis.call('GET', KEYS[1]))
if stock == nil or stock <= 0 then return -1 end
local owned = tonumber(redis.call('GET', KEYS[2])) or 0
if owned >= tonumber(ARGV[1]) then return -2 end
redis.call('DECR', KEYS[1])
redis.call('INCR', KEYS[2])
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[2]))
return 1
\end{lstlisting}
```

**给 Stage 3 的注意事项**：

- `firstnumber=N` 让代码块行号与仓库真实行号对齐，**讲师指着"看这个第 38 行"时学员能直接定位**。
- `caption=` 内必须用 `\srcnote{...}` 标来源（路径:行号），满足"红线"。
- 单页代码 ≤ 18 行；超出请截取最关键部分，断点用注释 `// ...` 标。

---

## 6. 五个 Deck 的 page-by-page outline

> 每张表 = 一份 deck；行 = 一页幻灯片。Stage 3 按顺序翻译即可。
> 「类型」枚举：cover / TOC / hook / problem / wrong-way / solution / code / diagram / metrics / takeaway / qa。
> 「配图/代码」枚举：TikZ-state / TikZ-sequence / TikZ-arch / TikZ-bucket / TikZ-pipeline / code-go / code-lua / code-sql / table-cmp / table-metrics / 无。
> 「来源」枚举：blog §X / report §X / file:line。

### Deck 1 · 幂等中间件设计（24 页）

副标题：从 `Idempotency-Key` 到 Redis Lua 状态机
对应博客：`docs/blog/01-idempotency.md`

| 页 | 类型 | 内容简介 | 配图/代码 | 来源 |
|----|------|----------|-----------|------|
| 1 | cover | 标题 + 副标题 + "重复请求只生效一次" 一句话引子 | 无 | — |
| 2 | TOC | 五段：问题 / 错法 / 三态机 / 实现 / 性能 + Q&A | 无 | — |
| 3 | hook | 凌晨 2 点故事：用户点一次扣三次钱 | 无 | blog §"一个让人崩溃的场景" |
| 4 | problem | 重复请求的四种来源（用户狂点 / 网关重投 / MQ 重投 / client retry） | TikZ-arch (CDN→网关→BFF→服务) | blog §"不做幂等的代价" |
| 5 | wrong-way | 错法一：唯一索引硬扛——拿不到响应回放、跨表失效 | code-sql (`UNIQUE(user, key)`) | blog §"错误的做法 1" |
| 6 | wrong-way | 错法二：先 GET 再 SET——画 check-then-act 竞态时序 | TikZ-sequence (两个 goroutine 都进入) | blog §"错误的做法 2" |
| 7 | solution | 正解三要素：状态机 + Lua + 响应回放 | TikZ-state (init/processing/done) | blog §"正确姿势" |
| 8 | diagram | 三态机详图：四种 acquire 返回码 0/1/2/3 | TikZ-state §4.1 | repository/cache/idempotency.go:30-50 |
| 9 | code | Lua 脚本完整 13 行 | code-lua | repository/cache/idempotency.go:33-50 |
| 10 | takeaway | 为什么必须 Lua 而不是 GET+SET：Redis 单线程串行 + 原子性 | 无 | blog §"状态转移用 Lua 原子完成" |
| 11 | solution | 用户隔离：`idemp:{uid}:{key}` 防 token 撞库 | code-go (键名拼装) | blog §"用户隔离" |
| 12 | code | `responseRecorder` 包装：怎么拿到 gin 的响应体 | code-go (responseRecorder.Write) | middleware/idempotency.go:18-32 |
| 13 | code | 中间件主体：拿锁 → 执行 → commit / release | code-go (Idempotency handler) | middleware/idempotency.go:35-95 |
| 14 | diagram | 完整时序图：第二次请求命中 done 直接回放 | TikZ-sequence (Client / Mw / Redis) | blog §"正确姿势" |
| 15 | solution | 失败回滚的取舍：回到 init 让客户端能用同 token 重试 | TikZ-state with rollback edge | blog §"关键决策" |
| 16 | metrics | 压测数字：50 VU × 15s = 755,033 请求，DB 1 笔订单 | table-metrics | report §4 / blog §"验证" |
| 17 | metrics | p95 2.33ms：回放路径只走 Redis，比真实下单快一个量级 | table-metrics (ping vs idemp) | report §"链路压测结果" 第 1+4 行 |
| 18 | takeaway | TTL 怎么选：5 分钟兜底 token 泄漏；不设无限 | 无 | blog §"面试时" 第 1 题 |
| 19 | takeaway | Redis 挂了怎么办：safe default 失败，不放行 | 无 | blog §"面试时" 第 3 题 |
| 20 | takeaway | 幂等 ≠ 分布式锁 ≠ 去重表：边界对比 | table-cmp | blog §"面试时" + 需求 §2 Deck 1 |
| 21 | qa | 高频题 1-3：竞态 / 重试不重复扣款 / token 生命周期 | 无 | 需求 §"面试重点" |
| 22 | qa | 高频题 4-5：失败回滚 / 幂等 vs 分布式锁 | 无 | 需求 §"面试重点" |
| 23 | qa | 反追问：跨方法共用 token / `Idempotency-Key` 是不是规范 | 无 | blog §"面试时" 2+5 |
| 24 | takeaway | 代码位置一览（5 条 file:line） + 复现命令 | 无 | blog §"代码位置" |

### Deck 2 · 防超发与抢购库存（26 页）

副标题：Redis 原子扣减 vs `SELECT ... FOR UPDATE` 的实战对比
对应博客：`docs/blog/02-anti-oversell.md`

| 页 | 类型 | 内容简介 | 配图/代码 | 来源 |
|----|------|----------|-----------|------|
| 1 | cover | "100 张券 / 500 并发 / 实际成功恰好 100" | 无 | — |
| 2 | TOC | 故事 / 方案矩阵 / DB 锁 / Redis Lua / 性能 / 进阶 + Q&A | 无 | — |
| 3 | hook | 618 真实事故：发 10w 张实际发出 23w 张 | 无 | blog §"优惠券超发能赔多少钱" |
| 4 | problem | check-then-act 时序：两个 T 都看到 99,999 | TikZ-sequence | blog §"为什么 check-then-act" |
| 5 | wrong-way | 单条 SQL `if claimed<total then UPDATE` 行不通的根因 | code-go (3 行 bug 代码) | blog §"看起来天衣无缝" |
| 6 | solution | 方案矩阵：悲观锁 / 乐观锁 / DECR / Lua / Redlock | table-cmp (五行选型表) | blog §"防超发的几种方案" |
| 7 | solution | 我们项目实现哪两种、模式如何切换 | TikZ-arch (mode=db vs redis 分支) | service/coupon.go (mode 切换) |
| 8 | code | DB 悲观锁实现：`SELECT FOR UPDATE` + Count + Insert | code-go (ClaimWithDBLock) | repository/db/dao/coupon.go ClaimWithDBLock |
| 9 | diagram | DB 锁原理：行 X 锁强制串行，整个 batch 单点 | TikZ-sequence (事务串行) | blog §"方案一" |
| 10 | takeaway | DB 锁优劣：正确性满分 / 并发上限 ~200 QPS | 无 | blog §"实际表现" |
| 11 | solution | Redis Lua 双 key：stock + user-flag 一次原子 | TikZ-arch | blog §"方案二" |
| 12 | code | Lua 脚本 12 行：检查库存 + 检查 perUser + 双扣减 | code-lua | repository/cache/coupon.go:23-49 |
| 13 | takeaway | 为什么 Lua 能保证：Redis 单线程顺序执行整段脚本 | 无 | blog §"为什么这能保证不超发" |
| 14 | solution | 异步落库 + 回滚：Redis 成功但 DB 失败要 RollbackCouponStock | code-go (4 行错误处理) | service/coupon.go RollbackCouponStock 调用 |
| 15 | problem | "扣成功 + 落库失败 + 回滚失败" 的幽灵库存 → 定时对账 | TikZ-arch (Redis vs DB 对账) | blog §"这个回滚是有缝隙的" |
| 16 | code | 单测：500 goroutine 抢 100 张券，断言 success==100 | code-go (TestCoupon_AtomicClaimNoOversell) | repository/cache/coupon_test.go |
| 17 | metrics | 性能表：Redis Lua vs DB 锁 RPS / avg / max | table-metrics | report §6 |
| 18 | metrics | 重点看 max：DB 453ms vs Redis 136ms，尾延迟翻 3 倍 | TikZ-pipeline (柱状对比可选) | report §6 |
| 19 | takeaway | 抢资源类必须 Lua，DB 锁只配低频高一致场景 | 无 | blog §"性能对比" 结论 |
| 20 | solution | 进阶：下单的双桶库存 `available / reserved` | TikZ-bucket §4.5 | repository/cache/inventory.go |
| 21 | code | inventory.go reserve / commit / release 三脚本 API | code-go (函数签名 × 3) | repository/cache/inventory.go:69-128 |
| 22 | takeaway | 设计决策清单：库存源 / perUser / Redis 重启 / TTL / sha 缓存 | 无 | blog §"设计决策清单" |
| 23 | qa | Q1-2：500 并发不超发 / Redis 扣成功 DB 失败 | 无 | 需求 §"面试重点" |
| 24 | qa | Q3-4：FOR UPDATE 错索引锁升级 / 为什么不能 `if>0 then DECR` | 无 | 需求 + blog §"面试角度" |
| 25 | qa | Q5：热点商品分片库存策略 | TikZ-arch (按 hash 分桶) | 需求 §"面试重点" |
| 26 | takeaway | 代码位置（6 条） + 一句"想完整对比？跑 coupon_claim_*.js" | 无 | blog §"代码位置" |

### Deck 3 · 缓存一致性（22 页）

副标题：Cache Aside、延迟双删与 SETNX 回源锁
对应博客：`docs/blog/03-cache-consistency.md`

| 页 | 类型 | 内容简介 | 配图/代码 | 来源 |
|----|------|----------|-----------|------|
| 1 | cover | "为什么我必须删两次" | 无 | — |
| 2 | TOC | 问题本质 / 候选评比 / 单删时序 / 双删 / 读路径 / binlog | 无 | — |
| 3 | hook | 面试现场："先更新 DB 再删缓存" 一句话挂半数候选人 | 无 | blog §"你以为很简单的题" |
| 4 | problem | 一致性问题本质：两份数据 + 主从延迟 + 网络抖动 | TikZ-arch (App / Redis / Master / Slave) | blog §"问题的本质" |
| 5 | solution | 候选方案五行评比表 | table-cmp | blog §"候选方案大评比" |
| 6 | wrong-way | 单删时序：A miss → B update → B del → A 读 slave 旧值 → A 回填 | TikZ-sequence §4.2 | blog §"把单删画时序图" |
| 7 | takeaway | 关键点：A 读的是 slave，B 写的是 master，主从延迟是 race 之源 | 无 | blog §"注意这个 race 的精妙" |
| 8 | wrong-way | 反过来"先删再写"：窗口只是换位置 | TikZ-sequence (4 步缩小版) | blog §"错误方案：先删再写" |
| 9 | solution | 延迟双删四步：DEL → UPDATE → sleep → DEL again | TikZ-sequence §4.2 (双删版) | blog §"延迟双删的思路" |
| 10 | takeaway | 这不是强一致，是"窗口期可控的最终一致"；500ms 是经验值 | 无 | blog §"延迟双删" 尾段 |
| 11 | code | `ProductUpdate`：DelProductDetail → UpdateProduct → DoubleDeleteAsync | code-go | service/product.go:271-289 |
| 12 | code | `DoubleDeleteAsync` 实现：goroutine + Sleep + Del | code-go (8 行) | repository/cache/product.go:66-74 |
| 13 | problem | 读路径的缓存击穿：1000 并发同时 miss，全部打 DB | TikZ-sequence (1000 个 A 同时打) | blog §"读路径" |
| 14 | solution | SETNX 抢回源锁：单飞 + 跨进程 | TikZ-sequence (只有 1 个回源) | blog §"我们的解法" |
| 15 | code | `ProductShow` Cache Aside + TryProductLock + sleep 50ms | code-go (精简 15 行) | service/product.go:42-100 |
| 16 | takeaway | 为什么不用 Go 的 `singleflight`：跨进程不生效，必须 Redis | 无 | blog §"single flight 模式" |
| 17 | wrong-way | 双写 DB+Cache 强一致：易错点（写顺序 / 部分失败） | TikZ-sequence (4 步) | blog §"候选方案大评比" 第 3 行 |
| 18 | solution | 终极方案 `binlog→MQ→刷缓存`：成本与收益 | TikZ-pipeline (Canal→MQ→Consumer→Redis) | blog §"为什么不用订阅 binlog" |
| 19 | takeaway | 还有哪些坑：删失败重试 / 多层缓存 / TTL 雪崩抖动 | 无 | blog §"还有哪些坑" |
| 20 | metrics | 实测：6M order + 1M product，缓存命中 p95 3ms vs 无缓存 50ms+ | table-metrics | report §1 + blog 结尾 |
| 21 | qa | Q1-3：双写方案取舍 / 500ms 怎么定 / 击穿 vs 穿透 | 无 | 需求 §"面试重点" |
| 22 | qa | Q4-5：为什么删不更新 / TTL 雪崩抖动 + 代码位置 | 无 | 需求 §"面试重点" + blog §"代码位置" |

### Deck 4 · Outbox 模式与 Saga 补偿（28 页）

副标题：从 DB 事务能不能套住 MQ，到一次真实线上事故
对应博客：`docs/blog/04-outbox-saga.md`

| 页 | 类型 | 内容简介 | 配图/代码 | 来源 |
|----|------|----------|-----------|------|
| 1 | cover | "我在自己项目里发现了一个库存虚高 bug" | 无 | — |
| 2 | TOC | bug 发现 / 双写问题 / Outbox / Saga / 事故复盘 + Q&A | 无 | — |
| 3 | hook | 盘代码发现 `CancelUnpaidOrder` 在加库存，但 `OrderCreate` 从没扣 | code-go (孪生函数对照) | service/order_cancel.go:31-50 + service/order.go:40-60 |
| 4 | problem | 推演：10 单未支付超时 → 库存 100 变 110 → 持续膨胀 | 无 | blog §"故事开始" |
| 5 | solution | 修法 A vs B：加状态字段 vs 重设状态机 | table-cmp | blog §"修这个 bug 的两条路" |
| 6 | solution | 库存状态机：available / reserved / 真实消耗 | TikZ-bucket §4.5 | blog §"路 B" |
| 7 | code | Reserve Lua：available -= n / reserved += n 原子 | code-lua | repository/cache/inventory.go:30-46 |
| 8 | code | Commit / Release Lua（两份小脚本） | code-lua | repository/cache/inventory.go:48-66 |
| 9 | metrics | 单测 500 goroutine 抢 100 件实测零超卖 | code-go (TestInventory_NoOversellUnderConcurrency) | repository/cache/inventory_test.go |
| 10 | problem | 然而新问题：写 DB 成功 / 发 MQ 失败 → 事件永久丢失 | TikZ-sequence | blog §"但还有一个问题" |
| 11 | problem | 反过来：先发 MQ / DB 失败 → 下游看到鬼订单 | TikZ-sequence | blog §"但还有一个问题" |
| 12 | takeaway | 这就是"双写问题"——分布式系统里最经典的坑 | 无 | blog §"这就是双写问题" |
| 13 | solution | Outbox 思路：事件先存 DB 同事务，再异步投递 | TikZ-pipeline §4.6 | blog §"Outbox 模式" |
| 14 | code | `outbox_event` 表结构 8 个字段（status/attempts/next_retry_at） | code-sql | repository/db/model/outbox.go:17-37 |
| 15 | code | `OrderCreate` 重构后：业务 + outbox.Insert 同事务 | code-go | service/order.go:64-100 |
| 16 | code | `publisher.go` 主循环：FetchBatch → publish → MarkSent/Failed | code-go | service/outbox/publisher.go:51-95 |
| 17 | solution | 失败重试 + 指数退避 + dead 状态：1s→2s→4s 上限 5min | TikZ-state (pending→sent / pending→dead) | repository/db/dao/outbox.go:62-78 |
| 18 | takeaway | "至少一次"语义 → 消费者必须幂等（呼应 Deck 1） | 无 | blog §"配合幂等" |
| 19 | solution | 完整链路：Order/Pay/Cancel + 三脚本 + 三事件 | TikZ-pipeline (完整版) | blog §"完整链路：3 个特性串起来" |
| 20 | solution | Saga 编排式 vs 协同式：本项目用协同（事件驱动） | table-cmp | 需求 §"学习目标 4" |
| 21 | solution | 故障演练：停掉 RMQ，业务无感知；恢复后 30s 追平 | TikZ-pipeline (RMQ down 标 ×) | blog §"RabbitMQ 故障演练" |
| 22 | hook | 复盘真实事故：`util.LogrusObj` 在 InitLog 前被使用 | code-go (initOrder 错位) | report §"已修" |
| 23 | takeaway | 两条通用教训：初始化顺序 + recover 内不能再 panic | 无 | report §"已修" + 需求 §"必带元素" |
| 24 | takeaway | 什么时候该用 Outbox：可靠通知 + 审计 + 多消费者 | 无 | blog §"什么时候该用" |
| 25 | qa | Q1-2：为什么不能 commit 后发 MQ / Outbox 表挤压怎么办 | 无 | 需求 §"面试重点" |
| 26 | qa | Q3-4：Saga 补偿再失败 / 至少一次 vs 恰好一次 | 无 | 需求 §"面试重点" |
| 27 | qa | Q5：那次"初始化顺序"事故定位过程 | 无 | 需求 §"面试重点" 第 5 |
| 28 | takeaway | 代码位置（8 条） + 复现 RMQ 故障演练命令 | 无 | blog §"代码位置" |

### Deck 5 · 限流与熔断（22 页）

副标题：令牌桶、滑动窗口、三态熔断器一次性梳清
对应博客：`docs/blog/05-ratelimit-circuit-breaker.md`

| 页 | 类型 | 内容简介 | 配图/代码 | 来源 |
|----|------|----------|-----------|------|
| 1 | cover | "50 行代码挡住 99.99% 的滥用请求" | 无 | — |
| 2 | TOC | 为什么限流 / 三算法 / 单机桶 / 分布式窗口 / 熔断 + Q&A | 无 | — |
| 3 | hook | 误区："我能扛 5w RPS，不需要限流" | 无 | blog §"为什么单机扛得住" |
| 4 | problem | 限流四个目的：隔离故障 / 保护下游 / 公平 / 防滥用 | TikZ-arch (用户分级 / 下游容量) | blog §"为什么单机扛得住" |
| 5 | solution | 三算法对比：计数器 / 令牌桶 / 滑动窗口 | table-cmp | blog §"三种限流算法" |
| 6 | wrong-way | 计数器临界点突刺：1.99s 放 100 + 2.01s 再放 100 | TikZ-sequence (时间轴) | blog §"计数器" |
| 7 | solution | 令牌桶原理图：桶容量 burst / 速率 rate / 消耗令牌 | TikZ-arch (桶 + 水龙头) | blog §"令牌桶" |
| 8 | code | `TokenBucket` 中间件：`golang.org/x/time/rate` 包按 IP 创建 | code-go | middleware/ratelimit.go:22-50 |
| 9 | solution | 单机桶的硬伤：3 实例各算各，全局放行 ×3 | TikZ-arch (3 个 limiter 不共享) | blog §"分布式滑动窗口" |
| 10 | solution | 滑动窗口 ZSet 设计：member=请求 ID，score=时间戳 | TikZ-arch (ZSet 内部图) | blog §"分布式滑动窗口" |
| 11 | code | 滑动窗口 Lua 8 行：ZREMRANGEBYSCORE + ZCARD + ZADD | code-lua | repository/cache/ratelimit.go:17-35 |
| 12 | code | `SlidingWindow` 中间件：Scope + ByUser + nowMS | code-go | middleware/ratelimit.go:59-100 |
| 13 | metrics | 精度实测：15s × 3/s 期望 45，实际通过 46，误差 2% | table-metrics | report §5 |
| 14 | takeaway | ZSet 复杂度 O(logN)，1000 RPS 窗口 N≈1000，单 op <10μs | 无 | blog §"分布式滑动窗口" |
| 15 | problem | 熔断的痛点：下游 30s 超时，goroutine 堆积爆内存 | TikZ-sequence (3000 个 goroutine 等同一下游) | blog §"熔断：三态机" |
| 16 | solution | 三态机 Closed/Open/HalfOpen 转换条件 | TikZ-state §4.4 | blog §"熔断：三态机" |
| 17 | code | `allow()` 实现：原子读 state，HalfOpen 探测计数 | code-go | middleware/circuitbreaker.go:76-106 |
| 18 | code | `report()` 实现：失败累计 / HalfOpen 任一失败回 Open | code-go | middleware/circuitbreaker.go:108-128 |
| 19 | solution | 接入 `paydown`：熔断在外、幂等在内的中间件顺序 | TikZ-arch (mw chain) | blog §"接到 paydown" |
| 20 | takeaway | 选型决策：单服务=令牌桶 / 多实例=滑动窗口 / 外部依赖=熔断 | table-cmp | blog §"选型建议" |
| 21 | qa | Q1-3：突发 / 单机 vs 分布式 / 为什么 ZSet 不用 List | 无 | 需求 §"面试重点" |
| 22 | qa | Q4-5：half-open 探测数怎么选 / 限流是 429 还是 200+码 + 代码位置 | 无 | 需求 §"面试重点" + blog §"代码位置" |

---

## 7. 质量门槛 checklist（给 Stage 4 验收）

### 7.1 每份 deck 通用红线

- [ ] 页数在 20--30 之间（封 1 + TOC 1 + 正文 N + 末 2 页 Q&A + 代码位置）
- [ ] 第 1 页 cover、第 2 页 TOC、最后 2 页固定为 Q&A + 代码位置
- [ ] ≥ 3 张 TikZ 图（数 `\begin{tikzpicture}` 出现次数）
- [ ] ≥ 1 段直接来自 `stressTest/REPORT.md` 的真实数字（含具体数值，不是约数）
- [ ] ≥ 1 个 `file:line` 形式的代码引用（在 listings caption 或 srcnote 里）
- [ ] 没有 "教学项目" / "本课程" / TODO / Lorem ipsum / 表情
- [ ] 没有红绿对比配色；状态图三态用 `cMain / cWarn / cOK`
- [ ] 单页正文 ≤ 6 行；代码块 ≤ 18 行
- [ ] 数字必须可追溯：每个数旁边必须 `\srcnote{...}` 标 report §X 或 file:line

### 7.2 内容正确性红线

- [ ] Lua 脚本能直接 `redis-cli EVAL` 跑通（不留语法错）
- [ ] Go 代码片段与 `main` 分支当前实现一致；截断处必须 `// ...`
- [ ] 时序图实线 = 同步、虚线（`arrow-async`）= 异步
- [ ] 状态机必须覆盖所有合法边（Deck 1 必含 processing→init 回滚边；Deck 5 必含 HalfOpen→Open）

### 7.3 编译/字体冒烟

- [ ] `bash docs/slides/build.sh` 退出码为 0（两遍 xelatex 都成功）
- [ ] 5 个 PDF 全部生成于 `docs/slides/out/`，文件大小 > 200KB
- [ ] 用 `pdffonts out/01-idempotency.pdf` 检查中文字体被嵌入
- [ ] 抽看 cover / 一张 TikZ 图 / 一段代码 / Q&A 末页：字符无方块、对齐无错位

### 7.4 编译命令最终版

```bash
cd /Users/capsfly/Desktop/gin-mall/docs/slides
bash build.sh                    # 全量编译 5 份
xelatex -output-directory=out 01-idempotency.tex  # 单独编一份
```

---

## 8. 留给 Stage 3 的注意事项（速读版）

1. **preamble 不允许改**：§2 的 preamble 是 5 份 deck 的公共契约。Stage 3 若发现需要新增宏包，先记到本文件 §1.2 表格，再更新 preamble，避免不同 deck 用不同包。
2. **TikZ 图先粗后细**：用 §4 的 6 个模板，每张图 10 分钟出第一版；不要追求像素级对齐，让讲义 PDF 跑通是第一目标。
3. **代码块行号必须真实对齐**：`firstnumber=N` 的 N 是仓库当前 `main` 的行号；不要拍脑袋写。每次截代码先 `grep -n` 定位。
4. **数字必须从 REPORT 拷贝**：不要"约 50K RPS"，要"50,319 RPS"（report §"链路压测结果" 表第 4 行）。
5. **Deck 4 的 bug 复盘是亮点**：单独留 1 页讲 `util.LogrusObj` 那个事故，比纯讲架构更有记忆点。
6. **末 2 页固定模板**：Q&A 页用一致版式（5 问 → 一句话答 → 一个反追问），代码位置页用 `description` 列表对齐 file:line。
7. **Metropolis fallback**：若编译时 Metropolis 在某台机器报错（与 ctex 互动 bug），切 `\usetheme{Madrid}` + 注释 `\metroset` 这一行即可。

---

## 附录 · 来源映射速查

### A.1 压测数字 → REPORT 节

| 用在 Deck | 数字 | 出处 |
|-----------|------|------|
| 1 | 755,033 请求 / DB 1 笔订单 / p95 2.33ms | REPORT §4 + 链路表第 4 行 |
| 1 | 50,319 RPS 幂等下单 | REPORT 链路表第 4 行 |
| 2 | Redis Lua 51,362 RPS / max 136ms | REPORT 链路表第 5 行 + §6 |
| 2 | DB 锁 50,142 RPS / max 453ms | REPORT 链路表第 6 行 + §6 |
| 2 | 500 goroutine 抢 100 张零超发 | REPORT §"单元测试" `TestCoupon_AtomicClaimNoOversell` |
| 3 | 缓存命中 p95 3ms vs 无缓存 50ms+ | REPORT §1 + blog 结尾 |
| 3 | 6M order / 1M product 数据规模 | REPORT 标题 §"数据规模" |
| 4 | 500 抢 100 件库存零超卖 | REPORT §"单元测试" `TestInventory_NoOversellUnderConcurrency`（在 inventory_test.go） |
| 4 | LogrusObj 事故 | REPORT §"已修：util.LogrusObj 在 InitLog 前被使用" |
| 5 | 781,624 限流 / 46 通过 / 期望 45 | REPORT §5 + 链路表第 7 行 |
| 5 | 64,254 RPS ping 基线 | REPORT 链路表第 1 行 |

### A.2 代码位置 → file:line

| 用在 Deck | 位置 |
|-----------|------|
| 1 | `middleware/idempotency.go:15-112`、`repository/cache/idempotency.go:30-90` |
| 2 | `repository/cache/coupon.go:13-81`、`repository/db/dao/coupon.go ClaimWithDBLock`、`service/coupon.go (mode 切换)`、`repository/cache/coupon_test.go` |
| 3 | `service/product.go:38-100,267-293`、`repository/cache/product.go:19-74` |
| 4 | `repository/cache/inventory.go:16-138`、`repository/db/model/outbox.go:10-37`、`repository/db/dao/outbox.go:13-78`、`service/outbox/publisher.go:21-95`、`service/order.go:40-100`、`service/order_cancel.go:19-60` |
| 5 | `middleware/ratelimit.go:22-100`、`middleware/circuitbreaker.go:17-133`、`repository/cache/ratelimit.go:17-57` |
