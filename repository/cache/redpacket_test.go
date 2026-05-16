package cache

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRedPacket_SplitSum(t *testing.T) {
	cases := []struct {
		total int64
		count int
	}{
		{10000, 100},
		{200, 100},
		{1, 1},
		{500, 1},
		{100, 100},
	}
	for _, c := range cases {
		amts, err := SplitRedPacket(c.total, c.count)
		if err != nil {
			t.Fatalf("split %d/%d: %v", c.total, c.count, err)
		}
		if len(amts) != c.count {
			t.Fatalf("split %d/%d: want %d parts, got %d", c.total, c.count, c.count, len(amts))
		}
		var sum int64
		for _, a := range amts {
			if a < 1 {
				t.Fatalf("split %d/%d: part < 1 cent: %d", c.total, c.count, a)
			}
			sum += a
		}
		if sum != c.total {
			t.Fatalf("split %d/%d: sum mismatch want=%d got=%d", c.total, c.count, c.total, sum)
		}
	}

	if _, err := SplitRedPacket(50, 100); err == nil {
		t.Fatal("expected error when total < count")
	}
	if _, err := SplitRedPacket(100, 0); err == nil {
		t.Fatal("expected error when count == 0")
	}
}

func TestRedPacket_LuaAtomicClaim(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	const id uint = 7001
	const total int64 = 10000
	const count = 100
	const goroutines = 1000

	amts, err := SplitRedPacket(total, count)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareRedPacket(ctx, id, amts, time.Minute); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	var success int64
	var drained int64
	var taken int64
	var wg sync.WaitGroup
	for i := 1; i <= goroutines; i++ {
		wg.Add(1)
		uid := uint(i)
		go func() {
			defer wg.Done()
			amt, err := ClaimRedPacket(ctx, id, uid, time.Minute)
			switch err {
			case nil:
				if amt < 1 {
					t.Errorf("got amount < 1: %d", amt)
				}
				atomic.AddInt64(&success, 1)
			case ErrRedPacketDrained:
				atomic.AddInt64(&drained, 1)
			case ErrRedPacketAlreadyTaken:
				atomic.AddInt64(&taken, 1)
			default:
				t.Errorf("claim err: %v", err)
			}
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&success) != int64(count) {
		t.Fatalf("expect exactly %d successful claims, got %d (drained=%d taken=%d)",
			count, success, drained, taken)
	}
	remain, err := GetRedPacketRemainingCount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if remain != 0 {
		t.Fatalf("list should be empty after drained, got %d", remain)
	}
}

func TestRedPacket_NoDoubleClaimByUser(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	const id uint = 7002
	amts, _ := SplitRedPacket(1000, 10)
	if err := PrepareRedPacket(ctx, id, amts, time.Minute); err != nil {
		t.Fatal(err)
	}

	const userID uint = 99
	amt1, err := ClaimRedPacket(ctx, id, userID, time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if amt1 < 1 {
		t.Fatalf("amt1 should be >= 1, got %d", amt1)
	}
	_, err = ClaimRedPacket(ctx, id, userID, time.Minute)
	if err != ErrRedPacketAlreadyTaken {
		t.Fatalf("expect ErrRedPacketAlreadyTaken on second claim, got %v", err)
	}
}

func TestRedPacket_ReleaseLeft(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	const id uint = 7003
	amts, _ := SplitRedPacket(500, 5)
	if err := PrepareRedPacket(ctx, id, amts, time.Minute); err != nil {
		t.Fatal(err)
	}
	// 抢 2 份
	_, _ = ClaimRedPacket(ctx, id, 1, time.Minute)
	_, _ = ClaimRedPacket(ctx, id, 2, time.Minute)

	left, err := ReleaseRedPacketLeft(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if left <= 0 {
		t.Fatalf("expect leftover > 0, got %d", left)
	}
	remain, _ := GetRedPacketRemainingCount(ctx, id)
	if remain != 0 {
		t.Fatalf("list should be drained after release, got %d", remain)
	}
}

func TestRedPacket_RollbackPutsAmountBack(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	const id uint = 7004
	amts, _ := SplitRedPacket(300, 3)
	if err := PrepareRedPacket(ctx, id, amts, time.Minute); err != nil {
		t.Fatal(err)
	}
	const uid uint = 42
	amt, err := ClaimRedPacket(ctx, id, uid, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	remainBefore, _ := GetRedPacketRemainingCount(ctx, id)

	if err := RollbackRedPacketClaim(ctx, id, uid, amt); err != nil {
		t.Fatal(err)
	}
	remainAfter, _ := GetRedPacketRemainingCount(ctx, id)
	if remainAfter != remainBefore+1 {
		t.Fatalf("rollback should put one back, got before=%d after=%d", remainBefore, remainAfter)
	}
	// 回滚后同用户可再次领取
	amt2, err := ClaimRedPacket(ctx, id, uid, time.Minute)
	if err != nil {
		t.Fatalf("after rollback claim: %v", err)
	}
	if amt2 < 1 {
		t.Fatalf("amt2 should be >= 1, got %d", amt2)
	}
}
