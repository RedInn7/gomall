//go:build exercise

package atomicreservation

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func assertSnapshot(t *testing.T, inv *Inventory, available, reserved, sold int) Snapshot {
	t.Helper()
	got := inv.Snapshot()
	if got.Available != available || got.Reserved != reserved || got.Sold != sold {
		t.Fatalf("snapshot = %+v, want available=%d reserved=%d sold=%d", got, available, reserved, sold)
	}
	if got.Available < 0 || got.Reserved < 0 || got.Sold < 0 {
		t.Fatalf("inventory bucket became negative: %+v", got)
	}
	if got.Available+got.Reserved+got.Sold != available+reserved+sold {
		t.Fatalf("inventory total changed: %+v", got)
	}
	return got
}

func runTogether(n int, fn func(int) error) []error {
	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for index := 0; index < n; index++ {
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = fn(index)
		}(index)
	}
	close(start)
	wg.Wait()
	return errs
}

func TestReserveMovesAvailableToReserved(t *testing.T) {
	inv := NewInventory(10)
	if err := inv.Reserve("order-1", 4); err != nil {
		t.Fatal(err)
	}
	got := assertSnapshot(t, inv, 6, 4, 0)
	if got.Reservations["order-1"] != (Reservation{Quantity: 4, Status: StatusReserved}) {
		t.Fatalf("reservation = %+v", got.Reservations["order-1"])
	}
}

func TestCommitMovesReservedToSold(t *testing.T) {
	inv := NewInventory(8)
	if err := inv.Reserve("paid", 3); err != nil {
		t.Fatal(err)
	}
	if err := inv.Commit("paid"); err != nil {
		t.Fatal(err)
	}
	got := assertSnapshot(t, inv, 5, 0, 3)
	if got.Reservations["paid"].Status != StatusCommitted {
		t.Fatalf("reservation = %+v", got.Reservations["paid"])
	}
}

func TestReleaseReturnsReservedToAvailable(t *testing.T) {
	inv := NewInventory(8)
	if err := inv.Reserve("cancelled", 3); err != nil {
		t.Fatal(err)
	}
	if err := inv.Release("cancelled"); err != nil {
		t.Fatal(err)
	}
	got := assertSnapshot(t, inv, 8, 0, 0)
	if got.Reservations["cancelled"].Status != StatusReleased {
		t.Fatalf("reservation = %+v", got.Reservations["cancelled"])
	}
}

func TestInvalidReserveInputsDoNotWrite(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		qty  int
	}{
		{"empty id", "", 1},
		{"zero quantity", "zero", 0},
		{"negative quantity", "negative", -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			inv := NewInventory(5)
			if err := inv.Reserve(test.id, test.qty); !errors.Is(err, ErrInvalidQuantity) {
				t.Fatalf("error = %v, want ErrInvalidQuantity", err)
			}
			got := assertSnapshot(t, inv, 5, 0, 0)
			if len(got.Reservations) != 0 {
				t.Fatalf("failed reserve wrote a record: %+v", got.Reservations)
			}
		})
	}
}

func TestInsufficientStockDoesNotWrite(t *testing.T) {
	inv := NewInventory(2)
	if err := inv.Reserve("too-many", 3); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("error = %v, want ErrInsufficientStock", err)
	}
	got := assertSnapshot(t, inv, 2, 0, 0)
	if len(got.Reservations) != 0 {
		t.Fatalf("failed reserve wrote a record: %+v", got.Reservations)
	}
}

func TestReserveRetryIsIdempotent(t *testing.T) {
	inv := NewInventory(5)
	for attempt := 0; attempt < 5; attempt++ {
		if err := inv.Reserve("same-request", 2); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	got := assertSnapshot(t, inv, 3, 2, 0)
	if len(got.Reservations) != 1 {
		t.Fatalf("reservations = %+v", got.Reservations)
	}
}

func TestReserveRetryWithDifferentQuantityConflicts(t *testing.T) {
	inv := NewInventory(5)
	if err := inv.Reserve("same-request", 2); err != nil {
		t.Fatal(err)
	}
	if err := inv.Reserve("same-request", 3); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("error = %v, want ErrReservationConflict", err)
	}
	assertSnapshot(t, inv, 3, 2, 0)
}

func TestCommitRetryIsIdempotent(t *testing.T) {
	inv := NewInventory(5)
	if err := inv.Reserve("paid", 2); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if err := inv.Commit("paid"); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	assertSnapshot(t, inv, 3, 0, 2)
}

func TestReleaseRetryIsIdempotent(t *testing.T) {
	inv := NewInventory(5)
	if err := inv.Reserve("cancelled", 2); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if err := inv.Release("cancelled"); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	assertSnapshot(t, inv, 5, 0, 0)
}

