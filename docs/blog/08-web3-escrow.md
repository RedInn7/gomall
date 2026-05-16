# 把"信任"卸到链上 —— Web3 Escrow 合约与 gomall 的链下集成

## 故事开始

gomall 跑到现在所有的资金流都假设两件事：

1. **支付网关是可信的**（微信/支付宝告诉你"已支付"就真的收到钱了）
2. **买家不会赖账，卖家不会跑路**（出问题走平台客诉）

线下电商场景这两条都成立。但只要稍微往外探一步——比如：

- 跨境数字商品交易，买方在 A 国卖方在 B 国，没有共同的支付清算
- 二手实物 NFT / 实体凭证，卖家发货后买家就消失了
- 没有平台兜底的 P2P 交易，比如直接卖一段游戏帐号

**双方都不信对方**。这时候没有任何一个中心化角色能拍板"钱归谁"。

## 银行托管的链上版本

线下的解法很老：**找一个共同信任的第三方托管资金**。买家把钱先打给第三方，卖家发货，买家确认后第三方放款给卖家。出问题第三方仲裁。

银行的 escrow account、淘宝的支付宝、Stripe 的 hold and capture，都是这个模式。

链上版本只是把"第三方"从一家公司换成**一段公开可审计的代码**：

- 合约地址持有钱（不是任何人的钱包）
- 合约逻辑写死状态机：什么条件下放给谁、什么条件下退给谁
- 合约部署后参数不可改（buyer / seller / arbiter / amount 都是 `immutable`）

**所有人都能在链上验证"这笔钱当前归属于哪个状态"**。这就是 Web3 escrow。

## 状态机：三个稳态 + 一个分叉

`contracts/Escrow.sol` 的核心是这个状态机：

```
                       fund / fundWithOrderID
        Created  ───────────────────────────────▶  Funded
                                                    │
                                                    │ dispute (buyer or seller)
                                                    ▼
                                                 Disputed
                                                    │
                      ┌─────────────────────────────┼─────────────────────────────┐
                      │ release                     │                             │ refund
                      │ (buyer / arbiter)           │                             │ (seller / arbiter)
                      ▼                             │                             ▼
                  Released ◀──────────release───── Funded ──────refund────▶   Refunded
                  (seller 收钱)                                                 (buyer 拿回)
```

三个**终态**（不可逆）：

- `Released`：交易成功，卖家拿到钱
- `Refunded`：交易失败，买家拿回钱
- 没有第三种终态。所有 dispute 最终都要回到这两条路之一。

两个**过渡态**：

- `Created`：合约部署完但没付款，没钱
- `Funded`：买家已付款，钱在合约里冻结，等待 release / refund / dispute

一个**分叉态**：

- `Disputed`：从 `Funded` 分出来，唯一能转出去的方向是 `Released` 或 `Refunded`（由 arbiter 裁定）

### 谁能触发什么

| 动作 | buyer | seller | arbiter |
|------|-------|--------|---------|
| `fund` / `fundWithOrderID` | yes | no | no |
| `release` (放款给卖家) | yes | no | yes |
| `refund` (退款给买家) | no | yes | yes |
| `dispute` (升级仲裁) | yes | yes | no |

设计上的不对称：

- buyer 不能主动 refund（否则付完款立刻 refund，卖家白搭一次发货）
- seller 不能主动 release（否则没必要做托管了，钱本来就该卖家说了算）
- arbiter 不能主动 dispute（仲裁人介入需要冲突先发生）
- arbiter 兼具 release + refund 权限是因为它存在的意义就是裁决冲突

### 状态机的不变式

合约级硬约束（编译期 + 运行时双重保证）：

1. `Created → Funded` 转移只能发生一次（`inState(Created)` modifier）
2. `Released` / `Refunded` 是吸收态：进入后任何函数调用都会撞 `inState` 或者 `state != Funded` 立即 revert
3. 金额必须精确匹配 `amount`（多 1 wei 或少 1 wei 都 revert，避免 dust 攻击）
4. 释放路径用 `call{value:}("")` 而不是 `transfer`，避免接收方是合约且 fallback 用了 >2300 gas 时永久卡死

## 关键 event：`PaymentConfirmed`

```solidity
event PaymentConfirmed(bytes32 indexed orderID, address indexed buyer, uint256 amount);
```

`buyer` 调 `fundWithOrderID(orderID)` 时同时 emit 这个事件。两个 `indexed` 字段意味着后端可以按 orderID 或者 buyer 钱包地址过滤日志。

这是**链下集成的核心 hook**。整个流程：

