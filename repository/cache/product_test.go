package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type productStub struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

func TestProduct_NullCacheBlocksPenetration(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	const id = 7001

	// 未写入任何标记时应为 miss。
	if err := GetProductDetail(ctx, id, &productStub{}); !errors.Is(err, ErrProductCacheMiss) {
		t.Fatalf("want ErrProductCacheMiss, got %v", err)
	}

	// 写空值标记后应命中 not found，而非 miss。
	if err := SetProductNotFound(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := GetProductDetail(ctx, id, &productStub{}); !errors.Is(err, ErrProductNotFound) {
		t.Fatalf("want ErrProductNotFound, got %v", err)
	}

	// 空值标记的 TTL 应为短 TTL（约 ProductNullTTL），不能用正常详情 TTL。
	ttl, err := RedisClient.TTL(ctx, ProductDetailKey(id)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > ProductNullTTL {
		t.Fatalf("null cache ttl out of range: got %v, want (0, %v]", ttl, ProductNullTTL)
	}
}

func TestProduct_DetailNotMisreadAsNull(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	const id = 7002

	want := &productStub{ID: id, Name: "soap"}
	if err := SetProductDetail(ctx, id, want); err != nil {
		t.Fatal(err)
	}
	got := &productStub{}
	if err := GetProductDetail(ctx, id, got); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", got, want)
	}
}

func TestProduct_DetailTTLHasJitter(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// TTL 必须落在 [ProductDetailTTL, ProductDetailTTL+ProductTTLJitter] 区间内，
	// 且多次写入存在差异（抖动生效）。
	const samples = 24
	seen := make(map[time.Duration]struct{})
	for i := 0; i < samples; i++ {
		id := uint(7100 + i)
		if err := SetProductDetail(ctx, id, &productStub{ID: id}); err != nil {
			t.Fatal(err)
		}
		ttl, err := RedisClient.TTL(ctx, ProductDetailKey(id)).Result()
		if err != nil {
			t.Fatal(err)
		}
		// 留 2s 余量给 Redis 取整与往返耗时。
		lo := ProductDetailTTL - 2*time.Second
		hi := ProductDetailTTL + ProductTTLJitter + time.Second
		if ttl < lo || ttl > hi {
			t.Fatalf("ttl out of jittered range: got %v, want [%v, %v]", ttl, lo, hi)
		}
		seen[ttl.Truncate(time.Second)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("expected TTL jitter to produce varied expirations, got %d distinct values", len(seen))
	}
}

func TestProduct_LoadOnceMergesConcurrentLoads(t *testing.T) {
	const id = 7200
	const goroutines = 300

	var calls int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = LoadProductOnce(id, func() (interface{}, error) {
				atomic.AddInt64(&calls, 1)
				time.Sleep(20 * time.Millisecond) // 拉长回源窗口，逼并发落入同一飞行
				return &productStub{ID: id}, nil
			})
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != 1 {
		t.Fatalf("singleflight should collapse concurrent loads to 1, got %d", got)
	}
}
