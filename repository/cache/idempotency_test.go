package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// 使用真实 Redis（默认 127.0.0.1:6379, DB 15）。Redis 不可达时整组用例 skip。
func setupTestRedis(t *testing.T) func() {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skip("Redis 127.0.0.1:6379 不可用，跳过：", err)
	}
	prev := RedisClient
	RedisClient = c
	return func() {
		c.FlushDB(context.Background())
		c.Close()
		RedisClient = prev
	}
}

func TestIdempotency_FullStateMachine(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	key := fmt.Sprintf("idemp:1:%d", time.Now().UnixNano())

	// 没颁发过 token → state=0
	state, _, err := AcquireIdempotencyLock(ctx, key)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if state != 0 {
		t.Fatalf("missing token should return state=0, got %d", state)
	}

	// 颁发
	if err := IssueIdempotencyToken(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := SetTokenTTL(ctx, key); err != nil {
		t.Fatal(err)
	}

	// 第一次拿到锁
	state, _, err = AcquireIdempotencyLock(ctx, key)
	if err != nil || state != 1 {
		t.Fatalf("expect state=1 (acquired), got state=%d err=%v", state, err)
	}

	// 第二次进入：上次还没 commit → processing
	state, _, err = AcquireIdempotencyLock(ctx, key)
	if err != nil || state != 3 {
		t.Fatalf("expect state=3 (processing), got state=%d err=%v", state, err)
	}

	// commit 结果
	if err := CommitIdempotencyResult(ctx, key, `{"ok":1}`); err != nil {
		t.Fatal(err)
	}

	// 第三次进入：返回缓存
	state, cached, err := AcquireIdempotencyLock(ctx, key)
	if err != nil || state != 2 {
		t.Fatalf("expect state=2 (done), got state=%d err=%v", state, err)
	}
	if cached != `{"ok":1}` {
		t.Fatalf("cached body mismatch: %q", cached)
	}
}

func TestIdempotency_ReleaseAllowsRetry(t *testing.T) {
	cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()
	key := fmt.Sprintf("idemp:2:%d", time.Now().UnixNano())

	_ = IssueIdempotencyToken(ctx, key)
	_ = SetTokenTTL(ctx, key)
	state, _, _ := AcquireIdempotencyLock(ctx, key)
	if state != 1 {
		t.Fatalf("acquire failed: state=%d", state)
	}
	if err := ReleaseIdempotencyLock(ctx, key); err != nil {
		t.Fatal(err)
	}
	// 释放后能再次拿锁
	state, _, _ = AcquireIdempotencyLock(ctx, key)
	if state != 1 {
		t.Fatalf("expect re-acquire after release, got state=%d", state)
	}
}

var _ = errors.New
