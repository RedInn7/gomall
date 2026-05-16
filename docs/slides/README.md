# gomall 业务 Deck 索引

围绕 gomall 真实代码与压测数据产出的中文 Beamer Deck。每份 deck 都给出
代码 `file:line` 引用 + 来自 `stressTest/REPORT.md` 的真实压测数字。

技术细节型 deck（01-05）与业务侧 deck（10-15）分两批：

- **01-05**：幂等、防超发、缓存一致性、Outbox+Saga、限流熔断（合订本 `master.pdf`）
- **10-15**：流量治理、库存、搜索、列表性能、鉴权、关单（独立 PR，逐份合入）

每份 deck 的源 `.tex` 文件与编译产物 `.pdf` 同目录。详细的编译流程、文件结构、
设计原则见各分支 README 与 `docs/slides-pipeline/` 下的需求 / 方案 / 验收文档。

## 下一代 feature 路线图

5 份业务侧 deck（11-15）讲完了 gomall 现状。下一步的"高并发 + 现代化"扩展见
`docs/architecture/feature-matrix.md`：

- 动静分离 / HTTP cache（PR `feat/http-cache`）
- 削峰填谷 / 下单异步化（PR `feat/order-async-mq`）
- Web3 Escrow + 钱包付款（PR `feat/web3-*`）
- 向量搜索 + 语义匹配（PR `feat/milvus-vector` + `feat/semantic-search`）

每个 feature 都配套独立 blog（`docs/blog/06-*` 起）。

---

## 下一代 feature 路线图

5 份业务侧 deck（11-15）讲完了 gomall 现状。下一步的"高并发 + 现代化"扩展见
`docs/architecture/feature-matrix.md`：

- 动静分离 / HTTP cache（PR `feat/http-cache`）
- 削峰填谷 / 下单异步化（PR `feat/order-async-mq`）
- Web3 Escrow + 钱包付款（PR `feat/web3-*`）
- 向量搜索 + 语义匹配（PR `feat/milvus-vector` + `feat/semantic-search`）

每个 feature 都配套独立 blog（`docs/blog/06-*` 起）。
