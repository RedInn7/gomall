//go:build exercise

package inventorybuckets

import (
	"errors"
	"testing"
)

func TestInventoryLifecyclePreservesTotal(t *testing.T) {
	inv := Inventory{Available: 10}
	if err := inv.Reserve(4); err != nil {
		t.Fatal(err)
	}
	if inv != (Inventory{Available: 6, Reserved: 4}) {
		t.Fatalf("after reserve: %+v", inv)
	}
	if err := inv.Commit(3); err != nil {
		t.Fatal(err)
	}
	if inv != (Inventory{Available: 6, Reserved: 1, Sold: 3}) {
		t.Fatalf("after commit: %+v", inv)
	}
	if err := inv.Release(1); err != nil {
		t.Fatal(err)
	}
	if inv != (Inventory{Available: 7, Sold: 3}) {
		t.Fatalf("after release: %+v", inv)
	}
	if got := inv.Available + inv.Reserved + inv.Sold; got != 10 {
		t.Fatalf("total = %d, want 10", got)
	}
}

func TestFailedOperationDoesNotMutateInventory(t *testing.T) {
	tests := []struct {
		name    string
		run     func(*Inventory) error
		wantErr error
	}{
		{"reserve too much", func(i *Inventory) error { return i.Reserve(6) }, ErrInsufficientStock},
		{"commit too much", func(i *Inventory) error { return i.Commit(3) }, ErrInsufficientReserve},
		{"release zero", func(i *Inventory) error { return i.Release(0) }, ErrInvalidQuantity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := Inventory{Available: 5, Reserved: 2, Sold: 1}
			before := inv
			if err := tt.run(&inv); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if inv != before {
				t.Fatalf("failed operation changed inventory: before=%+v after=%+v", before, inv)
			}
		})
	}
}
