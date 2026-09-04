# 普通订单的清算、结算与退款

## 1. 先说业务结论

GoMall 的普通订单资金链是一个真正的两阶段模型：

- **支付确认时做清算**：确认钱已经收到，把钱记入 `merchant_escrow`（商家托管），订单进入待发货；卖家此时看不到这笔可用余额。
- **履约完成后做结算**：买家确认收货或系统自动确认收货后产生 `order.completed`，结算消费者再把钱从 `merchant_escrow` 转进卖家钱包。

这样做的业务意义很直接：买家付款不等于卖家已经履约。钱先由平台托管，发货、收货链路走完后才放款；如果在放款前退款，直接从托管款退给买家，不需要让尚未收到钱的卖家承担扣款。

本文中的术语固定如下：

- **清算（clearing）**：根据支付渠道生成借贷指令，记录这笔钱已经进入商家托管，并创建 `PaymentClearing` 清算单。
- **结算（settlement）**：订单完成后执行放款，把托管款转进卖家钱包并提交。

清算和结算都是实时、按订单发生的业务步骤。

## 2. 资金生命周期

一笔普通订单的 `payment_clearing.status` 只有三个有效值：

| 状态 | 含义 | 允许的下一状态 |
| --- | --- | --- |
| `cleared` | 支付已确认，资金在 `merchant_escrow`，卖家尚未入账 | `settled` 或 `refunded` |
| `settled` | 订单已完成，托管款已放给卖家 | `refunded` |
| `refunded` | 退款已落地，资金生命周期结束 | 无 |

正常履约路径是：

1. 订单处于待支付；
2. Wallet、Stripe、Web3 或 Xcash 确认付款；
3. 清算事务写入 `PaymentClearing(status=cleared)`，资金进入 `merchant_escrow`；
4. 同一事务扣库存、把订单推进到待发货、转移商品归属并写 `order.paid` Outbox；
5. 商家发货，订单进入待收货；
6. 买家确认收货，或 7 天任务自动确认，订单进入已完成并写 `order.completed` Outbox；
7. `clearing.settle` 消费 `order.completed`；
8. 结算事务把 `merchant_escrow` 转给卖家，清算单变为 `settled`。

支付代码里的 `finishPaymentConfirmationTx` 完成“支付确认事务尾段”（库存、待发货、商品归属、`order.paid`），**不是**卖家资金结算；真正的资金结算入口是 `clearing.SettleCompletedOrder`。

## 3. 支付确认阶段：四渠道如何清算

所有渠道都从数据库订单读取买家、卖家、商品、数量和权威金额，不相信客户端价格。统一应付口径是：命中促销（`promo_rule_id != 0`）时取 `final_cents`，否则取 `money * num`。全额优惠导致 `final_cents=0` 也是合法结果。

四条渠道的清算目标相同，区别只在资金来源：

| 渠道 | 清算借方（debit） | 清算贷方（credit） | `PaymentClearing` 渠道信息 |
| --- | --- | --- | --- |
| Wallet | 买家 `user_wallet` | `merchant_escrow` | `channel=wallet`，币种 `CNY`，`provider_ref` 为空 |
| Stripe | `external_clearing` | `merchant_escrow` | `channel=stripe`，记录 Checkout Session ID 和实际币种 |
| Web3 | `external_clearing` | `merchant_escrow` | `channel=web3`，记录链上 tx hash 和代币代码 |
| Xcash | `external_clearing` | `merchant_escrow` | `channel=xcash`，记录 `sys_no:chain:tx_hash`；清算币种为订单计价 CNY |

四个渠道统一调用 `clearing.RecordClearedTx`。该方法创建清算单，并以 `biz_type=order_clear` 写一借一贷两条台账。此时卖家钱包不变。

### 3.1 Wallet

Wallet 支付在本地事务内完成：锁定买家钱包，验证支付密码和余额，扣减 `users.money`，写买家 `debit` 和托管账户 `credit`。买家余额变更、清算单、台账、库存、订单待发货、商品归属和 `order.paid` Outbox 任一步失败，整个事务回滚。

