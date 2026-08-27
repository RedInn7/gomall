# 07.01 参考实现说明

参考实现先验证类目，再按 `info → title → name` 顺序选择去除首尾空白后的关键词；随后规范页码和页大小，最后按固定顺序追加过滤条件。

`OnSale` 使用 `*bool`，因此 `nil` 表示未提供过滤条件，非空指针即使指向 `false` 也必须进入计划。固定的过滤器顺序使等价请求生成稳定结构，便于测试和缓存。

验证命令：

```bash
go test -tags exercise ./exercises/07-product-search/07.01-search-query-plan/solution
```
