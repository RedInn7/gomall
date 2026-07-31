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
	// TODO: 只把 req 当成购买意图；身份、价格、卖家取服务端可信数据。
	return Order{}, nil
}
