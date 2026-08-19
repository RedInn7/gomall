# 代码实验使用说明

这组实验不需要启动 MySQL、Redis、RabbitMQ，也不需要先把 gomall 整个项目跑起来。每道题都把一个业务问题缩成了独立的 Go package，补完代码后直接运行单元测试即可。

## 目录怎么看

每道题都有两个目录：

```text
problem/     学生作答目录
solution/    参考答案
```

作答时只修改 `problem/` 里的 `.go` 文件。不要修改：

- `*_test.go`；
- 函数名、参数和返回值；
- 已经给出的类型、错误变量和 build tag；
- `solution/` 里的任何文件。

每道题的 `problem/readme.md` 会说明业务背景和完成条件。先读题，再打开同目录下的 `.go` 文件，找到 `TODO`：

```bash
rg -n "TODO" exercises/00-overview exercises/01-user-auth exercises/02-payment-up exercises/03-payment-down exercises/04-payment-clearing
```

如果编辑器支持全局搜索，直接搜索 `TODO` 也可以。

## 怎么做一道题

以 `00.01-authoritative-order` 为例。

先运行一次测试，确认能看到尚未实现造成的失败：

```bash
go test -tags exercise ./exercises/00-overview/00.01-authoritative-order/problem
```

然后打开：

```text
exercises/00-overview/00.01-authoritative-order/problem/order.go
```

只补 `BuildOrder` 中的 `TODO`。写完后重复运行同一条测试命令。看到下面这种结果才算通过：

```text
ok  github.com/RedInn7/gomall/exercises/...
```

如果看到 `FAIL`，先看最上面的失败用例和 `want`：

```text
order = {...}, want {...}
```

左边是代码实际返回的结果，右边是测试要求的结果。不要为了某一组输入写死答案，要把 readme 中描述的业务规则实现完整。

## 第一讲：业务总览

### 00.01 服务端权威数据

文件：

```text
exercises/00-overview/00.01-authoritative-order/problem/order.go
```

需要补：

```go
func BuildOrder(...) (Order, error)
```

测试命令：

```bash
go test -tags exercise ./exercises/00-overview/00.01-authoritative-order/problem
```

完成标准：

- 用户身份取鉴权结果，不信请求里的 `UserID`；
- 价格和商家取服务端商品数据；
- 数量非法、商品不匹配、地址越权时返回对应错误。

### 00.02 两桶库存

文件：

```text
exercises/00-overview/00.02-inventory-buckets/problem/inventory.go
```

需要补：

```go
func (i *Inventory) Reserve(qty int) error
func (i *Inventory) Commit(qty int) error
func (i *Inventory) Release(qty int) error
```

测试命令：

```bash
go test -tags exercise ./exercises/00-overview/00.02-inventory-buckets/problem
```

完成标准：

- `Reserve` 把可售库存移到预留；
- `Commit` 把预留库存变成已售；
- `Release` 把预留库存退回可售；
- 失败操作不能改动任何一个库存桶。

### 00.03 Transactional Outbox

文件：

```text
exercises/00-overview/00.03-transactional-outbox/problem/outbox.go
```

需要补：

```go
func CreateOrder(db *DB, order Order, eventID string) error
```

测试命令：

```bash
go test -tags exercise ./exercises/00-overview/00.03-transactional-outbox/problem
```

完成标准：

- 订单与 `order.created` 事件写在同一个事务回调里；
- Outbox 写入失败时返回原始错误；
- 事务失败后不能留下只有订单、没有事件的半状态。

## 第二讲：用户鉴权

### 01.01 双 Token 续期

文件：

```text
exercises/01-user-auth/01.01-dual-token/problem/token.go
```

需要补：

```go
func NextAction(...) Action
```

测试命令：

```bash
go test -tags exercise ./exercises/01-user-auth/01.01-dual-token/problem
```

完成标准：

- access 有效时返回 `PASS`；
- access 过期但 refresh 有效时返回 `REFRESH`；
- refresh 也过期时返回 `RELOGIN`；
- 当前时间等于到期时间时，token 已经过期。

### 01.02 RBAC 角色缓存

文件：

```text
exercises/01-user-auth/01.02-rbac-cache/problem/cache.go
```

需要补：

```go
func (c *RoleCache) Lookup(...) (string, error)
func (c *RoleCache) Invalidate(userID uint)
```

测试命令：

```bash
go test -tags exercise ./exercises/01-user-auth/01.02-rbac-cache/problem
```

完成标准：

- TTL 内直接返回缓存角色；
- 缓存到期后重新查询；
- 查询失败不能缓存空结果；
- 显式失效后，下一次查询必须立即回源。

### 01.03 Token Version 强制失效

文件：

```text
exercises/01-user-auth/01.03-token-version/problem/auth.go
```

需要补：

```go
func Authorize(...) error
```

测试命令：

```bash
go test -tags exercise ./exercises/01-user-auth/01.03-token-version/problem
```

完成标准：

- 拒绝签名错误和已过期 token；
- claims 中的用户必须与当前用户一致；
- 旧 token version 必须被拒绝；
- 权限判断使用用户当前角色，不信 token 里的旧角色。

## 第五讲：支付清算

### 04.01 清算复式记账

文件：

```text
exercises/04-payment-clearing/04.01-clearing-ledger/problem/clearing.go
```

需要补：

```go
func RecordClearedTx(...) error
```

测试命令：

```bash
go test -tags exercise ./exercises/04-payment-clearing/04.01-clearing-ledger/problem
```

完成标准：

- Wallet 借记买家钱包，Stripe/Web3 借记外部清算账户；
- 三种渠道统一贷记卖家托管账户，并保证借贷平衡；
- 正确处理普通价、促销最终价、币种和外部凭证标准化；
- 非法输入、重复订单和托管入账失败时不留下半成品；
- 通过题目列出的 16 个公开测试场景。

## 一次运行全部题目

完成单题后，可以一次运行全部学生测试：

```bash
./scripts/grade-exercises.sh student
```

所有 package 都显示 `ok` 才算完成。

如果想确认 Go 文件能否编译，但暂时不运行测试：

```bash
./scripts/grade-exercises.sh compile
```

## 提交前检查

- 当前要求提交的 `problem` package 全部测试通过；
- 没有修改任何 `*_test.go`；
- 没有删除 `//go:build exercise`；
- 没有从 `solution/` 直接复制整份文件；
- 每个错误分支都返回题目给出的错误变量；
- 代码已经执行 `gofmt`。

格式化学生代码：

```bash
gofmt -w exercises/*/*/problem/*.go
```

公开测试只负责给出基本反馈。提交后还会运行额外测试，检查负数、到期边界、失败回滚、缓存隔离等情况。

## 第三讲：支付（上）

### 02.01 固定账户加锁顺序

```bash
go test -tags exercise ./exercises/02-payment-up/02.01-ordered-account-locks/problem
```

### 02.02 复式资金流水

```bash
go test -tags exercise ./exercises/02-payment-up/02.02-double-entry-ledger/problem
```

### 02.03 原子完成支付

```bash
go test -tags exercise ./exercises/02-payment-up/02.03-payment-finalize/problem
```

## 第四讲：支付（下）

### 03.01 支付票根状态机

```bash
go test -tags exercise ./exercises/03-payment-down/03.01-idempotency-state-machine/problem
```

### 03.02 先回放，再检查熔断

```bash
go test -tags exercise ./exercises/03-payment-down/03.02-replay-before-breaker/problem
```

### 03.03 支付对账扫描

```bash
go test -tags exercise ./exercises/03-payment-down/03.03-payment-reconciliation/problem
```
