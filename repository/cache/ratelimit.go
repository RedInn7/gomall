package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// 滑动窗口限流脚本
// KEYS[1]  = 限流 key（如 "rl:skill:user:42"）
// ARGV[1]  = 窗口大小（毫秒）
// ARGV[2]  = 阈值
// ARGV[3]  = 当前时间戳（毫秒）
// ARGV[4]  = 请求唯一 id（用 timestamp+rand）
//
// 返回 [allowed, count]，allowed=1 表示通过，0 表示限流
var slidingWindowScript = redis.NewScript(`
local key       = KEYS[1]
local window_ms = tonumber(ARGV[1])
local limit     = tonumber(ARGV[2])
local now_ms    = tonumber(ARGV[3])
local member    = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, 0, now_ms - window_ms)
local count = redis.call('ZCARD', key)
if count >= limit then
    return {0, count}
end

redis.call('ZADD', key, now_ms, member)
redis.call('PEXPIRE', key, window_ms)
return {1, count + 1}
`)

func RateLimitKey(scope, identifier string) string {
	return fmt.Sprintf("rl:%s:%s", scope, identifier)
}

// SlidingWindowAllow 判断是否在窗口内放行。
//   windowMS: 窗口大小毫秒
//   limit:    窗口内最大请求数
//   nowMS:    当前时间毫秒
//   member:   唯一请求 id，避免同毫秒冲突
func SlidingWindowAllow(ctx context.Context, key string, windowMS, limit, nowMS int64, member string) (bool, int64, error) {
	res, err := slidingWindowScript.Run(ctx, RedisClient, []string{key}, windowMS, limit, nowMS, member).Result()
	if err != nil {
		return false, 0, err
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) != 2 {
		return false, 0, fmt.Errorf("sliding window lua 返回值异常: %v", res)
	}
	allowed, _ := arr[0].(int64)
	count, _ := arr[1].(int64)
	return allowed == 1, count, nil
}
