package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/RedInn7/gomall/pkg/utils/log"
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
// 用 SET NX：仅当 key 不存在时才写空值标记。否则在"回源发现不存在"与
// "另一并发请求刚把真实详情写入缓存"竞争时，无条件 SET 会把真实值覆盖成空值，
// 最长 ProductNullTTL 内读到错误的 not found。
func SetProductNotFound(ctx context.Context, id uint) error {
	return RedisClient.SetNX(ctx, ProductDetailKey(id), productNullValue, ProductNullTTL).Err()
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

const (
	// doubleDeleteTimeout 第二次删除的独立超时，避免 Redis 故障时 goroutine 长期阻塞。
	doubleDeleteTimeout = 2 * time.Second
	// doubleDeleteMaxInflight 延迟删除在飞 goroutine 上限。Redis 慢/不可达时 goroutine
	// 会随 sleep 堆积，超过上限直接放弃本次延迟删，防止无界堆积拖垮进程。
	doubleDeleteMaxInflight = 1024
)

// doubleDeleteInflight 当前在飞的延迟删除数量。
var doubleDeleteInflight atomic.Int64

// DoubleDeleteAsync 延迟双删：写库后已经做了第一次删除，这里在 interval 后再删一次，
// 用于覆盖"读旧值的并发请求把旧值塞回缓存"的窗口。
// 第二次删除带独立超时 context 并记录错误；在飞数量超过上限时放弃本次延迟删，
// 避免 Redis 故障下裸 goroutine 无界堆积。
func DoubleDeleteAsync(id uint, interval time.Duration) {
	if interval <= 0 {
		interval = ProductDelayInterval
	}
	if doubleDeleteInflight.Add(1) > doubleDeleteMaxInflight {
		doubleDeleteInflight.Add(-1)
		if log.LogrusObj != nil {
			log.LogrusObj.Warnf("double delete dropped, inflight over limit: product=%d", id)
		}
		return
	}
	go func() {
		defer doubleDeleteInflight.Add(-1)
		time.Sleep(interval)
		ctx, cancel := context.WithTimeout(context.Background(), doubleDeleteTimeout)
		defer cancel()
		if err := RedisClient.Del(ctx, ProductDetailKey(id)).Err(); err != nil {
			if log.LogrusObj != nil {
				log.LogrusObj.Errorf("double delete failed: product=%d err=%v", id, err)
			}
		}
	}()
}
