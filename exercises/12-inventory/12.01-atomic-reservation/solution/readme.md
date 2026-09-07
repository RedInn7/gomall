# 12.01：原子库存预占与幂等状态流转（参考实现）

参考实现使用一把互斥锁保护完整状态机。锁内依次完成 reservation 查询、幂等判断、库存校验、库存桶移动和状态写入，避免并发请求同时基于旧快照做决定。

关键行为：

- 首次 `Reserve` 原子地执行 `Available -= qty` 和 `Reserved += qty`；
- 相同 ID、相同数量的 Reserve 重试直接成功；
- `Commit` 只把 `Reserved` 移到 `Sold`；
- `Release` 只把 `Reserved` 退回 `Available`；
- 重复终态操作幂等成功，相反终态操作返回 `ErrAlreadyFinalized`；
- 所有错误路径都在写入前返回，库存及记录保持原样；
- `Snapshot` 在锁内复制 map，调用方不能修改内部 reservation 状态。

运行参考实现及竞态检测：

```bash
go test -race -tags exercise ./exercises/12-inventory/12.01-atomic-reservation/solution
```
