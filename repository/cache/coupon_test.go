package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoupon_AtomicClaimNoOversell(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	const batchId = 9999
	const total = 100
	const concurrency = 500

	if err := InitCouponStock(ctx, batchId, total, time.Minute); err != nil {
		t.Fatal(err)
	}

	var success int64
	var wg sync.WaitGroup
	for i := 1; i <= concurrency; i++ {
		wg.Add(1)
		uid := uint(i)
		go func() {
			defer wg.Done()
			ok, err := ClaimCouponAtomic(ctx, uid, batchId, 1)
			if err != nil {
				return
			}
			if ok {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	wg.Wait()

	got := atomic.LoadInt64(&success)
	if got != total {
		t.Fatalf("oversell or undersell: want exactly %d successes, got %d", total, got)
	}
}

func TestCoupon_PerUserLimit(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	const batchId = 9998
	if err := InitCouponStock(ctx, batchId, 100, time.Minute); err != nil {
		t.Fatal(err)
	}
	// 同一用户连领 5 次，per-user=2 → 只应成功 2 次
	var ok int
	for i := 0; i < 5; i++ {
		got, err := ClaimCouponAtomic(ctx, 1, batchId, 2)
		if err != nil {
			continue
		}
		if got {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("expect per-user cap = 2 successes, got %d", ok)
	}
}
