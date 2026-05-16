# 链下监听链上事件：把以太坊托管合约接进 gomall

## 场景：链上付款，怎么传回业务系统？

gomall 计划支持 Web3 支付：用户把 USDC 转到 escrow（托管）合约，合约对账后发一条 `PaymentConfirmed(bytes32 orderID, address buyer, uint256 amount)` event。我们的业务系统要据此推进订单状态、给运营出报表、给买家发通知。

问题来了：**链上 event 不会主动 push 到 Web2 应用**。链是被动的——你不监听，它就当你不存在。

社区常见的三条路：

| 方案 | 一句话 | 痛点 |
| --- | --- | --- |
| 用户付完款手动调一个 webhook | 让前端在 tx 确认后 POST 一下 | 前端可被劫持，金额可伪造 |
| 第三方 indexer（The Graph / Alchemy webhook） | 接现成 SaaS | 强外部依赖，节点不可控，有费用 |
| 自己跑 listener，订阅 RPC 节点 | 后端常驻进程订阅 logs | 要自己处理重连、reorg、catch-up |

gomall 选第三条：**链是真理之源，能自己跑就别外包**。

## 难点一：连接断了怎么办

`ethclient.Subscribe` 走 WebSocket，长连接会因为 RPC 节点重启 / 网络抖动 / nginx 心跳超时断掉。生产里要做的：

1. 把订阅放在一个 for-loop 里，断开就重连
2. 重连别马上重连——指数退避（1s → 2s → 4s ... 上限 1min），避免节点没起来时打爆它
3. 重连后**先 catch-up 历史，再开新订阅**，否则掉线那段时间的事件会永远丢

`service/web3/listener.go` 里的 `run` 就是这个壳子：

```go
for {
    if err := l.connectAndServe(ctx); err != nil { ... }
    select {
    case <-ctx.Done():
        return
    case <-time.After(l.backoff.next()):
    }
}
```

`connectAndServe` 每次：

```go
client = ethclient.Dial(...)
head   = client.BlockNumber()
last   = redis.Get("web3:listener:last_block")
if last < head { client.FilterLogs(from=last+1, to=head) }   // catch-up
sub    = client.SubscribeFilterLogs(from=head+1)             // live
for log := range logsCh { handleLog(log); save(log.Block) }
```

## 难点二：last_block 持久化

如果重启 listener 时不知道"我上次处理到哪一块"，就只能选一个策略：

- 从 genesis 拉 —— 链上几百万块，几小时；显然不行
- 从当前 head 拉 —— 简单，但**进程停机期间的事件全丢**

正确做法：**每处理完一条 log 就把它的 block 号写进 Redis**。重启的时候读出来，从 `last+1` 开始 FilterLogs，把空窗期补上。

```go
func (l *listener) saveLastBlock(ctx, block uint64) {
    rdb.Set(ctx, "web3:listener:last_block", block, 0)
}
```

写 Redis 失败也只是 log warn——下次成功保存时会自然覆盖，丢一点点进度比阻塞主流程值得。

## 难点三：reorg 和 at-least-once

以太坊不是马上 final 的。一笔 tx 进了 block 100，但如果链发生 reorg（短分叉），block 100 可能被"撤销"，原本属于它的 logs 会带 `Removed = true` 再推一次。

光靠 `Removed` 字段还不够。常见场景：

1. listener 处理了 log → 写完 outbox → save last_block 之前进程崩了 → 重启 catch-up 又把同一条拉回来
2. 节点切换主线后，同一 tx hash 出现在另一个 block，但 logIndex 没变

要兜底就得**幂等**。我们用 `(tx_hash, log_index)` 作 dedupe key，写 Redis：

```go
key := fmt.Sprintf("web3:event:%s:%d", lg.TxHash.Hex(), lg.Index)
if !rdb.SetNX(ctx, key, "1", 72*time.Hour) {
    return // 重复事件，已经处理过
}
outbox.Insert("web3.payment.confirmed", payload)
```

72 小时 TTL 是个工程权衡：足够覆盖任何合理的 reorg + 节点切换 + 进程重启场景，又不会让 Redis 永久堆积。

注意 SetNX 和 outbox.Insert 之间**不是原子**的。理论上 listener 在两者之间崩溃，下次重启会跳过这条事件（dedupe key 已写但 outbox 没写）。两种工程化的处理：

