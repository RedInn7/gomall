# 给电商加一条 Web3 支付通道：钱包签名 + outbox + 链上 listener

## 为什么不能直接复用 `/paydown`

`/paydown` 是一条同步路径：用户传支付密码 → 后端在一个事务里扣余额、扣库存、转账给商家、推 outbox `order.paid`。

钱包支付完全不是这套模型：

- **后端无权动用户的资金**。私钥在钱包里，签名授权也只是授权一次链上动作，不是授权后端的余额账户。
- **链上转账是异步的**。区块出块、确认数、节点延迟，没人会让 HTTP 请求阻塞着等链上回执。
- **重放的风险面更大**。一段裸签名 `0x...` 是可拷贝的，谁拿到都能往后端贴。

所以这条通道得是**两段式**：

```
①  钱包签一段“支付意图”      ②  钱包真的发起链上转账
    ↓ (HTTP)                    ↓ (RPC)
    后端验签 + pending 占位       链上 emit PaymentConfirmed
                                  ↓
                                 listener 收到事件 → 订单转 paid
```

前端必须分别完成两件事：HTTP 的“我打算付”和链上的“真的付了”。前者是免费的链下签名，后者才花 gas。

## 第一段：链下签名 + 后端验签

### 防钓鱼的 EIP-191

如果服务端要求“请对这段 32 字节 hash 直接签名”，那它和让用户签一笔随便什么 calldata 之间没有区别——钓鱼站点可以把恶意交易伪装成一段无意义的 hash 让你签。EIP-191 的解法是**强制前缀**：

```
keccak256("\x19Ethereum Signed Message:\n" + len(msg) + msg)
```

这个 0x19 前缀让钱包知道这是一段“人读的消息”，会把原文展示给用户确认；同时这个 hash 进入合约校验时也不会与 EIP-712 / 普通 tx hash 互通——天然隔离。

后端这边对应的还原代码：

```go
func VerifyPersonalSign(addr string, msg []byte, sig []byte) (bool, error) {
    prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msg))
    hash := crypto.Keccak256(append([]byte(prefix), msg...))

    // 钱包给的 v 是 27/28，go-ethereum 需要 0/1
    rsv[64] -= 27
    pub, _ := crypto.SigToPub(hash, rsv)
    recovered := crypto.PubkeyToAddress(*pub)
    return bytes.Equal(recovered.Bytes(), common.HexToAddress(addr).Bytes()), nil
}
```

### 消息模板的每个字段都不是装饰

```
gomall:paydown:order={orderID}:nonce={nonce}:chain={chainID}
```

- `gomall:paydown:` 业务域前缀，避免一段签名被串到另一个系统/接口。
- `order={orderID}` 把签名绑死到一笔订单。
- `nonce={nonce}` 一次性随机数，下面单独讲。
- `chain={chainID}` 把签名绑死到一条链：在 mainnet 签的同一段消息不能被搬到 Polygon 重放（因为 chainID 已经在原文里）。

### nonce 防重放

`GET /api/v1/paydown/crypto/nonce?order_id=` 颁发 nonce，写 Redis：

```
web3:nonce:{userID}:{orderID} = <16 字节 hex>   TTL 5min
```

`POST /api/v1/paydown/crypto` 消费 nonce 是关键：**必须原子 GET+DEL**，否则两个并发请求拿到同一份 nonce 都能验签通过。

```lua
local cur = redis.call('GET', KEYS[1])
if cur == false then return -1 end
if cur ~= ARGV[1] then return -2 end
redis.call('DEL', KEYS[1])
return 1
```

走 Lua 比 `GET` + `DEL` 两步要省一个 RTT，更关键的是它们之间没有窗口给攻击者插队。

### 后端写什么，不写什么

签名校验通过后，后端**不动余额，不动库存**——这两件事根本不在它的控制范围内。它只做两件：

1. 同事务 outbox 写 `web3.payment.pending`，body 里带 `orderID / walletAddr / amount / chainID / nonce`。
2. Redis 占位 `paydown:web3:pending:{orderID}`，TTL 30min，方便对账巡检/前端轮询展示。

订单状态**仍然是 UnPaid**。这一点很重要：签了名不等于付了钱。如果在 30min 内链上没确认，超时关单服务照样会把这笔订单关掉、释放库存预占——和普通超时订单走同一个 Saga。

## 第二段：链上确认（接力给 listener）

链上转账由钱包发起，合约 emit `PaymentConfirmed(orderID, payer, amount)`，由 Unit 4 的 listener 订阅：

- listener 验证 `payer == 之前签名的 walletAddr`、`amount == 订单金额`、`orderID` 存在且仍是 UnPaid。
- 任一条件不满足 → 落 dead letter，告警人工核对。
- 全部满足 → 在一个事务里更新订单为 paid、写 outbox `order.paid`、`DEL paydown:web3:pending:{orderID}`。

这样下游消费者（库存提交、消息推送、积分发放）拿到的是和**法币 `/paydown` 完全一致的 `order.paid` 事件**——上层业务不需要为 Web3 分支再实现一遍。

## 为什么这套设计是对的

回过头看，整条链路上后端的“权力”很小：

| 阶段           | 后端在做什么                  | 没有做什么                |
|--------------|-------------------------|----------------------|
| 颁发 nonce    | 写 Redis，5min TTL       | 不暴露私钥、不签名             |
| 验签           | 还原 pubkey 对地址          | 不接收链上转账、不动余额          |
| 写 outbox    | 标记“用户已同意付款”             | 不修改订单状态              |
| listener 确认 | 校验金额，状态机推进              | 不发起链上交易、不持币           |

**核心原则：钱不在后端，签名才是关键**。后端唯一可信的输入是“钱包亲手签出的一段不可伪造的消息”，至于钱有没有真到账，由链上账本说了算，由 listener 异步兜底。

这也是为什么 `web3.payment.pending` 写到 outbox 而不是直接更新订单——这条消息只是“我看到了用户的支付承诺”，不是“支付完成”。两件事之间隔着一整条公链。

## 顺手处理的边角

- **idempotency**：`POST /paydown/crypto` 挂了和 `/paydown` 同一份 `Idempotency()` 中间件，前端重试时不会重复写 outbox。
- **签名格式兼容**：钱包给的 `0x...` 和裸 hex 都接受，统一在 `decodeSignature` 里规整。
- **大小写无关比对**：地址全部走 `strings.ToLower(common.HexToAddress(addr).Hex())` 落库，避免 checksum 写法差异引发对账假阳性。
- **多链隔离**：`chainID` 进 outbox payload，listener 启动多实例时每个实例只订阅自己关心的链，互不干扰。

## 收尾

支付通道这种东西，难点从来不是“能不能跑通”，而是**当出错时谁兜底**。法币支付的兜底是后端事务回滚；Web3 支付的兜底是“链上有没有发生”——后端能做的只是：先把用户的意图签下来留个凭据，再让 listener 拿真实链上事件去推进状态机。

只要这两段没有连成一个同步调用，剩下的就只是补全 listener 和事件回环了。