支付确认阶段不再锁卖家，也不修改卖家余额；卖家只会在订单完成后的结算事务中被加行锁和入账。

### 3.2 Stripe

Stripe Checkout Session 创建使用 `checkout-order-{order_id}` 幂等键。Webhook 必须验签，只处理已支付的 `checkout.session.completed`，并校验会话金额、币种、订单和用户。

Stripe 已经在外部收款，所以本地不扣买家钱包。清算分录是 `external_clearing debit / merchant_escrow credit`，`provider_ref` 保存 Checkout Session ID。外部收款和本地数据库事务无法组成一个原子事务；如果 Stripe 已收款而本地清算暂时失败，依靠 webhook 重投恢复。

如果订单已经被另一渠道抢先支付，新的 Stripe Session 仍显示已付款，这不是普通幂等重放。系统会以 `channel + provider_ref` 幂等写入 `payment_anomaly(status=pending_review)`，保留实付金额和币种并告警，等待退款处理；只有 provider_ref 与原清算单完全相同才按重放直接成功。签名有效且状态为 paid、但金额或币种与订单不一致的 Session 也会以 `reason=amount_currency_mismatch` 持久化后返回成功，表示异常已被系统接管，并不表示钱已经退回。

### 3.3 Web3

Web3 先签名授权，再等待 escrow 合约事件达到确认深度：

1. 为用户和订单签发一次性 nonce；
2. 校验 EIP-191 签名并写 `web3.payment.pending` Outbox；
3. Redis pending 保存签名钱包地址；
4. listener 回扫达到确认深度的 `PaymentConfirmed`；
5. 消费者校验链上 buyer 与 pending 地址、链上金额与订单金额；
6. 本地清算写 `external_clearing debit / merchant_escrow credit`，`provider_ref` 保存 tx hash。

链上交易和本地清算同样不是一个原子事务。链上已确认但本地失败时，依靠事件回扫、消息重投和数据库幂等收敛。结算成功后删除 Web3 pending 只是 best-effort，不参与数据库提交。

Web3 也执行相同的重复实收检查：同一 tx hash 是事件重放，不同 tx hash 或跨渠道重复付款会进入 `payment_anomaly`，不能静默当成“订单早已支付”。当前代码先保证异常资金可追踪、不会丢失；自动调用 Stripe Refund API 或链上返款仍未实现，需要运营处理 `pending_review`。

### 3.4 Xcash

Xcash 为订单创建限时托管账单，买家可以从服务端白名单允许的链币组合中选择付款。Gomall 校验 HMAC、时间戳、持久化 nonce，并通过 HTTPS 最终账单复核账单号、链、币、收款地址、实付数量和交易哈希；严格 AML 模式还会等待 Low/Moderate 风险结果。Webhook 丢失时，后台轮转查询等待中、风控中和最近过期的账单，并复用同一条事务结算路径。错链、错币、错地址、错金额、高风险或重复实收都进入 `payment_anomaly`，不推进订单。

## 4. 清算事务的业务结果

清算成功并不只写资金台账。支付确认事务还必须原子完成：

1. 创建每订单唯一的 `payment_clearing`，状态为 `cleared`；
2. 写 `order_clear` 的一借一贷两条流水；
3. 条件扣减数据库权威库存，避免超卖；
4. 使用状态守卫把订单从待支付推进到待发货；
5. 为买家创建一份下架的商品归属记录；
6. 写 `order.paid` Outbox。

这些数据库写入在同一个 GORM 事务里：全部成功才提交，任一失败全部回滚。事务提交后再 best-effort 核销 Redis reserved 库存桶；Redis 失败不回滚已完成的清算，后续由同步或巡检修复缓存视图。

## 5. 订单完成后的真实结算