func TestOppositeTerminalOperationIsRejected(t *testing.T) {
	t.Run("release after commit", func(t *testing.T) {
		inv := NewInventory(5)
		_ = inv.Reserve("paid", 2)
		_ = inv.Commit("paid")
		if err := inv.Release("paid"); !errors.Is(err, ErrAlreadyFinalized) {
			t.Fatalf("error = %v, want ErrAlreadyFinalized", err)
		}
		assertSnapshot(t, inv, 3, 0, 2)
	})
	t.Run("commit after release", func(t *testing.T) {
		inv := NewInventory(5)
		_ = inv.Reserve("cancelled", 2)
		_ = inv.Release("cancelled")
		if err := inv.Commit("cancelled"); !errors.Is(err, ErrAlreadyFinalized) {
			t.Fatalf("error = %v, want ErrAlreadyFinalized", err)
		}
		assertSnapshot(t, inv, 5, 0, 0)
	})
}

func TestUnknownCommitAndReleaseDoNotWrite(t *testing.T) {
	inv := NewInventory(5)
	if err := inv.Commit("missing"); !errors.Is(err, ErrUnknownReservation) {
		t.Fatalf("commit error = %v", err)
	}
	if err := inv.Release("missing"); !errors.Is(err, ErrUnknownReservation) {
		t.Fatalf("release error = %v", err)
	}
	got := assertSnapshot(t, inv, 5, 0, 0)
	if len(got.Reservations) != 0 {
		t.Fatalf("unknown operations wrote records: %+v", got.Reservations)
	}
}

func TestConcurrentBuyersCompeteForLastItem(t *testing.T) {
	const buyers = 64
	inv := NewInventory(1)
	var successes atomic.Int32
	for index, err := range runTogether(buyers, func(index int) error {
		err := inv.Reserve(fmt.Sprintf("buyer-%d", index), 1)
		if err == nil {
			successes.Add(1)
		}
		return err
	}) {
		if err != nil && !errors.Is(err, ErrInsufficientStock) {
			t.Fatalf("buyer %d: unexpected error %v", index, err)
		}
	}
	if successes.Load() != 1 {
		t.Fatalf("successful buyers = %d, want 1", successes.Load())
	}
	got := assertSnapshot(t, inv, 0, 1, 0)
	if len(got.Reservations) != 1 {
		t.Fatalf("reservations = %d, want 1", len(got.Reservations))
	}
}

func TestConcurrentSameReservationOnlyDeductsOnce(t *testing.T) {
	inv := NewInventory(10)
	for index, err := range runTogether(64, func(int) error {
		return inv.Reserve("same-request", 4)
	}) {
		if err != nil {
			t.Fatalf("call %d: %v", index, err)
		}
	}
	assertSnapshot(t, inv, 6, 4, 0)
}

func TestConcurrentCommitRetriesOnlySellOnce(t *testing.T) {
	inv := NewInventory(10)
	if err := inv.Reserve("paid", 4); err != nil {
		t.Fatal(err)
	}
	for index, err := range runTogether(64, func(int) error { return inv.Commit("paid") }) {
		if err != nil {
			t.Fatalf("call %d: %v", index, err)
		}
	}
	assertSnapshot(t, inv, 6, 0, 4)
}

func TestConcurrentReleaseRetriesOnlyReturnOnce(t *testing.T) {
	inv := NewInventory(10)
	if err := inv.Reserve("cancelled", 4); err != nil {
		t.Fatal(err)
	}
	for index, err := range runTogether(64, func(int) error { return inv.Release("cancelled") }) {
		if err != nil {
			t.Fatalf("call %d: %v", index, err)
		}
	}
	assertSnapshot(t, inv, 10, 0, 0)
}

func TestMixedLifecyclePreservesInventoryTotal(t *testing.T) {
	inv := NewInventory(12)
	for _, request := range []struct {
		id  string
		qty int
	}{{"a", 2}, {"b", 3}, {"c", 4}} {
		if err := inv.Reserve(request.id, request.qty); err != nil {
			t.Fatal(err)
		}
	}
	if err := inv.Commit("a"); err != nil {
		t.Fatal(err)
	}
	if err := inv.Release("b"); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(t, inv, 6, 4, 2)
}

func TestSnapshotReturnsReservationCopy(t *testing.T) {
	inv := NewInventory(3)
	if err := inv.Reserve("one", 1); err != nil {
		t.Fatal(err)
	}
	first := inv.Snapshot()
	first.Reservations["one"] = Reservation{Quantity: 99, Status: StatusReleased}
	second := inv.Snapshot()
	if second.Reservations["one"] != (Reservation{Quantity: 1, Status: StatusReserved}) {
		t.Fatalf("snapshot leaked mutable state: %+v", second.Reservations)
	}
}
