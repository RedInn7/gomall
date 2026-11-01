package cache

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSplitRedPacketInvariants(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	cases := [][2]int64{{1, 1}, {5, 5}, {6, 5}, {20000, 1}, {20000, 1000}, {1000, 1000}, {1001, 1000}}
	for i := 0; i < 2000; i++ {
		count := r.Int63n(1000) + 1
		cases = append(cases, [2]int64{count + r.Int63n(20000-count+1), count})
	}
	for _, c := range cases {
		total, count := c[0], int(c[1])
		out, err := SplitRedPacket(total, count)
		if err != nil {
			t.Fatalf("split(%d,%d): %v", total, count, err)
		}
		if len(out) != count {
			t.Fatalf("split(%d,%d) len=%d", total, count, len(out))
		}
		var sum int64
		for _, a := range out {
			if a < 1 {
				t.Fatalf("split(%d,%d) has share %d < 1", total, count, a)
			}
			sum += a
		}
		if sum != total {
			t.Fatalf("split(%d,%d) sum=%d", total, count, sum)
		}
	}
	for _, bad := range [][2]int64{{0, 1}, {4, 5}, {10, 0}, {10, -1}} {
		if _, err := SplitRedPacket(bad[0], int(bad[1])); !errors.Is(err, ErrRedPacketSplitInvalid) {
			t.Fatalf("split(%d,%d) want ErrRedPacketSplitInvalid got %v", bad[0], bad[1], err)
		}
	}
}

func useMiniredis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	prev := RedisClient
	RedisClient = redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 64})
	t.Cleanup(func() {
		_ = RedisClient.Close()
		RedisClient = prev
	})
	return mr
}