买家主动确认收货和系统自动确认收货都在订单状态事务中写 `order.completed` Outbox。独立队列 `clearing.settle` 消费该事件，调用 `SettleCompletedOrder`：

1. 对订单和 `PaymentClearing` 加 `FOR UPDATE` 行锁；
2. 只有清算单为 `cleared` 且订单确实为 `OrderCompleted` 才继续；
3. 校验清算单买家、卖家与订单一致；
4. 锁定卖家钱包，增加 `net_cents`；
5. 写 `order_settle` 分录：`merchant_escrow debit / seller user_wallet credit`；
6. 条件更新清算单为 `settled`，写 `settled_at`；
7. 余额、两条分录和状态在同一事务提交。

`order.completed` 的发布与卖家入账是最终一致的：订单可能已经显示完成，但清算单短时间仍是 `cleared`。消费者遇到数据库抖动会 Nack 重排；解析错误、未知 routing key 或超过投递上限的消息进入 DLQ。

重复的 `order.completed` 不会重复放款：`settled` 和 `refunded` 都直接幂等返回，行锁、状态条件更新和台账唯一索引提供多层保护。

## 6. 退款以清算状态决定资金来源

退款申请和运营审批先推进订单状态并写 `order.refunded` Outbox，资金回退由 `refund.settle` 消费者异步执行。退款事务对清算单加行锁，按当前状态决定从哪里拿钱。

### 6.1 结算前退款：`cleared -> refunded`

资金仍在托管账户，分录为：

- `merchant_escrow debit`
- 买家 `user_wallet credit`
- `biz_type=escrow_refund`

卖家没有收到过这笔钱，因此不读取、不锁定也不扣减卖家钱包。退款成功后写 `refunded_at`，并回补库存。

### 6.2 结算后退款：`settled -> refunded`

资金已经进入卖家钱包，分录为：

- 卖家 `user_wallet debit`
- 买家 `user_wallet credit`
- `biz_type=refund`

卖家和买家的余额、台账、库存回补及清算状态在一个事务中提交。为兼容迁移前没有 `PaymentClearing` 的旧订单，代码按“已经结算给卖家”处理，从卖家退款。

有清算单时，退款金额以已经固化的 `gross_cents` 为准，并校验它仍与订单实付口径一致。当前 `fee_cents` 固定为 0；如果未来出现 `gross_cents != net_cents` 的手续费清算单，自动退款会先拒绝，直到补齐“卖家净额 + 平台手续费”拆分退款，避免多扣卖家。

当前退款会回补库存，但不会自动删除支付时创建的买家商品归属副本，因为该副本可能已经被改价或转售；这需要独立的溯源和人工处理方案。

### 6.3 结算与退款竞争

结算和退款都会锁定同一笔 `PaymentClearing`，并结合订单状态决定是否允许执行：

- 清算单已经 `refunded` 时，迟到的 `order.completed` 直接成功返回，不再放款；
- 清算单已经 `settled` 时，退款从卖家扣回；
- 订单进入退款时会把原状态写入 `order.refund_from_type`。退款中的迟到 `order.completed` 会被安全忽略；如果退款被驳回，订单恢复原状态并产生 `order.refund_rejected`。只有原状态是 Completed 时，该事件才重新触发结算；WaitShip / WaitReceive 只恢复履约，不提前放款。

## 7. 系统账户为什么需要 `account_code`

系统账户都使用 `user_id=0`，但资金含义不同，不能只靠 `user_id` 对账。`account_transaction.account_code` 明确区分：

| `account_code` | 含义 | 是否有 `users.money` |
| --- | --- | --- |
| `user_wallet` | 买家或卖家的站内钱包 | 有 |
| `external_clearing` | Stripe/Web3/Xcash 外部已收资金的本地会计对手方 | 无 |
| `merchant_escrow` | 支付成功后、履约完成前的商家托管款 | 无 |
| `legacy_system` | 旧红包、拼团等通过兼容入口写入的系统流水 | 无 |

