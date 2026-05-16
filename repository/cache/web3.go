package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Web3 链下签名相关的 Redis 协议：
//
//   web3:nonce:{userID}:{orderID}     一次性 nonce，5 分钟有效
//   paydown:web3:pending:{orderID}    钱包签名通过后写入，30 分钟有效，
//                                     listener 链上确认后由消费者侧 DEL
//
// nonce 走 SETNX + EXPIRE 双段写入；消费走 Lua 原子 GET+DEL，
// 避免两个并发请求拿到同一 nonce 后重放。

const (
	Web3NonceTTL          = 5 * time.Minute
	Web3PendingTTL        = 30 * time.Minute
	web3NonceKeyTemplate  = "web3:nonce:%d:%d"
	web3PendingKeyPattern = "paydown:web3:pending:%d"
)

var (
	// ErrWeb3NonceMissing nonce 已过期或从未签发
	ErrWeb3NonceMissing = errors.New("web3 nonce 不存在或已过期")
	// ErrWeb3NonceMismatch nonce 不匹配，可能被串用或伪造
	ErrWeb3NonceMismatch = errors.New("web3 nonce 不匹配")
)

// Web3NonceKey 业务 nonce key。userID + orderID 双绑定，避免跨订单复用
func Web3NonceKey(userID, orderID uint) string {
	return fmt.Sprintf(web3NonceKeyTemplate, userID, orderID)
}

// Web3PendingKey 钱包签名通过后的待链上确认占位
func Web3PendingKey(orderID uint) string {
	return fmt.Sprintf(web3PendingKeyPattern, orderID)
}

// PutWeb3Nonce 写入 nonce 并设 TTL。同一订单二次签发会覆盖旧 nonce，
// 等同于让上一次未消费的 nonce 立刻作废
func PutWeb3Nonce(ctx context.Context, userID, orderID uint, nonce string) error {
	return RedisClient.Set(ctx, Web3NonceKey(userID, orderID), nonce, Web3NonceTTL).Err()
}

// consumeNonceScript: GET 后立刻 DEL，保证同一个 nonce 只能用一次
//
// KEYS[1] = nonce key
// ARGV[1] = 客户端提交的 nonce
//
// 返回:
//   1  = 成功 (nonce 匹配并已删除)
//   -1 = 不存在 / 过期
//   -2 = 不匹配
var consumeNonceScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur == false then
    return -1
end
if cur ~= ARGV[1] then
    return -2
end
redis.call('DEL', KEYS[1])
return 1
`)

// ConsumeWeb3Nonce 校验 + 一次性消费 nonce，校验通过后立即 DEL，防重放
func ConsumeWeb3Nonce(ctx context.Context, userID, orderID uint, nonce string) error {
	res, err := consumeNonceScript.Run(ctx, RedisClient,
		[]string{Web3NonceKey(userID, orderID)}, nonce).Result()
	if err != nil {
		return err
	}
	code, _ := res.(int64)
	switch code {
	case 1:
		return nil
	case -1:
		return ErrWeb3NonceMissing
	case -2:
		return ErrWeb3NonceMismatch
	}
	return fmt.Errorf("consume nonce unexpected code: %v", code)
}

// SetWeb3Pending 写入钱包签名通过的待确认占位。HSet 形式方便对账时取字段
func SetWeb3Pending(ctx context.Context, orderID uint, walletAddr string) error {
	pipe := RedisClient.TxPipeline()
	pipe.HSet(ctx, Web3PendingKey(orderID),
		"addr", walletAddr,
		"ts", time.Now().Unix(),
	)
	pipe.Expire(ctx, Web3PendingKey(orderID), Web3PendingTTL)
	_, err := pipe.Exec(ctx)
	return err
}