```
[前端]
    用户在订单详情页点 "用 ETH 付款"
    ├─ MetaMask 弹窗，发起 tx：escrow.fundWithOrderID(orderID, { value: amount })
    └─ 用户签名，tx 上链

[链上]
    Escrow 合约
    ├─ require msg.sender == buyer (合约部署时锁死的钱包)
    ├─ require msg.value == amount (锁死的价格)
    ├─ state = Funded
    └─ emit PaymentConfirmed(orderID, buyer, amount)

[gomall 后端 EVM listener]
    订阅 PaymentConfirmed
    收到事件 → 校验：
    ├─ orderID 存在且属于 buyer 关联的用户
    ├─ amount 与订单总价一致
    └─ tx confirmations ≥ N (防回滚)
    通过 → outbox.Insert("order.paid", ...)
                └─ 下游：库存 commit、清缓存、发货推送
```

**注意三个细节**：

1. **listener 不主动调合约**，它只订阅事件。调合约的是用户的钱包。这就避免了后端持有 buyer 私钥这种灾难。
2. **outbox 接住事件**之后，剩下的链路（commit 库存 / 通知 ES）完全复用前面文章讲的那套 Outbox + Saga。链上事件只是 Outbox 的又一个**入口**。
3. **confirmations** 必须等。EVM 在 6-12 块之前都可能回滚（reorg）。listener 收到事件不要立刻当真，至少等满足 finality 阈值再写订单状态。

## 资金风险：怎么把"代码即法律"做得不那么吓人

链上托管最大的争议：**合约一旦部署，参数 immutable 写死**。改不动。

这有两层风险：

### 风险 1：合约有 bug

整个仓库 200 行 Solidity，但一个 reentrancy / 整数溢出就足以掏空合约持有的所有 USD。怎么防：

- **状态机迁移发生在转账之前**：`state = Released` 在 `_safeSend(seller, amount)` 之前一行。任何重入回 release 会撞 `state != Funded` 立即 revert
- **不接受非 amount 金额**：`if (msg.value != amount) revert WrongAmount(...)`，避免 dust 累积
- **没有 receive() / fallback()**：直接给合约地址打钱会失败，杜绝资金路径之外的入账
- **审计 + 单测**：见 `contracts/Escrow.test.txt`，每一条状态转移都有正反两条 case
- **正式上线前过 OpenZeppelin Defender 或 CertiK 一类的第三方审计**

### 风险 2：arbiter 跑路

arbiter 同时有 release 和 refund 权限。如果 arbiter 私钥被盗，所有 Funded 状态的合约都能被洗。怎么防：

- **arbiter 用多签**（Gnosis Safe），单一私钥泄漏不致命，必须 N-of-M 签名才能动钱
- **arbiter 是合约地址**而不是 EOA。合约的话可以加治理逻辑（社区投票、白名单时间锁等等）
- **极端情况上 DAO**：arbiter 是一个治理合约，触发 release/refund 需要 DAO 提案通过。代价是仲裁慢，适合大额纠纷。

**实际工程里 arbiter 多签 + 运营介入 + 7 天异议期，是最常见的折中**。

## 链上做什么，链下做什么

这个边界划清楚太重要了。原则：

> **链上只做一件事：资金托管 + 必要事件发射。其它一切搬到链下。**

为什么？因为链上每一次写操作都要 gas，每一行 storage 都是钱。

| 责任 | 链上 | 链下 |
|------|------|------|
| 钱在哪 | yes (合约持有) | no |
| 谁能动钱 | yes (合约 modifier) | no |
| 订单详情（商品、数量、地址、物流） | no | yes (MySQL) |
| 订单状态推进 | 只推 escrow 自己的 5 个状态 | 全套订单生命周期（待付/已付/已发/已签/已评） |
| 仲裁工单流 | no | yes (运营后台) |
| 用户认证、风控、推荐 | no | yes |
| 事件源 | yes (emit) | listener 把链上事件转成 outbox 事件 |

gomall 现有的订单/库存/支付/搜索完全不需要改，**escrow 只是又接了一种支付方式**，事件最终都汇聚到 outbox。

## 链下集成代码骨架

`pkg/web3/escrow/` 当前是占位实现（避免一个 100MB 的 go-ethereum 依赖进业务模块）。真正上链时按 `contracts/README.md` 跑：

```bash
solc --abi --bin --optimize contracts/Escrow.sol -o build/
abigen --abi build/Escrow.abi --bin build/Escrow.bin \
       --pkg escrow --type Escrow --out pkg/web3/escrow/escrow.gen.go
```

然后 listener 大致这样：

