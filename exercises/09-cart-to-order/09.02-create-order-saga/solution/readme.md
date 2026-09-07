# 09.02 参考实现说明

参考实现先判断重放和冲突，再预占库存；订单与事件在内存事务副本中一起提交。事务错误会触发库存释放，释放失败使用 `errors.Join` 保留两个错误原因。

```bash
go test -tags exercise ./exercises/09-cart-to-order/09.02-create-order-saga/solution
```
