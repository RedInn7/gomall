//go:build exercise

package authoritativecheckout

import "errors"

var (
	ErrInvalidUser        = errors.New("invalid user")
	ErrInvalidProduct     = errors.New("invalid product")
	ErrInvalidQuantity    = errors.New("invalid quantity")
	ErrAddressNotOwned    = errors.New("address not owned")
	ErrProductUnavailable = errors.New("product unavailable")
	ErrInvalidPrice       = errors.New("invalid product price")
	ErrAmountOverflow     = errors.New("amount overflow")
)

type Request struct {
	UserID     uint
	ProductID  uint
	Num        int64
	AddressID  uint
	PriceCents int64
	SellerID   uint
}

type Product struct {
	ID         uint
	PriceCents int64
	SellerID   uint
	OnSale     bool
}

type Checkout struct {
	UserID        uint
	ProductID     uint
	Num           int64
	AddressID     uint
	UnitCents     int64
	SubtotalCents int64
	SellerID      uint
}

type AddressBook interface {
	OwnerOf(addressID uint) (uint, error)
}
type Catalog interface {
	GetProduct(productID uint) (Product, error)
}

func PrepareCheckout(req Request, authUserID uint, addresses AddressBook, catalog Catalog) (Checkout, error) {
	// TODO 1：先校验鉴权用户、商品 ID、地址 ID 和数量；失败时不得调用依赖。
	// TODO 2：先查询地址归属，只有地址属于 authUserID 才能继续查商品，依赖错误原样返回。
	// TODO 3：商品必须与请求 ID 一致、处于上架状态、价格不能为负且 SellerID 不能为 0。
	// TODO 4：检测 PriceCents * Num 的 int64 溢出。
	// TODO 5：用户取 authUserID，价格和卖家只取 Catalog；忽略请求中的 UserID、PriceCents、SellerID。
	return Checkout{}, nil
}