```go
// service/web3/escrow_listener.go
func (l *EscrowListener) Run(ctx context.Context) error {
    sink := make(chan *escrow.PaymentConfirmedEvent, 64)
    sub, err := l.contract.WatchPaymentConfirmed(escrow.WatchOpts{
        Start: l.checkpoint.Last(),
    }, sink)
    if err != nil { return err }
    defer sub.Unsubscribe()

    for {
        select {
        case ev := <-sink:
            if err := l.confirmAndDispatch(ctx, ev); err != nil {
                l.log.Errorf("dispatch failed: %v", err)
                continue  // 不要 ack，下次重启会从 checkpoint 重放
            }
            l.checkpoint.Advance(ev.Block)
        case err := <-sub.Err():
            return err
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

func (l *EscrowListener) confirmAndDispatch(ctx context.Context, ev *escrow.PaymentConfirmedEvent) error {
    // 等够 confirmations
    if err := l.waitFinality(ctx, ev.Block, 12); err != nil {
        return err
    }
    // 校验金额 / 买家匹配
    order, err := l.orderDao.GetByExternalID(hex.EncodeToString(ev.OrderID[:]))
    if err != nil { return err }
    if order.AmountWei.Cmp(ev.Amount) != 0 { return ErrAmountMismatch }
    if !bytes.Equal(order.BuyerWallet[:], ev.Buyer[:]) { return ErrBuyerMismatch }
    // 推 outbox（事务内）
    return l.db.Transaction(func(tx *gorm.DB) error {
        if err := l.orderDao.MarkPaid(tx, order.ID, ev.TxHash); err != nil { return err }
        return l.outbox.Insert(tx, "order", "OrderPaid", "order.paid", order.ID, ev.Payload())
    })
}
```

四个关键点：

1. **checkpoint 持久化**：listener 重启不能丢事件。把"上次处理到第几块"存 DB
2. **waitFinality**：少于 N 个确认数不要相信
3. **DB 事务里写订单 + outbox**：跟前面文章讲的双写问题同样的解法
4. **失败不要 ack**：保留链上事件，下次重放是幂等的（订单已经 Paid 就跳过）

## 至少一次 + 幂等

链上事件本质上就是"至少一次"投递：

- 网络抖动 → listener 重连 → 同一事件被取两次
- listener 崩了 → checkpoint 没更新 → 重启重放最近 K 个块的事件
- reorg → 同样一笔 tx 在不同块出现两次（前一次会被回滚但 listener 已经看见）

**所以消费端必须幂等**。和 outbox 消费者一样：

- 订单已经是 Paid 状态？跳过
- tx_hash 已经在订单的 payment_hash 字段？跳过
- 不要靠 listener 自己去重，靠**业务终态本身就有去重语义**

## 验证

- 合约预期行为：`contracts/Escrow.test.txt`，列出 A-G 七组场景共 22 条 case（部署、fund、release、refund、dispute、资金安全、事件）
- Go binding 占位：`pkg/web3/escrow/escrow_test.go` 校验 ABI JSON 合法 + 必要方法事件齐全 + State 枚举一致性 + 事件签名合法
- `go test ./pkg/web3/...` 全绿
- `go build ./...` 0 错（占位实现不引入 go-ethereum，业务模块不受影响）

## 面试角度

- **为什么不直接在链上跑订单整套逻辑？** gas 太贵 + 业务变更不灵活；链上只该做"必须用代码保证的事"
- **arbiter 怎么选？** 单一 EOA / 多签 / DAO 治理合约，按金额和合规要求升级
- **链上 event 怎么保证不丢？** listener 持久化 checkpoint + 至少一次 + 消费端幂等
- **reorg 怎么处理？** 等够 confirmations 才确认事件，未确认的状态用一个 pending 中间态
- **immutable 合约怎么升级？** 不能升级，只能换合约（部署新地址）+ 治理层迁移用户引用；或者从一开始就用 proxy + implementation 模式（OpenZeppelin Transparent / UUPS）

## 代码位置

- 合约：`contracts/Escrow.sol`
- 合约文档：`contracts/README.md`
- 合约预期行为：`contracts/Escrow.test.txt`
- Go binding 占位：`pkg/web3/escrow/escrow.go`
- ABI JSON：`pkg/web3/escrow/escrow.abi.json`
- 单元测试：`pkg/web3/escrow/escrow_test.go`

## 写给自己

链上和链下系统的分工是这类项目最难的一步：**不是"哪些代码上链"，而是"哪些信任假设上链"**。

gomall 主流程的信任假设——"支付网关 + 平台仲裁"——足够撑起 99% 的电商场景。escrow 不是要替代它，而是把"陌生人之间无平台兜底"的那 1% 边缘场景也接入主链路。

写代码时如果你发现自己在链上塞订单详情、把链当 MySQL 用——停下来。链是个非常贵的状态机，它存在的意义是承载**必须无中介的信任**，不是承载业务复杂度。
