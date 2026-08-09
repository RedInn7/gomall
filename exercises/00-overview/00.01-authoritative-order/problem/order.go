//go:build exercise

package authoritativeorder

import "errors"

var (
	ErrInvalidQuantity = errors.New("invalid quantity")
	ErrProductMismatch = errors.New("product mismatch")
	ErrAddressNotOwned = errors.New("address not owned")
)

type Request struct {
	UserID     uint
	ProductID  uint
	Quantity   int
	AddressID  uint
	PriceCents int64
	MerchantID uint
}

type Product struct {
	ID         uint
	PriceCents int64
	MerchantID uint
}

type Order struct {
	UserID     uint
	ProductID  uint
	Quantity   int
	AddressID  uint
	UnitCents  int64
	MerchantID uint
}

func BuildOrder(req Request, authUserID uint, product Product, addressOwnerID uint) (Order, error) {
	// TODO: 校验请求并生成可信订单。
	// 1. Quantity 必须大于 0，否则返回 ErrInvalidQuantity；
	// 2. req.ProductID 必须等于 product.ID，否则返回 ErrProductMismatch；
	// 3. addressOwnerID 必须等于 authUserID，否则返回 ErrAddressNotOwned；
	// 4. 订单中的用户取 authUserID，价格和商家取 product，不能相信请求中的同名字段；
	// 5. 商品 ID、数量和地址 ID 使用已经通过校验的请求数据。
	return Order{}, nil
}
