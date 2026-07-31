//go:build exercise

package authoritativeorder

import (
	"errors"
	"testing"
)

func TestBuildOrderIgnoresForgedFacts(t *testing.T) {
	req := Request{
		UserID: 999, ProductID: 7, Quantity: 2, AddressID: 31,
		PriceCents: 1, MerchantID: 888,
	}
	product := Product{ID: 7, PriceCents: 2599, MerchantID: 42}

	got, err := BuildOrder(req, 12, product, 12)
	if err != nil {
		t.Fatalf("BuildOrder() error = %v", err)
	}
	want := Order{
		UserID: 12, ProductID: 7, Quantity: 2, AddressID: 31,
		UnitCents: 2599, MerchantID: 42,
	}
	if got != want {
		t.Fatalf("order = %+v, want %+v", got, want)
	}
}

func TestBuildOrderValidatesBusinessBoundary(t *testing.T) {
	tests := []struct {
		name         string
		req          Request
		product      Product
		addressOwner uint
		wantErr      error
	}{
		{"zero quantity", Request{ProductID: 7, Quantity: 0}, Product{ID: 7}, 12, ErrInvalidQuantity},
		{"product mismatch", Request{ProductID: 7, Quantity: 1}, Product{ID: 8}, 12, ErrProductMismatch},
		{"foreign address", Request{ProductID: 7, Quantity: 1}, Product{ID: 7}, 13, ErrAddressNotOwned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildOrder(tt.req, 12, tt.product, tt.addressOwner)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