`external_clearing` 与 `merchant_escrow` 即使都使用 `user_id=0`，也必须通过 `AppendSystemTransaction` 显式传入账户编码。系统账户没有加密余额行，`balance_after_cents` 记为 0，其余额应由对应 `account_code` 的流水聚合得出。

## 8. 表与字段

### 8.1 `payment_clearing`

| 字段 | 用途 |
| --- | --- |
| `order_id` | 关联订单；唯一索引保证一笔订单只有一个清算单 |
| `buyer_id`、`seller_id` | 固化清算时的交易双方，结算前与订单复核 |
| `channel` | `wallet`、`stripe`、`web3` 或 `xcash` |
| `provider_ref` | Stripe Session ID、Web3 tx hash、Xcash 的 `sys_no:chain:tx_hash`；Wallet 为空 |
| `gross_cents` | 订单清算总额，单位为分 |
| `fee_cents` | 渠道/平台费用；当前为 0 |
| `net_cents` | 最终放给卖家的金额；当前等于 `gross_cents` |
| `currency` | Wallet/Xcash 为订单计价 CNY，Stripe 为实付法币，Web3 为代币代码；Xcash 实付代币和数量保存在 payment intent |
| `status` | `cleared`、`settled`、`refunded` |
| `cleared_at`、`settled_at`、`refunded_at` | 各资金阶段时间；后两者在到达对应状态后写入 |

模型还包含公共的 `id`、`created_at`、`updated_at`、`deleted_at`。`buyer_id`、`seller_id`、`channel`、`provider_ref`、`status` 建有查询索引。

### 8.2 `account_transaction`

关键字段为：

- `user_id`：真实钱包用户，或系统账户统一使用的 0；
- `account_code`：区分 `user_wallet`、`external_clearing`、`merchant_escrow` 和兼容系统账户；
- `direction`：`debit` 或 `credit`；
- `amount_cents`：本次借贷金额；
- `ref_order_id`：关联订单；
- `balance_after_cents`：真实钱包变更后余额，系统账户为 0；
- `biz_type`：本链路使用 `order_clear`、`order_settle`、`escrow_refund`、`refund`。

唯一索引是 `(ref_order_id, direction, biz_type)`。因此同一订单可以分别写清算、结算和退款分录，但同一业务阶段、同一方向只能写一次。

### 8.3 `payment_anomaly`

记录外部渠道确实收款但不能进入正常清算的资金：`order_id` 关联订单，`channel + provider_ref` 唯一，`provider_amount` 保留渠道原始金额，`currency` 保留币种，`reason` 包括重复付款、金额/币种不符、链上付款细节不符或高风险，`status=pending_review`。它不是账务分录，而是退款/人工处置队列的事实记录；记录成功不等于退款完成。

### 8.4 关联业务表

- `order.type`：支付确认从待支付推进到待发货；确认收货后进入已完成；退款审批后进入已退款。
- `order.refund_from_type`：进入退款前原子保存的状态，驳回时恢复，避免未发货订单被错误跳到 Completed。
- `order.money`、`promo_rule_id`、`final_cents`：共同决定权威实付和退款金额。
- `users.money`：买家、卖家的加密站内余额，必须在事务和行锁下读改写。
- `product.num`：数据库权威库存，支付时条件扣减，退款时回补。
- `outbox_event`：承载 `order.paid`、`order.completed`、`order.refunded` 等最终一致事件。

## 9. 幂等与失败边界

### 9.1 幂等防线

