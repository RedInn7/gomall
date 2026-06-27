要多从业务背景触发，多画交互流程图，画流程图时候应该使用 pgf-umlsd 绘制标准时序交互图（首选）或者使用纯 TikZ 绘制自由拓扑流转（适合复杂网络交互）

这个项目是教学项目，应该多讲业务背景，代码也要讲，要从业务出发去讲技术，每次搞完一个slide 都应该另外起一个agent 去读看看深入如何，容易理解性如何

不要有太多的数学

这个项目的slide 至少得50页吧

## Agent skills

（由 `/setup-matt-pocock-skills` 生成；配置 Matt Pocock 工程类 skill 在本仓库的运行方式。）

### Issue tracker

issue 存放在 GitHub Issues（仓库 RedInn7/gomall），`to-issues`/`triage`/`to-prd` 用 `gh` CLI 读写；外部 PR **不**纳入 triage 队列。详见 `docs/agents/issue-tracker.md`。

### Triage labels

triage 用 5 个默认标签：`needs-triage` / `needs-info` / `ready-for-agent` / `ready-for-human` / `wontfix`。详见 `docs/agents/triage-labels.md`。

### Domain docs

单 context 布局：根目录一份 `CONTEXT.md` + `docs/adr/`（按需由 `/domain-modeling` 惰性创建）。详见 `docs/agents/domain.md`。