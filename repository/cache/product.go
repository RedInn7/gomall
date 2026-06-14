package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	ProductDetailTTL     = 10 * time.Minute
	ProductLockTTL       = 3 * time.Second
	ProductDelayInterval = 500 * time.Millisecond

	// ProductTTLJitter 详情缓存 TTL 的最大正向抖动，写缓存时在固定 TTL 上叠加
	// [0, ProductTTLJitter) 的随机偏移，打散同批写入的过期时刻，避免缓存雪崩。
	ProductTTLJitter = 90 * time.Second

	// ProductNullTTL 空值标记的 TTL，远小于正常详情 TTL，
	// 既能挡住对不存在商品的穿透打库，又能在商品后续上架时较快自愈。
	ProductNullTTL = 60 * time.Second

	// productNullValue 空值标记的占位内容，与正常 JSON 对象首字符区分。
	productNullValue = "\x00null"
)

func ProductDetailKey(id uint) string {
	return fmt.Sprintf("product:detail:%d", id)
}

func ProductLockKey(id uint) string {
	return fmt.Sprintf("product:lock:%d", id)
}

var (
	ErrProductCacheMiss = errors.New("product cache miss")
	// ErrProductNotFound 命中空值标记，表示该商品确认不存在，调用方应直接按 not found 处理，不再回源。
	ErrProductNotFound = errors.New("product not found")
)

// productLoadGroup 合并同一进程内对同一 key 的并发回源，叠加在跨进程 Redis 互斥锁之上，
// 进一步消除缓存惊群（同实例大量 goroutine 同时打库）。
var productLoadGroup singleflight.Group

// withProductTTLJitter 在固定 TTL 上叠加 [0, ProductTTLJitter) 的随机抖动。
func withProductTTLJitter(base time.Duration) time.Duration {
	if ProductTTLJitter <= 0 {
		return base
	}
	return base + time.Duration(rand.Int63n(int64(ProductTTLJitter)))
}

// GetProductDetail 读缓存。未命中返回 ErrProductCacheMiss；命中空值标记返回 ErrProductNotFound。
func GetProductDetail(ctx context.Context, id uint, dst interface{}) error {
	raw, err := RedisClient.Get(ctx, ProductDetailKey(id)).Result()
	if err == redis.Nil {
		return ErrProductCacheMiss
	}
	if err != nil {
		return err
	}
	if raw == productNullValue {
		return ErrProductNotFound
	}
	return json.Unmarshal([]byte(raw), dst)
}

// SetProductDetail 写缓存，TTL 带随机抖动。
func SetProductDetail(ctx context.Context, id uint, val interface{}) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return RedisClient.Set(ctx, ProductDetailKey(id), b, withProductTTLJitter(ProductDetailTTL)).Err()
}

// SetProductNotFound 为不存在的商品写一个短 TTL 的空值标记，挡住缓存穿透。
func SetProductNotFound(ctx context.Context, id uint) error {
	return RedisClient.Set(ctx, ProductDetailKey(id), productNullValue, ProductNullTTL).Err()
}

// DelProductDetail 删缓存
func DelProductDetail(ctx context.Context, id uint) error {
	return RedisClient.Del(ctx, ProductDetailKey(id)).Err()
}

// LoadProductOnce 用进程内 singleflight 合并同 id 的并发回源调用。
// load 只会被其中一个 goroutine 实际执行，其余等待并共享同一结果。
func LoadProductOnce(id uint, load func() (interface{}, error)) (interface{}, error) {
	v, err, _ := productLoadGroup.Do(ProductDetailKey(id), func() (interface{}, error) {
		return load()
	})
	return v, err
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
