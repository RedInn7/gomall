# Xcash Base USDC 支付接入

## 目标

为普通 Gomall 订单增加 Xcash 加密货币支付渠道。第一阶段只开放 Base 网络的 USDC，并要求 Xcash 项目在后台使用 VaultSlot 智能合约独立地址模式，不允许使用依赖金额尾数区分订单的 Differ 模式。

## 用户流程

1. 登录用户对自己的待支付订单创建 Xcash 账单。
2. Gomall 只向 Xcash 发送服务端计算的订单实付金额，不接受客户端金额、链或币种。
3. Xcash 返回支付页；用户按页面给出的独立地址支付正常金额的 USDC。
4. Xcash 在链上交易达到确认数后，以带 HMAC 签名的 Webhook 通知 Gomall。
5. Gomall 校验通知、账单身份、Base/USDC、收款地址、金额和交易哈希后，在同一数据库事务内完成清算、扣库存、推进订单并记录 Webhook 幂等凭据。

## 对外接口

- `POST /api/v1/paydown/xcash`：登录后为订单创建或复用账单，请求只包含 `order_id`。
- `GET /api/v1/paydown/xcash?order_id=...`：登录后查询自己的账单状态，并主动与 Xcash 对账。
- `POST /api/v1/webhooks/xcash`：公开回调，只接受通过 Xcash HMAC-SHA256 校验且时间戳在允许窗口内的通知。

## 必须满足的业务规则

- 固定请求 `methods={"USDC":["base"]}`，客户端不能覆盖。
- 同一订单只创建一张本地账单；重复请求复用已有支付页。
- 订单号、金额、币种、链、收款地址或实付金额不一致时不得放货，已经收到的外部款进入 `payment_anomaly`。
- `XC-Nonce` 在数据库中唯一；Webhook 事务失败时幂等凭据必须一同回滚，使 Xcash 可以安全重试。
- 清算引用使用 `base:tx_hash`；正常重放不重复入账，不同付款不能被当作同一次重放。
- 只有 `confirmed=true` 的账单事件可以推进订单。
- 支付成功后沿用现有 `merchant_escrow`、库存扣减、商品归属和 `order.paid` 流程。

## 配置

- `XCASH_BASE_URL`
- `XCASH_APP_ID`
- `XCASH_HMAC_KEY`
- `XCASH_NOTIFY_URL`
- `XCASH_RETURN_URL`（可选）
- `XCASH_INVOICE_DURATION_MINUTES`（可选，默认 15，范围 5–30）

Xcash 管理后台必须启用 Base、USDC 和 VaultSlot 收款模式，并关闭该项目的 Differ 钱包直收模式。Gomall 无法通过公开创建账单接口远程验证后台所选模式，因此这项部署检查不可省略。

## 验收场景

- 正常创建 Base USDC 账单并完成支付。
- 重复创建请求返回同一张账单。
- 请求签名与 Webhook 签名符合 Xcash 文档。
- 伪造、过期或重复 Webhook 不会入账。
- 错链、错币、错地址、错金额、高风险和重复付款进入异常款，订单保持原状态。
- Xcash 暂时不可用、回调处理失败和服务重启后均可重试或通过查询对账恢复。
