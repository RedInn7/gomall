# 固定账户加锁顺序

## 背景

晚上八点，GoMall 同时收到两笔余额支付：Alice 在购买 Bob 的二手显示器，Bob 也恰好在购买 Alice 的机械键盘。

第一笔事务先锁 Alice，再等 Bob；第二笔事务先锁 Bob，再等 Alice。两边都拿着一把锁等待另一把锁，数据库最终只能选择一笔事务回滚。这不是余额不足，也不是机器太慢，而是两笔业务采用了不同的加锁顺序。

## 需要实现

补全：

```go
func OrderedAccountIDs(buyerID, sellerID uint) []uint
```

函数只负责生成一笔支付应该采用的账户加锁顺序。无论谁买谁，都必须按照账户 ID 从小到大加锁。

## 输入与输出

- 输入：买家账户 ID 和卖家账户 ID；
- 输出：去重后的账户 ID 列表，严格升序排列；
- 当买家和卖家是同一个账户时，只返回一个 ID。

## 样例

```text
OrderedAccountIDs(9, 3) -> [3, 9]
OrderedAccountIDs(3, 9) -> [3, 9]
OrderedAccountIDs(7, 7) -> [7]
```

前两组的资金方向相反，但锁顺序相同，因此不会形成循环等待。

## 实现要求

- 不得依赖调用参数的先后顺序；
- 不得返回重复账户；
- 不要为公开样例中的 `3`、`7`、`9` 写特殊分支；
- 正确处理 `uint` 的完整取值范围。

## 运行测试

```bash
go test -tags exercise ./exercises/02-payment-up/02.01-ordered-account-locks/problem
```