| 层级 | 机制 |
| --- | --- |
| 客户端入口 | Wallet、Stripe、Web3、Xcash 支付接口使用请求幂等中间件 |
| Stripe 建会话 | 同订单复用 Checkout 幂等键 |
| Stripe webhook | `stripe:event:{event_id}` 去重，数据库错误时释放占位供重试 |
| Web3 授权 | nonce 原子消费，防签名重放 |
| Web3 listener | `tx_hash + log_index` 去重，并保存安全区块水位 |
| Xcash Webhook | HMAC + 五分钟时间窗；`appid + nonce` 与结算同事务持久化去重 |
| Xcash 主动对账 | 轮转扫描 waiting、risk_pending 与最近 24 小时 expired 账单，复用清算单、订单状态和 provider_ref 幂等守卫 |
| 清算单 | `payment_clearing.order_id` 唯一 |
| 外部重复实收 | `payment_anomaly(channel, provider_ref)` 唯一，同一事件只进入一次待退款队列 |
| 订单支付 | `WHERE type=WaitPay` 条件推进 |
| 资金台账 | `(ref_order_id, direction, biz_type)` 唯一 |
| 履约结算 | 清算单行锁、`cleared -> settled` 条件更新 |
| 退款 | 清算单行锁、买家 credit 预检、台账唯一键、最终 `refunded` 状态 |
| 消息 | Transactional Outbox、at-least-once 消费、重试上限与 DLQ |

Redis 去重是减压层，不是最终资金安全边界。Redis 不可用时，数据库唯一约束、状态守卫和事务仍必须保证不重复扣款、不重复放款。

### 9.2 原子边界

- **Wallet 清算事务**：买家扣款、清算单、两条清算分录、库存、订单、商品归属、`order.paid` 同生共死。
- **Stripe/Web3/Xcash 清算事务**：外部支付已经发生；本地清算单、分录和支付业务状态同生共死。外部与本地之间靠可信事件重试或主动对账恢复。
- **订单完成事务**：订单进入 Completed 和 `order.completed` Outbox 同生共死；卖家放款由下游独立事务最终完成。
- **结算事务**：卖家余额、托管 debit、卖家 credit、清算单 `settled` 同生共死。
- **退款审批事务**：订单进入 Refunded 和 `order.refunded` Outbox 同生共死；真正退款由下游独立事务完成。
- **退款事务**：资金回退、两条退款分录、库存回补、清算单 `refunded` 同生共死。
- **提交后缓存动作**：Redis reserved 核销、Web3 pending 删除都是 best-effort，不反向回滚数据库。

## 10. 代码入口

| 职责 | 文件 / 入口 |
| --- | --- |
| 清算单模型与状态 | `internal/clearing/model.go` / `PaymentClearing` |
| 外部重复实收异常 | `internal/clearing/model.go` / `PaymentAnomaly` |
| 统一清算、履约后结算 | `internal/clearing/service.go` / `RecordClearedTx`、`SettleCompletedOrder` |
| `order.completed` 结算消费者 | `internal/clearing/consumer.go` / `StartSettleConsumer` |
| Wallet 支付确认 | `internal/payment/service.go` / `PaymentSrv.PayDown` |
| Stripe 建会话、Webhook、清算 | `internal/payment/service_stripe.go` / `CreateCheckout`、`HandleWebhook`、`settleStripeOrder` |
| Web3 授权与 pending | `internal/payment/service_crypto.go` / `IssueNonce`、`VerifyAndPark` |
| Web3 链上监听 | `service/web3/listener.go` / `StartPaymentListener` |
| Web3 确认消息与清算 | `internal/payment/consumer_web3.go`、`internal/payment/service_web3_settle.go` |
| Xcash 账单、Webhook 与主动对账 | `internal/payment/xcash_client.go`、`internal/payment/service_xcash.go`、`initialize/xcash.go` |
| 支付确认事务尾段 | `internal/payment/settle.go` / `finishPaymentConfirmationTx` |
| 确认收货与完成事件 | `internal/order/shipping.go`、`internal/order/task.go` |
| 台账模型和系统账户写入 | `internal/money/ledger_model.go`、`internal/money/ledger_repo.go` |
| 退款审批与资金回退 | `internal/refund/service.go`、`internal/refund/consumer.go` |
| 表结构迁移 | `internal/migrate/migrate.go` |

## 11. 测试清单

### 11.1 已落地的核心测试

