# 09.01 服务端权威结算

## 题目背景

用户在购物车点击“提交订单”时，请求里的商品 ID、数量和地址 ID 都来自客户端。攻击者可以抓包伪造用户、价格和卖家，正常用户也可能持有已经过期的购物车价格。如果订单服务直接相信这些字段，就可能按一分钱成交、把货款记给错误卖家，或使用不属于当前用户的地址。

系统必须先验证地址归属，再从商品服务读取最新商品。只有认证结果可以决定买家，只有商品服务可以决定价格和卖家。

## 题目描述

实现 `PrepareCheckout`，构造可用于创建订单的权威结算快照：

1. 校验认证用户、商品 ID、地址 ID 和正数数量，非法请求不得调用依赖。
2. 先查询地址归属；地址不属于认证用户时停止，不能继续查询商品。
3. 商品服务错误原样返回；商品 ID 不匹配、未上架或卖家为空时返回 `ErrProductUnavailable`。
4. 拒绝负价格，并检测单价乘数量的 `int64` 溢出。
5. 忽略请求中的 `UserID`、`PriceCents` 和 `SellerID`，从认证上下文与商品服务构造结果。

## 输入与已有接口

```go
func PrepareCheckout(req Request, authUserID uint, addresses AddressBook, catalog Catalog) (Checkout, error)
```

`AddressBook.OwnerOf` 返回地址所有者，`Catalog.GetProduct` 返回商品表中的最新数据。依赖错误应原样传播。

## 输出与完成条件

成功时返回包含认证用户、服务端单价、服务端卖家与准确小计的 `Checkout`。任何失败都返回零值 `Checkout`，并满足调用顺序要求。

## 调用样例

```go
req := Request{UserID: 999, ProductID: 7, Num: 2, AddressID: 3, PriceCents: 1, SellerID: 888}
product := Product{ID: 7, PriceCents: 2500, SellerID: 42, OnSale: true}

checkout, err := PrepareCheckout(req, 10, addresses, catalog)
// err == nil
// checkout.UserID == 10
// checkout.UnitCents == 2500
// checkout.SubtotalCents == 5000
// checkout.SellerID == 42
```

## 约束与提示

- 数量必须大于 0；免费商品价格 0 合法，负价格非法。
- 地址越权时不得调用商品服务，避免无效工作和信息泄露。
- 乘法前做溢出检查，不能依赖溢出后的负数来判断。
- 请求中的伪造事实不能出现在结果中。

## 本地运行

```bash
go test -tags exercise ./exercises/09-cart-to-order/09.01-authoritative-checkout/problem
```