- 接受这个边界 case，因为概率极低，且配套有链下对账作兜底
- 把 dedupe key 写进 outbox 表的 unique index，让 DB 层挡重复

gomall 这阶段采用前者——Outbox 表本身的 idempotency 在消费侧再做一层。

## 难点四：解码 event 不依赖具体合约 binding

通常 web3 项目会用 `abigen` 把 Solidity 合约编译成 Go binding，但这意味着合约 ABI 改了、Go 包要重新生成、整个仓库要再 build 一遍。

监听器只关心一个 event 的形状，不关心合约里其他方法。所以直接 inline 一段 ABI JSON：

```go
const paymentConfirmedABI = `[{
  "anonymous": false,
  "inputs": [
    {"indexed": true,  "name": "orderID", "type": "bytes32"},
    {"indexed": false, "name": "buyer",   "type": "address"},
    {"indexed": false, "name": "amount",  "type": "uint256"}
  ],
  "name": "PaymentConfirmed",
  "type": "event"
}]`
parsed, _ := abi.JSON(strings.NewReader(paymentConfirmedABI))
values, _ := parsed.Unpack("PaymentConfirmed", lg.Data)  // [buyer, amount]
orderID := lg.Topics[1]                                  // indexed 参数走 topics
```

合约升级时只需要核对一下 event 签名没变，listener 就还能跑。

## 写到哪里去：Outbox

监听到事件不能直接更新订单表——`service/web3/listener.go` 是个独立 goroutine，跨服务的副作用应该走事件总线。所以 listener 只做一件事：**把链上事件复制成一条 outbox 行**。

```go
outbox.Insert(
    "web3_payment",
    "PaymentConfirmed",
    "web3.payment.confirmed",
    0,
    PaymentConfirmed{OrderID, Buyer, Amount, TxHash, LogIndex, BlockNumber},
)
```

然后由现有的 publisher（`service/outbox`）异步投到 RabbitMQ。下游订阅 `web3.payment.confirmed` 的服务可以是：

- 订单服务：根据 orderID 把状态推进到 paid
- 风控服务：累计买家地址的链上行为
- 财务服务：导出对账

**listener 不知道也不关心下游有谁**——典型事件驱动。

## 环境变量与降级

```
WEB3_RPC_URL=wss://eth-sepolia.example/ws/v3/...
WEB3_ESCROW_ADDR=0xabc...
```

两个都没设置时，`initialize.InitWeb3Listener` 静默不启动。这点和 `tryInitES` 一致：业务团队本地开发不需要起一个以太坊节点，也不影响主链路。

## 完整链路

```
[escrow.sol on-chain]
    PaymentConfirmed(orderID, buyer, amount)
                │
                │  WSS subscribe + FilterLogs catch-up
                ▼
[gomall listener goroutine]
    ├─ dedupe(tx, logIdx) via Redis SetNX
    ├─ outbox.Insert("web3.payment.confirmed", payload)
    └─ Redis.Set(last_block = log.Block)
                │
                │  outbox publisher 异步 fetch & publish
                ▼
[RabbitMQ domain.events]
    routing_key = web3.payment.confirmed
                │
                ▼
[订单服务 / 风控 / 财务]   各自订阅，独立处理
```

## 测试

`service/web3/listener_test.go`：

- `TestStartPaymentListener_NoEnvSkips`：两个 env 都空时 return nil，主进程能起来
- `TestStartPaymentListener_InvalidAddress`：地址不是 hex 时拒绝启动
- `TestStartPaymentListener_CatchUpAndSubscribe`：catch-up 一条 + live 一条 + 同事件再投一次（被 dedupe 吃掉）
- `TestStartPaymentListener_ReorgRemovedSkipped`：`Removed = true` 的 log 不写 outbox

mock 走 interface（`ethClient`、`outboxWriter`、`redisCmd`），不依赖真实 RPC 节点 / Redis / MySQL，跑 100ms 内完成。

## 后续

- listener 单实例跑。多副本要加 leader election（K8s lease 或者 Redis SETNX），不然每条事件会被处理 N 次（dedupe 兜得住但 DB 写多了浪费）
- 单独跑成独立进程，从 web 服务里拆出来，方便横向扩 web 时不带 listener
- 加 metrics：last_block 滞后 head 多少、SetNX 命中率、outbox 写入耗时
