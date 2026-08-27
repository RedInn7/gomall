# 07.02 商品修改和索引事件必须一起落地

## 题目背景

Gomall 以 MySQL 中的商品记录作为 Source of Truth，Elasticsearch 只保存可重建的搜索副本。商家修改商品名称后，接口先更新 MySQL，再由后台消费者根据 Outbox 事件刷新 ES。如果商品已经提交而事件没有写入，搜索结果会长期停留在旧版本；如果事件先发出而事务随后失败，ES 又可能展示数据库里不存在的状态。

因此，商品和 `product.changed` 事件必须在同一个事务中提交。消息系统还会重投，后台修复也可能为同一商品版本补写新事件，所以写入函数必须同时处理原子性、事件幂等和商品版本冲突。

## 题目描述

请实现 `SaveProductAndEvent`：

1. 拒绝商品 ID 为 0 或空事件 ID，校验失败不改变数据库。
2. 在一次 `db.Transaction` 中写入商品和 Outbox 事件。
3. 已存在的事件 ID 若指向同一商品、同一版本，视为幂等重放并直接成功；若指向其他商品或版本，返回 `ErrEventConflict`。
4. 商品版本低于数据库版本时返回 `ErrStaleVersion`；版本相同但商品内容不同，返回 `ErrVersionConflict`。
5. 相同商品内容和版本可以使用新的事件 ID 补写事件。
6. Outbox 写入失败时返回 `ErrOutboxUnavailable`，商品更新必须一起回滚。

## 输入格式

本题不读取标准输入。调用函数：

```go
func SaveProductAndEvent(db *DB, product Product, eventID string) error
```

`DB` 是题目提供的内存事务模型；`FailOutbox` 用于模拟事件表暂时不可写。

## 输出格式

成功时返回 `nil`，并原子更新 `db.Products` 与 `db.Events`。失败时返回题目给出的错误变量，两个集合都必须保持调用前状态。

## 输入输出样例 #1

### 调用

```go
db := NewDB()
err := SaveProductAndEvent(db, Product{
    ID:      42,
    Name:    "便携咖啡壶",
    Version: 3,
}, "evt-product-42-v3")
```

### 返回后的状态

```go
err == nil

db.Products[42] == Product{
    ID: 42, Name: "便携咖啡壶", Version: 3,
}

db.Events["evt-product-42-v3"] == Event{
    ID:        "evt-product-42-v3",
    Topic:     "product.changed",
    ProductID: 42,
    Version:   3,
}
```

如果在调用前设置 `db.FailOutbox = true`，函数应返回 `ErrOutboxUnavailable`，并且 `Products[42]` 和该事件都不能出现。

## 约束与提示

- `Product.ID > 0`，`Version` 使用无符号递增版本号。
- `eventID` 去除首尾空白后才参与空值判断和幂等判断。
- 幂等重放必须在检查模拟故障之前返回成功，否则同一条已经落库的事件会因当前 Outbox 故障而错误失败。
- 任何冲突或写入失败都不能留下只有商品、没有事件的半状态。
- 不要在事务外先修改 `db.Products`；题目提供的事务通过副本提交来模拟数据库回滚。

## 本地运行

```bash
go test -tags exercise ./exercises/07-product-search/07.02-index-outbox/problem
```

完成 `problem/index.go` 中的 TODO 后，公开测试会覆盖成功写入、参数错误、Outbox 回滚、事件重放、事件冲突、陈旧版本、同版本内容冲突和版本升级。
