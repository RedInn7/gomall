package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	IdempotencyStateInit       = "init"
	IdempotencyStateProcessing = "processing"
	IdempotencyStateDone       = "done"

	IdempotencyTokenTTL  = 5 * time.Minute
	IdempotencyResultTTL = 10 * time.Minute
)

var (
	ErrIdempotencyTokenMissing    = errors.New("idempotency token 不存在或已过期")
	ErrIdempotencyTokenInProgress = errors.New("请求正在处理中，请勿重复提交")
)

// acquireScript 原子地将 init → processing。
// KEYS[1]=token key
// 返回:
//   1 = 成功获取（init → processing）
//   2 = 已完成，返回缓存结果 (ARGV 中读 result 字段)
//   3 = 正在处理中
//   0 = 不存在
var acquireScript = redis.NewScript(`
local v = redis.call('HGET', KEYS[1], 'state')
if v == false or v == nil then
    return {0, ''}
end
if v == 'done' then
    local r = redis.call('HGET', KEYS[1], 'result')
    return {2, r or ''}
end
if v == 'processing' then
    return {3, ''}
end
if v == 'init' then
    redis.call('HSET', KEYS[1], 'state', 'processing')
    redis.call('EXPIRE', KEYS[1], tonumber(ARGV[1]))
    return {1, ''}
end
return {0, ''}
`)

// IssueIdempotencyToken 写入 init 状态
func IssueIdempotencyToken(ctx context.Context, key string) error {
	return RedisClient.HSet(ctx, key, "state", IdempotencyStateInit).Err()
}

// SetTokenTTL 为新建的 token 设置过期
func SetTokenTTL(ctx context.Context, key string) error {
	return RedisClient.Expire(ctx, key, IdempotencyTokenTTL).Err()
}

// AcquireIdempotencyLock 尝试占用 token，返回 (state, cachedResult)
// state: 1 成功 / 2 已完成 / 3 处理中 / 0 不存在
func AcquireIdempotencyLock(ctx context.Context, key string) (int64, string, error) {
	res, err := acquireScript.Run(ctx, RedisClient, []string{key}, int(IdempotencyTokenTTL.Seconds())).Result()
	if err != nil {
		return 0, "", err
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) != 2 {
		return 0, "", errors.New("idempotency lua 返回值异常")
	}
	state, _ := arr[0].(int64)
	cached, _ := arr[1].(string)
	return state, cached, nil
}

// CommitIdempotencyResult 写入最终结果并设置 TTL
func CommitIdempotencyResult(ctx context.Context, key, result string) error {
	pipe := RedisClient.TxPipeline()
	pipe.HSet(ctx, key, "state", IdempotencyStateDone, "result", result)
	pipe.Expire(ctx, key, IdempotencyResultTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// ReleaseIdempotencyLock 处理失败时回滚到 init，允许客户端用同一 token 重试
func ReleaseIdempotencyLock(ctx context.Context, key string) error {
	return RedisClient.HSet(ctx, key, "state", IdempotencyStateInit).Err()
}
