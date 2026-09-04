# Xcash 多币种支付接入

## 目标

为普通 Gomall 订单增加 Xcash 加密货币支付渠道。币种和链由 Xcash 项目统一配置，Gomall 可以同时提供多种付款方式，并要求 Xcash 项目在所有启用链上使用 VaultSlot 智能合约独立地址模式，不允许使用依赖金额尾数区分订单的 Differ 模式。

## 用户流程

1. 登录用户对自己的待支付订单创建 Xcash 账单。
2. Gomall 只向 Xcash 发送服务端计算的订单实付金额，不接受客户端金额或自行拼装的收款方式。
3. Xcash 返回支付页；用户从项目已启用的币种和链中选择付款方式，再按页面给出的独立地址支付正常金额。
4. Xcash 在链上交易达到确认数后，以带 HMAC 签名的 Webhook 通知 Gomall。
5. Gomall 再通过 HTTPS 查询最终账单，校验通知、账单身份、服务端允许的链币组合、收款地址、金额和交易哈希；严格风控模式下还要等 AML 得出 Low/Moderate，才会在同一数据库事务内完成清算、扣库存、推进订单并记录 Webhook 幂等凭据。

## 对外接口

- `POST /api/v1/paydown/xcash`：登录后为订单创建或复用账单，请求只包含 `order_id`。
- `GET /api/v1/paydown/xcash?order_id=...`：登录后查询自己的账单状态，并主动与 Xcash 对账。
- `POST /api/v1/webhooks/xcash`：公开回调，只接受通过 Xcash HMAC-SHA256 校验且时间戳在允许窗口内的通知。

服务启动后还会每分钟轮转查询等待付款、等待风控以及最近 24 小时过期的账单；即使 Webhook 丢失或过期账单随后完成链上确认，也会进入同一条幂等结算路径。

## 必须满足的业务规则

- 可用币种和链取自服务端 `XCASH_METHODS_JSON` 白名单；未配置白名单时由 Xcash 项目提供所有已启用方式，客户端不能覆盖。
- 同一订单同时只使用一张未过期账单；重复请求复用已有支付页，旧账单过期后才创建下一次尝试。买家在付款前可以合法换链或换币，结算只核对 Xcash 的最终账单，不把 waiting 阶段的临时支付指引当成不可变值。
- 订单号、金额、币种、链、收款地址或实付金额与 Xcash 最终账单不一致时不得放货，已经收到的外部款进入 `payment_anomaly`。
- `XC-Nonce` 在数据库中唯一；Webhook 事务失败时幂等凭据必须一同回滚，使 Xcash 可以安全重试。
- 清算引用使用 `sys_no:chain:tx_hash`；正常重放不重复入账，同一链上交易里的不同账单也不会被折叠。
- 只有 `confirmed=true` 的账单事件可以推进订单。
- 支付成功后沿用现有 `merchant_escrow`、库存扣减、商品归属和 `order.paid` 流程。

## 配置

- `XCASH_BASE_URL`
- `XCASH_APP_ID`
- `XCASH_HMAC_KEY`
- `XCASH_NOTIFY_URL`
- `XCASH_VAULTSLOT_CONFIRMED=true`（必填部署门禁；确认所有开放链都使用 VaultSlot 后才能启动集成）
- `XCASH_RETURN_URL`（可选）
- `XCASH_INVOICE_DURATION_MINUTES`（可选，默认 15，范围 5–30）
- `XCASH_METHODS_JSON`（可选，例如 `{"USDC":["base","ethereum"],"USDT":["base","arbitrum-one","tron"],"ETH":["ethereum"]}`）
- `XCASH_REQUIRE_AML_RESULT`（可选，默认 `true`；风险结果为空时保持 `risk_pending`，只接受 Low/Moderate。若 Xcash 项目没有开 AML，必须在明确接受风险后设置为 `false`）

Gomall 订单金额是人民币“分”，因此 Xcash 账单固定使用 `CNY` 计价；加密货币种类仍由 `XCASH_METHODS_JSON` 或 Xcash 项目配置决定，不能把人民币金额原值改成 USD 开票。

Xcash 管理后台必须为每条开放的链启用 VaultSlot 收款模式，并关闭该项目的 Differ 钱包直收模式。Gomall 无法通过公开创建账单接口远程读取后台所选模式，因此除人工检查外，还必须显式设置 `XCASH_VAULTSLOT_CONFIRMED=true` 才能启用。EVM 网络可以启用其已配置的原生资产和 ERC-20 代币；Tron 当前只开放 USDT 与 TRX。

生产环境还必须完成以下部署检查：

- `XCASH_BASE_URL` 使用 HTTPS；Gomall 不跟随查询接口的重定向。
- Xcash 项目通知开关已开启，`XCASH_NOTIFY_URL` 是公网 HTTPS 地址，并把 Gomall 出口 IP 加入 Xcash 项目白名单。
- 每条 VaultSlot 链的系统钱包有足够 Gas；Tron 同时准备足够 Energy/TRX，确保合约部署与归集可以执行。
- 严格 AML 模式下，Xcash 项目已开通并启用 AML，且筛查阈值覆盖 Gomall 要求的订单范围；否则付款会停在 `risk_pending`，不会放货。
- 当前 Xcash 的公开账单查询会缓存已完成账单约 1 小时，而 AML 写回不会主动清除此缓存。生产部署前应在 Xcash 端补上 AML 写回后的 `invoice:public` 缓存失效，或提供不缓存的已签名商户查询接口；在上游修复前，Gomall 会安全停在 `risk_pending`，但放货最多可能延迟约 1 小时。

## 验收场景

- 正常创建多币种账单，分别选择允许的链币组合并完成支付。
- 重复创建请求返回同一张账单。
- 请求签名与 Webhook 签名符合 Xcash 文档。
- 伪造、过期或重复 Webhook 不会入账。
- 错链、错币、错地址、错金额、高风险和重复付款进入异常款，订单保持原状态。
- Xcash 暂时不可用、回调处理失败和服务重启后均可重试或通过查询对账恢复。
