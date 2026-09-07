//go:build exercise

package authoritativecheckout

import (
	"errors"
	"math"
)

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
	UserID, ProductID uint
	Num               int64
	AddressID         uint
	PriceCents        int64
	SellerID          uint
}
type Product struct {
	ID         uint
	PriceCents int64
	SellerID   uint
	OnSale     bool
}
type Checkout struct {
	UserID, ProductID        uint
	Num                      int64
	AddressID                uint
	UnitCents, SubtotalCents int64
	SellerID                 uint
}
type AddressBook interface {
	OwnerOf(addressID uint) (uint, error)
}
type Catalog interface {
	GetProduct(productID uint) (Product, error)
}

func PrepareCheckout(req Request, authUserID uint, addresses AddressBook, catalog Catalog) (Checkout, error) {
	if authUserID == 0 {
		return Checkout{}, ErrInvalidUser
	}
	if req.ProductID == 0 || req.AddressID == 0 {
		return Checkout{}, ErrInvalidProduct
	}
	if req.Num <= 0 {
		return Checkout{}, ErrInvalidQuantity
	}
	owner, err := addresses.OwnerOf(req.AddressID)
	if err != nil {
		return Checkout{}, err
	}
	if owner != authUserID {
		return Checkout{}, ErrAddressNotOwned
	}
	p, err := catalog.GetProduct(req.ProductID)
	if err != nil {
		return Checkout{}, err
	}
	if p.ID != req.ProductID || p.SellerID == 0 || !p.OnSale {
		return Checkout{}, ErrProductUnavailable
	}
	if p.PriceCents < 0 {
		return Checkout{}, ErrInvalidPrice
	}
	if p.PriceCents != 0 && req.Num > math.MaxInt64/p.PriceCents {
		return Checkout{}, ErrAmountOverflow
	}
	return Checkout{UserID: authUserID, ProductID: p.ID, Num: req.Num, AddressID: req.AddressID, UnitCents: p.PriceCents, SubtotalCents: p.PriceCents * req.Num, SellerID: p.SellerID}, nil
}
