package cache

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSlidingWindow_RejectsOverLimit(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	key := RateLimitKey("test", "user-1")

	const windowMS = 1000
	const limit = 5

	allowed := 0
	for i := 0; i < 20; i++ {
		now := time.Now().UnixMilli()
		ok, _, err := SlidingWindowAllow(ctx, key, windowMS, limit, now, fmt.Sprintf("%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			allowed++
		}
	}
	if allowed != limit {
		t.Fatalf("burst should let exactly %d through, got %d", limit, allowed)
	}
}

func TestSlidingWindow_WindowSlides(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	key := RateLimitKey("test", "user-2")

	const windowMS = 200
	const limit = 2

	allow := func(member string) bool {
		ok, _, _ := SlidingWindowAllow(ctx, key, windowMS, limit, time.Now().UnixMilli(), member)
		return ok
	}

	if !allow("a") || !allow("b") {
		t.Fatal("first burst should fit limit")
	}
	if allow("c") {
		t.Fatal("third within window should be rejected")
	}
	time.Sleep(250 * time.Millisecond)
	if !allow("d") {
		t.Fatal("after window slide should allow again")
	}
}
