# gomall 课程录制顺序

总览负责把系统地图铺开，正式课程从用户鉴权开始。每一集控制在 40 分钟左右；一个主题讲不完就拆成上下，不把自测和延伸内容硬塞进正片。

| 顺序 | 主题 | 讲义 | 单集时长 |
|---|---|---|---:|
| 总览（上） | 角色、分层与交易主链路 | [00-overview.md](./00-overview.md) | 约 40 min |
| 总览（下） | 库存、Outbox 与异步架构 | [00-overview-architecture.md](./00-overview-architecture.md) | 约 40 min |
| 第一讲 | 用户与鉴权 | [01-user-auth.md](./01-user-auth.md) | 上下各约 40 min |
| 第二讲（上） | 余额支付与资金事务 | [02-payment.md](./02-payment.md) | 约 40 min |
| 第二讲（下） | 幂等、熔断与对账 | [02-payment-resilience.md](./02-payment-resilience.md) | 约 40 min |
| 第三讲 | 商品展示 | [03-product-display.md](./03-product-display.md) | 约 40 min |
| 第四讲（上） | Elasticsearch 商品搜索 | [04-product-search.md](./04-product-search.md) | 约 40 min |
| 第四讲（下） | Hybrid Search | [04-product-search-hybrid.md](./04-product-search-hybrid.md) | 约 40 min |
| 第五讲 | 购物车到订单 | [05-cart-to-order.md](./05-cart-to-order.md) | 约 40 min |
| 第六讲（上） | Web3 签名与付款发起 | [06-payment-web3.md](./06-payment-web3.md) | 约 40 min |
| 第六讲（下） | 链上事件结算 | [06-payment-web3-settlement.md](./06-payment-web3-settlement.md) | 约 40 min |
| 第七讲 | 库存与防超卖 | [07-inventory.md](./07-inventory.md) | 约 40 min |

## 写作顺序

讲义按同一套顺序展开：

1. 先写这一节要学生带走的判断；
2. 再给事故、客诉或业务场景，说明这个判断解决什么问题；
3. 接着走读代码，解释约束落在哪一层；
4. 最后用故障演练或测试证明代码守住了前面的判断。

标题不代替结论。每一节开头要能单独回答“这段代码为什么存在”，后面的流程图、代码和测试只负责把答案讲透。
