//go:build exercise

package authoritativecheckout

import (
	"errors"
	"math"
	"testing"
)

var errDependency = errors.New("dependency failed")

type fakeAddress struct {
	owner uint
	err   error
	calls int
}

func (f *fakeAddress) OwnerOf(uint) (uint, error) { f.calls++; return f.owner, f.err }

type fakeCatalog struct {
	product Product
	err     error
	calls   int
}

func (f *fakeCatalog) GetProduct(uint) (Product, error) { f.calls++; return f.product, f.err }
func runCheckout(t *testing.T, req Request, user uint, a *fakeAddress, c *fakeCatalog, wantErr error) Checkout {
	t.Helper()
	got, err := PrepareCheckout(req, user, a, c)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v want=%v", err, wantErr)
	}
	return got
}
func TestPrepareCheckout(t *testing.T) {
	validReq := Request{UserID: 999, ProductID: 7, Num: 2, AddressID: 3, PriceCents: 1, SellerID: 888}
	validProduct := Product{ID: 7, PriceCents: 2500, SellerID: 42, OnSale: true}
	t.Run("uses authoritative facts", func(t *testing.T) {
		a := &fakeAddress{owner: 10}
		c := &fakeCatalog{product: validProduct}
		got := runCheckout(t, validReq, 10, a, c, nil)
		want := Checkout{UserID: 10, ProductID: 7, Num: 2, AddressID: 3, UnitCents: 2500, SubtotalCents: 5000, SellerID: 42}
		if got != want {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
	})
	cases := []struct {
		name                           string
		req                            Request
		user, owner                    uint
		product                        Product
		addrErr, errorWant, catalogErr error
		addrCalls, catalogCalls        int
	}{
		{"missing auth user", validReq, 0, 10, validProduct, nil, ErrInvalidUser, nil, 0, 0}, {"missing product id", Request{Num: 1, AddressID: 3}, 10, 10, validProduct, nil, ErrInvalidProduct, nil, 0, 0}, {"missing address id", Request{ProductID: 7, Num: 1}, 10, 10, validProduct, nil, ErrInvalidProduct, nil, 0, 0}, {"zero quantity", Request{ProductID: 7, Num: 0, AddressID: 3}, 10, 10, validProduct, nil, ErrInvalidQuantity, nil, 0, 0}, {"negative quantity", Request{ProductID: 7, Num: -1, AddressID: 3}, 10, 10, validProduct, nil, ErrInvalidQuantity, nil, 0, 0}, {"address dependency", validReq, 10, 10, validProduct, errDependency, errDependency, nil, 1, 0}, {"foreign address", validReq, 10, 11, validProduct, nil, ErrAddressNotOwned, nil, 1, 0}, {"catalog dependency", validReq, 10, 10, validProduct, nil, errDependency, errDependency, 1, 1}, {"product mismatch", validReq, 10, 10, Product{ID: 8, PriceCents: 1, SellerID: 2, OnSale: true}, nil, ErrProductUnavailable, nil, 1, 1}, {"off sale", validReq, 10, 10, Product{ID: 7, PriceCents: 1, SellerID: 2}, nil, ErrProductUnavailable, nil, 1, 1}, {"missing seller", validReq, 10, 10, Product{ID: 7, PriceCents: 1, OnSale: true}, nil, ErrProductUnavailable, nil, 1, 1}, {"negative price", validReq, 10, 10, Product{ID: 7, PriceCents: -1, SellerID: 2, OnSale: true}, nil, ErrInvalidPrice, nil, 1, 1}, {"amount overflow", Request{ProductID: 7, Num: 2, AddressID: 3}, 10, 10, Product{ID: 7, PriceCents: math.MaxInt64, SellerID: 2, OnSale: true}, nil, ErrAmountOverflow, nil, 1, 1}, {"zero price allowed", Request{ProductID: 7, Num: 2, AddressID: 3}, 10, 10, Product{ID: 7, SellerID: 2, OnSale: true}, nil, nil, nil, 1, 1}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a := &fakeAddress{owner: tt.owner, err: tt.addrErr}
			c := &fakeCatalog{product: tt.product, err: tt.catalogErr}
			runCheckout(t, tt.req, tt.user, a, c, tt.errorWant)
			if a.calls != tt.addrCalls || c.calls != tt.catalogCalls {
				t.Fatalf("calls address=%d catalog=%d", a.calls, c.calls)
			}
		})
	}
}