`internal/clearing/service_test.go` 已覆盖：

- 渠道与资金来源不匹配时拒绝清算；
- 清算时卖家不入账；
- 订单未完成时拒绝结算；
- 重复 `order.completed` 只给卖家入账一次；
- `external_clearing` 与 `merchant_escrow` 使用不同 `account_code`；
- 非法完成事件被识别为毒消息。
- 同 provider_ref 重放不会建异常单，不同外部实收会幂等写入一笔 `pending_review`。

`internal/refund/service_test.go` 已覆盖结算前、结算后两条退款路径，以及重复退款不会重复入账或重复回补库存。

### 11.2 清算与结算

- [ ] Wallet 清算后买家只扣一次、卖家不变、托管金额正确。
- [ ] Stripe/Web3/Xcash 清算不改变买家钱包，外部清算 debit 与托管 credit 等额。
- [ ] 促销订单使用 `final_cents`，全额优惠不会回退到原价。
- [ ] 清算单、两条分录、库存、订单、商品归属或 Outbox 任一步失败时全部回滚。
- [ ] 同订单多渠道竞争时只有一个清算事务成功。
- [ ] 竞争中另一笔外部实收进入 `payment_anomaly` 并可被退款流程检索，不能静默吞掉。
- [ ] 未完成订单不能放款；主动确认和自动确认都能触发结算。
- [ ] 结算只增加卖家 `net_cents`，并原子写 `settled_at`。
- [ ] 重复、乱序、并发的 `order.completed` 不会重复放款。
- [ ] 结算消费者的可重试错误会重排，毒消息和超限消息进入 DLQ。

### 11.3 退款

- [ ] `cleared` 退款从 `merchant_escrow` 退给买家，卖家余额不变。
- [ ] `settled` 退款从卖家退给买家。
- [ ] 迁移前无清算单的旧订单按已结算路径退款。
- [ ] 重复 `order.refunded` 不重复退款或重复回补库存。
- [ ] 结算与退款并发时只会形成一个合法终态，不出现托管和卖家双扣。
- [ ] 退款金额与支付清算金额口径一致，含促销和零元边界。
- [ ] 资金写入、库存回补或清算状态更新失败时整个退款事务回滚。

### 11.4 渠道与故障恢复

- [ ] Stripe 签名无效时拒绝；已实收但金额/币种不符时不创建清算单、只创建一笔 `pending_review` 异常；数据库瞬时失败可由 webhook 重投恢复。
- [ ] Web3 buyer 绑定、金额、确认深度和 tx hash 传递正确；回扫不会重复清算。
- [ ] Xcash 多链多币种白名单、签名、nonce、付款细节与风险校验正确；Webhook 丢失后主动对账可收敛。
- [ ] Redis 不可用时数据库幂等防线仍有效。
- [ ] 数据库提交后 reserved 核销或 pending 删除失败，不把已提交事务伪装成失败支付。
- [ ] `payment_clearing` 与 `account_transaction` 可以按订单还原完整的清算、结算或退款资金路径。

建议执行：

```bash
go test ./internal/clearing ./internal/payment ./internal/refund ./internal/money ./internal/order ./service/web3
go test -race ./internal/clearing ./internal/payment ./internal/refund
go test ./...
```

## 12. 运维检查

运行时至少关注：长期停在 `cleared` 且订单已经 Completed 的清算单、`payment_anomaly.status=pending_review` 的重复实收、`merchant_escrow` 按 `account_code` 聚合后的异常余额、只有单边分录的订单、`order.completed` / `order.refunded` 消费 DLQ、Stripe/Web3/Xcash 外部成功但本地没有清算单的订单，以及 Outbox 长期 pending/publishing/dead 的事件。

出现异常时应优先重放原始可信事件或触发 Xcash 主动对账，让现有幂等链路修复；不要直接改卖家余额或订单状态。人工调账必须追加可审计流水，并保留订单号、Stripe Session ID 或链上 tx hash。
