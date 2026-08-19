# gomall 文档导航

这里是仓库文档的唯一入口。教学材料统一使用 Markdown，不再维护博客、TeX、幻灯片或生成 PDF 等平行版本。

## 从哪里开始

- 想按课程顺序学习：进入 [课程讲义](./lecture/README.md)。
- 想查普通订单的资金链路：进入 [清算、结算与退款](./architecture/PAYMENT_CLEARING_SETTLEMENT.md)。
- 想直接看图：查看下方“架构与流程图”。

## 课程讲义

[课程讲义总目录](./lecture/README.md) 是唯一教学顺序。支付相关内容按以下顺序阅读：

1. [支付（上）：余额支付与资金事务](./lecture/02-payment-up.md)
2. [支付（下）：幂等、熔断与对账](./lecture/03-payment-down.md)
3. [支付清算：平台收到的钱去了哪里](./lecture/payment-clearing.md)
4. [支付结算：订单完成后怎么把钱给卖家](./lecture/payment-settlement.md)
5. [Web3 支付（上）：签名与付款发起](./lecture/08-payment-web3.md)
6. [Web3 支付（下）：链上事件结算](./lecture/09-payment-web3-settlement.md)

## 架构与设计

- [普通订单的清算、结算与退款](./architecture/PAYMENT_CLEARING_SETTLEMENT.md)

## 架构与流程图

- [整体架构](./architecture.svg)
- [DDD 领域地图](./architecture-domains.svg)
- [订单流程](./flow-order.svg)
- [支付流程](./flow-payment.svg)
- [支付渠道与统一结算](./payment-channels.svg)
- [资金台账](./funds-ledger.svg)
- [库存流程](./flow-inventory.svg)
- [搜索流程](./flow-search.svg)

SVG 是架构原图，不是另一套教学文档。对应解释仍以课程讲义和架构 Markdown 为准。

## 文档维护约定

1. 教学内容只维护 Markdown 源稿。
2. 新课程必须加入 `docs/lecture/README.md`，不另建平行目录。
3. 跨课程共用的系统设计说明放入 `docs/architecture/`。
4. 导出的 PDF、幻灯片、博客改写稿和临时生成文件不提交到仓库。
5. 同一主题只能有一个明确入口；补充材料从主讲义链接出去。
