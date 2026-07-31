# 00.03 参考实现

`InsertOrder` 与 `InsertOutbox` 共用同一个事务对象。Outbox 插入报错时，事务回调把错误向外返回，内存数据库不会替换原状态，等价于数据库回滚。
