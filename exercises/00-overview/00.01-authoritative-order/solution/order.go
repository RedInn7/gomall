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
	if req.Quantity <= 0 {
		return Order{}, ErrInvalidQuantity
	}
	if req.ProductID != product.ID {
		return Order{}, ErrProductMismatch
	}
	if addressOwnerID != authUserID {
		return Order{}, ErrAddressNotOwned
	}
	return Order{
		UserID:     authUserID,
		ProductID:  product.ID,
		Quantity:   req.Quantity,
		AddressID:  req.AddressID,
		UnitCents:  product.PriceCents,
		MerchantID: product.MerchantID,
	}, nil
}
