package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestInventory_ReserveCommitRelease(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	const pid = 9001

	if err := InitStock(ctx, pid, 50); err != nil {
		t.Fatal(err)
	}
	if err := ReserveStock(ctx, pid, 30); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	a, r, _ := GetStockSnapshot(ctx, pid)
	if a != 20 || r != 30 {
		t.Fatalf("after reserve want a=20 r=30, got a=%d r=%d", a, r)
	}
	if err := CommitReservation(ctx, pid, 30); err != nil {
		t.Fatalf("commit: %v", err)
	}
	a, r, _ = GetStockSnapshot(ctx, pid)
	if a != 20 || r != 0 {
		t.Fatalf("after commit want a=20 r=0, got a=%d r=%d", a, r)
	}
}

func TestInventory_ReleasePutsBack(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	const pid = 9002

	InitStock(ctx, pid, 10)
	ReserveStock(ctx, pid, 7)
	if err := ReleaseReservation(ctx, pid, 7); err != nil {
		t.Fatal(err)
	}
	a, r, _ := GetStockSnapshot(ctx, pid)
	if a != 10 || r != 0 {
		t.Fatalf("release should fully restore, got a=%d r=%d", a, r)
	}
}

func TestInventory_NoOversellUnderConcurrency(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	const pid = 9003
	const initial int64 = 100
	const goroutines = 500

	InitStock(ctx, pid, initial)

	var success int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ReserveStock(ctx, pid, 1); err == nil {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&success) != initial {
		t.Fatalf("oversell: want exactly %d successful reservations, got %d", initial, success)
	}
	a, r, _ := GetStockSnapshot(ctx, pid)
	if a != 0 || r != initial {
		t.Fatalf("post-burst want a=0 r=%d, got a=%d r=%d", initial, a, r)
	}
}
