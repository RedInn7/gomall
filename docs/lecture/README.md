# gomall 课程录制顺序

总览负责把系统地图铺开，正式课程从用户鉴权开始。每一集控制在 40 分钟左右；一个主题讲不完就拆成上下，不把自测和延伸内容硬塞进正片。

| 序号 | 主题 | 讲义 | 单集时长 |
|---|---|---|---:|
| 00（上） | 角色、分层与交易主链路 | [00-overview.md](./00-overview.md) | 约 40 min |
| 00（下） | 库存、Outbox 与异步架构 | [00-overview-architecture.md](./00-overview-architecture.md) | 约 40 min |
| 01 | 用户与鉴权 | [01-user-auth.md](./01-user-auth.md) | 上下各约 40 min |
| 02 | 支付（上）：余额支付与资金事务 | [02-payment-up.md](./02-payment-up.md) | 约 40 min |
| 03 | 支付（下）：幂等、熔断与对账 | [03-payment-down.md](./03-payment-down.md) | 约 40 min |
| 04 | 支付清算：平台收到的钱去了哪里 | [04-payment-clearing.md](./04-payment-clearing.md) | 约 40 min |
| 05 | 支付结算：订单完成后怎么把钱给卖家 | [05-payment-settlement.md](./05-payment-settlement.md) | 约 40 min |
| 06 | 商品展示 | [06-product-display.md](./06-product-display.md) | 约 40 min |
| 07 | 商品搜索（上）：Elasticsearch | [07-product-search.md](./07-product-search.md) | 约 40 min |
| 08 | 商品搜索（下）：Hybrid Search | [08-product-search-hybrid.md](./08-product-search-hybrid.md) | 约 40 min |
| 09 | 购物车到订单 | [09-cart-to-order.md](./09-cart-to-order.md) | 约 40 min |
| 10 | Web3 支付（上）：签名与付款发起 | [10-payment-web3.md](./10-payment-web3.md) | 约 40 min |
| 11 | Web3 支付（下）：链上事件结算 | [11-payment-web3-settlement.md](./11-payment-web3-settlement.md) | 约 40 min |
| 12 | 库存与防超卖 | [12-inventory.md](./12-inventory.md) | 约 40 min |
| 13 | 预售定金 | [13-preorder.md](./13-preorder.md) | 约 40 min |
| 14 | 接入层护栏（上） | [14-middleware.md](./14-middleware.md) | 约 40 min |
| 15 | 接入层护栏（下） | [15-middleware-transaction.md](./15-middleware-transaction.md) | 约 40 min |

## 写作顺序

讲义按同一套顺序展开：

1. 先写这一节要学生带走的判断；
2. 再给事故、客诉或业务场景，说明这个判断解决什么问题；
3. 接着走读代码，解释约束落在哪一层；
4. 最后用故障演练或测试证明代码守住了前面的判断。

标题不代替结论。每一节开头要能单独回答“这段代码为什么存在”，后面的流程图、代码和测试只负责把答案讲透。