// 200 个 goroutine 同时抢 50 份，每个用户还重复点 2 次：恰好 50 人抢到、总额守恒、同一用户只成一次。
func TestClaimRedPacketLuaConcurrent(t *testing.T) {
	useMiniredis(t)
	ctx := context.Background()
	amounts, err := SplitRedPacket(5000, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareRedPacket(ctx, 1, amounts, time.Hour); err != nil {
		t.Fatal(err)
	}

	var got, sum, dup, drained int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for uid := uint(1); uid <= 100; uid++ {
		for k := 0; k < 2; k++ {
			wg.Add(1)
			go func(uid uint) {
				defer wg.Done()
				<-start
				amt, err := ClaimRedPacket(ctx, 1, uid, time.Hour)
				switch {
				case err == nil:
					atomic.AddInt64(&got, 1)
					atomic.AddInt64(&sum, amt)
				case errors.Is(err, ErrRedPacketAlreadyTaken):
					atomic.AddInt64(&dup, 1)
				case errors.Is(err, ErrRedPacketDrained):
					atomic.AddInt64(&drained, 1)
				default:
					t.Errorf("claim uid=%d: %v", uid, err)
				}
			}(uid)
		}
	}
	close(start)
	wg.Wait()

	if got != 50 || sum != 5000 {
		t.Fatalf("claimed %d shares sum %d, want 50 / 5000", got, sum)
	}
	marks, err := ListRedPacketClaimed(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 50 {
		t.Fatalf("claimed marks %d want 50", len(marks))
	}
	var markSum int64
	for _, m := range marks {
		markSum += m.Amount
		if time.Since(m.At) > time.Minute {
			t.Fatalf("mark time not recorded: %v", m.At)
		}
	}
	if markSum != 5000 {
		t.Fatalf("mark sum %d want 5000", markSum)
	}
	if exists, _ := RedPacketPoolExists(ctx, 1); exists {
		t.Fatal("pool should be drained")
	}
}

func TestRollbackRedPacketClaimRestoresShareAndMark(t *testing.T) {
	useMiniredis(t)
	ctx := context.Background()
	if err := PrepareRedPacket(ctx, 2, []int64{30, 70}, time.Hour); err != nil {
		t.Fatal(err)
	}
	amt, err := ClaimRedPacket(ctx, 2, 9, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := RollbackRedPacketClaim(ctx, 2, 9, amt); err != nil {
		t.Fatal(err)
	}
	// 撤掉标记后本人能再抢，且抢到的就是放回头部的同一份
	again, err := ClaimRedPacket(ctx, 2, 9, time.Hour)
	if err != nil || again != amt {
		t.Fatalf("reclaim got %d,%v want %d", again, err, amt)
	}
	if left, _ := ReleaseRedPacketLeft(ctx, 2); left != 100-amt {
		t.Fatalf("left %d want %d", left, 100-amt)
	}
}

func TestListRedPacketClaimedLegacyFormat(t *testing.T) {
	mr := useMiniredis(t)
	mr.HSet(RedPacketClaimedKey(3), "7", "88")
	marks, err := ListRedPacketClaimed(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if m := marks[7]; m.Amount != 88 || !m.At.IsZero() {
		t.Fatalf("legacy mark parsed as %+v", m)
	}
}

// Saga 回滚和对账补领并发抢同一个标记：每一轮恰好一方处理这一份——要么份额放回 LIST，
// 要么标记被对账接手（回滚让路），不会既放回又补领。
func TestRollbackAndRepairLockAreExclusive(t *testing.T) {
	useMiniredis(t)
	ctx := context.Background()
	var pushed, repaired int
	for i := uint(1); i <= 200; i++ {
		if err := PrepareRedPacket(ctx, i, []int64{10}, time.Hour); err != nil {
			t.Fatal(err)
		}
		if _, err := ClaimRedPacket(ctx, i, 9, time.Hour); err != nil {
			t.Fatal(err)
		}
		marks, err := ListRedPacketClaimed(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		var (
			wg     sync.WaitGroup
			rbErr  error
			locked bool
			lkErr  error
			start  = make(chan struct{})
		)
		wg.Add(2)
		go func() { defer wg.Done(); <-start; rbErr = RollbackRedPacketClaim(ctx, i, 9, 10) }()
		go func() { defer wg.Done(); <-start; locked, lkErr = LockRedPacketClaimForRepair(ctx, i, 9, marks[9].Raw) }()
		close(start)
		wg.Wait()
		if lkErr != nil || (rbErr != nil && !errors.Is(rbErr, ErrRedPacketClaimRepairing)) {
			t.Fatalf("round %d: rollback=%v lock=%v", i, rbErr, lkErr)
		}
		n, _ := RedisClient.LLen(ctx, RedPacketAmountsKey(i)).Result()
		if locked == (n == 1) {
			t.Fatalf("round %d: locked=%v pushed=%v, want exactly one", i, locked, n == 1)
		}
		if locked != errors.Is(rbErr, ErrRedPacketClaimRepairing) {
			t.Fatalf("round %d: locked=%v but rollback err=%v", i, locked, rbErr)
		}
		if locked {
			repaired++
		} else {
			pushed++
		}
	}
	t.Logf("pushed=%d repaired=%d", pushed, repaired)
}

// 对账只认读到的原标记：标记已被回滚撤掉、或撤掉后本人又重抢出新标记，都不能接手。
func TestRepairLockRequiresOriginalMark(t *testing.T) {
	useMiniredis(t)
	ctx := context.Background()
	if err := PrepareRedPacket(ctx, 5, []int64{10, 20}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimRedPacket(ctx, 5, 9, time.Hour); err != nil {
		t.Fatal(err)
	}
	marks, _ := ListRedPacketClaimed(ctx, 5)
	stale := marks[9].Raw
	_ = RollbackRedPacketClaim(ctx, 5, 9, marks[9].Amount)
	if ok, _ := LockRedPacketClaimForRepair(ctx, 5, 9, stale); ok {
		t.Fatal("lock must fail after rollback removed the mark")
	}
	RedisClient.HSet(ctx, RedPacketClaimedKey(5), "9", "20:1")
	if ok, _ := LockRedPacketClaimForRepair(ctx, 5, 9, stale); ok {
		t.Fatal("lock must fail on a different mark")
	}
	if ok, _ := LockRedPacketClaimForRepair(ctx, 5, 9, "20:1"); !ok {
		t.Fatal("lock should succeed on the current mark")
	}
	// 已在补领中：重入成功；解析仍拿到金额和时间
	if ok, _ := LockRedPacketClaimForRepair(ctx, 5, 9, "20:1:r"); !ok {
		t.Fatal("re-lock of repairing mark should succeed")
	}
	marks, _ = ListRedPacketClaimed(ctx, 5)
	if m := marks[9]; m.Amount != 20 || m.At.Unix() != 1 || m.Raw != "20:1:r" {
		t.Fatalf("repairing mark parsed as %+v", m)
	}
}
