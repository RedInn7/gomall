# Escrow 合约

## 职责

链上资金托管合约。三方角色：

- **buyer**：买家，下单时把货款打到合约
- **seller**：卖家，确认发货后由 buyer/arbiter 触发释放
- **arbiter**：仲裁人，buyer / seller 失联或撕逼时介入

合约只做一件事：**持有资金，按状态机规则放钱**。其它一切（订单详情、物流、对账）放在链下，链上只 emit 必要的事件让后端跟得上。

## 状态机

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

不变式：

- `Created → Funded` 只能发生一次（`inState(Created)`）
- `Funded → Released | Refunded` 终态，不可逆
- `Disputed` 是 `Funded` 的分叉，最终仍走 `Released` 或 `Refunded`
- 所有 immutable 参数（buyer/seller/arbiter/amount）在 constructor 后无法变更

## 编译

```bash
# 单合约编译，产物落到 build/
solc --abi --bin --optimize --overwrite contracts/Escrow.sol -o build/
```

产物：

- `build/Escrow.abi`：ABI JSON
- `build/Escrow.bin`：部署字节码

## 重新生成 Go binding

```bash
abigen --abi build/Escrow.abi --bin build/Escrow.bin --pkg escrow \
       --type Escrow --out pkg/web3/escrow/escrow.go
```

当前仓库里的 `pkg/web3/escrow/escrow.go` 是按 abigen 输出模板手写的占位实现，便于在未安装 go-ethereum 的环境下保持 `go build ./...` 通过；真正集成上链时按上面命令重新生成覆盖。

## 事件

### `Funded(uint256 amount)`

买家付款成功后触发。后端可以靠这个 event 校验金额。

### `PaymentConfirmed(bytes32 indexed orderID, address indexed buyer, uint256 amount)`

`fundWithOrderID(orderID)` 路径专属。`orderID` 是链下订单唯一标识（一般是 snowflake / UUID 的 hash），后端 EVM listener 订阅这个 event 然后做：

```
on PaymentConfirmed(orderID, buyer, amount):
    1. 查订单 by orderID
    2. 校验 buyer 钱包地址 + amount 与订单一致
    3. 状态推进：order.status = Paid
    4. 触发 outbox: order.paid（库存 commit、清缓存等下游）
```

`orderID` 作为 indexed 字段方便按订单号过滤日志。

### `Released(address indexed to)` / `Refunded(address indexed to)`

资金最终流向。可以用来对账：链上事件 vs 链下订单终态。

### `Disputed(address indexed by)`

进入仲裁。运营后台监听这个事件触发人工介入工单。

## 安全注意

- `amount` immutable：一旦部署，不能改价。变价需要换合约。
- 合约用 custom errors（gas 友好）而不是字符串 require。
- `release/refund` 用 `call{value:}` 而不是 `transfer`，避免接收方是合约且 fallback 用了 >2300 gas 时 revert。
- 没有 reentrancy 守卫：状态机在转账前已经迁移到终态（Released/Refunded），重入会直接撞 `inState` check 退出。

## 部署参数

constructor:

```
Escrow(buyerAddr, sellerAddr, arbiterAddr, amountWei)
```

- `arbiterAddr` 推荐用多签（Gnosis Safe），不要单一 EOA。生产环境也可以是 DAO 投票合约。
- `amountWei` 是订单总价的 wei 表示（1 ETH = 10^18 wei）。
