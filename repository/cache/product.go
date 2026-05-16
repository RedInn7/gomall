package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	ProductDetailTTL     = 10 * time.Minute
	ProductLockTTL       = 3 * time.Second
	ProductDelayInterval = 500 * time.Millisecond
)

func ProductDetailKey(id uint) string {
	return fmt.Sprintf("product:detail:%d", id)
}

func ProductLockKey(id uint) string {
	return fmt.Sprintf("product:lock:%d", id)
}

var ErrProductCacheMiss = errors.New("product cache miss")

// GetProductDetail 读缓存。未命中返回 ErrProductCacheMiss
func GetProductDetail(ctx context.Context, id uint, dst interface{}) error {
	raw, err := RedisClient.Get(ctx, ProductDetailKey(id)).Result()
	if err == redis.Nil {
		return ErrProductCacheMiss
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), dst)
}

// SetProductDetail 写缓存
func SetProductDetail(ctx context.Context, id uint, val interface{}) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return RedisClient.Set(ctx, ProductDetailKey(id), b, ProductDetailTTL).Err()
}

// DelProductDetail 删缓存
func DelProductDetail(ctx context.Context, id uint) error {
	return RedisClient.Del(ctx, ProductDetailKey(id)).Err()
}

// TryProductLock 用 SETNX 抢回源锁，避免缓存击穿时多个请求同时回源
func TryProductLock(ctx context.Context, id uint) (bool, error) {
	return RedisClient.SetNX(ctx, ProductLockKey(id), "1", ProductLockTTL).Result()
}

func UnlockProduct(ctx context.Context, id uint) {
	RedisClient.Del(ctx, ProductLockKey(id))
}

// DoubleDeleteAsync 延迟双删：写库后已经做了第一次删除，这里在 interval 后再删一次，
// 用于覆盖"读旧值的并发请求把旧值塞回缓存"的窗口。
func DoubleDeleteAsync(id uint, interval time.Duration) {
	if interval <= 0 {
		interval = ProductDelayInterval
	}
	go func() {
		time.Sleep(interval)
		_ = RedisClient.Del(context.Background(), ProductDetailKey(id)).Err()
	}()
}
