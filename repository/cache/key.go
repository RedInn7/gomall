package cache

import (
	"fmt"
	"strconv"
	"time"
)

const (
	// RankKey 每日排名
	RankKey = "rank"
	// SkillProductKey 秒杀活动详情 JSON（按活动 id），查不到时存 "null" 防穿透
	SkillProductKey = "skill:product:%d"
	// SkillProductListKey 未结束的秒杀活动列表 JSON 数组
	SkillProductListKey = "skill:product_list"

	// IdempotencyKey idempotency token，按用户隔离
	IdempotencyKey = "idemp:%d:%s"
)

// SkillProductTTL 秒杀活动详情缓存的存活时间；活动创建后不改，可以长一些。
const SkillProductTTL = 30 * time.Minute

// SkillListTTL 秒杀列表缓存的存活时间：新建活动会主动删列表，这里只兜新开场 / 结束的可见性。
const SkillListTTL = time.Minute

// SkillNullTTL 不存在的活动 id 的空值缓存时间，挡住随机 id 打穿 DB。
const SkillNullTTL = time.Minute

// SkillStockKey 秒杀剩余库存（Lua 原子扣减）。{id} 是 cluster hash tag，与用户名额同槽，Lua 才能同时操作两个 key。
func SkillStockKey(id uint) string {
	return fmt.Sprintf("skill:{%d}:stock", id)
}

// SkillUsersKey 秒杀用户名额 HASH：userID -> 已抢件数（含排队中、未支付的单）。
func SkillUsersKey(id uint) string {
	return fmt.Sprintf("skill:{%d}:users", id)
}

// SkillKeys 一场秒杀在 Redis 里的全部 key，{id} 让它们同槽，Lua 可以一起操作：
// stock 剩余库存 / users 用户名额 HASH / inflight 在途 ticket ZSET（member=userID:ticket，score=抢到时刻毫秒）/
// returned 已归还 Redis 的关单订单 id SET / ver 每次变更 +1，对账靠它判断快照期间有没有人动过。
func SkillKeys(id uint) []string {
	return []string{
		SkillStockKey(id),
		SkillUsersKey(id),
		fmt.Sprintf("skill:{%d}:inflight", id),
		fmt.Sprintf("skill:{%d}:returned", id),
		fmt.Sprintf("skill:{%d}:ver", id),
	}
}

// OrderAsyncInflightKey 某商品已预占、还在 MQ 里排队没落库的异步下单 ZSET（member=ticket:num，score=入队秒）。
// 预占对账把它算作合法占用，MQ 积压时不会把排队单的预占当泄漏回收。
func OrderAsyncInflightKey(productID uint) string {
	return fmt.Sprintf("order:async:inflight:%d", productID)
}

func IdempotencyTokenKey(userId uint, token string) string {
	return fmt.Sprintf(IdempotencyKey, userId, token)
}

func ProductViewKey(id uint) string {
	return fmt.Sprintf("view:product:%s", strconv.Itoa(int(id)))
}

// RedPacketAmountsKey 红包预拆好的金额 LIST (LPOP 即抢)
func RedPacketAmountsKey(id uint) string {
	return fmt.Sprintf("redpacket:%d:amounts", id)
}

// RedPacketClaimedKey 红包已领用户 HASH (userID -> amount)
func RedPacketClaimedKey(id uint) string {
	return fmt.Sprintf("redpacket:%d:claimed", id)
}

// StripeCheckoutKey 订单有一个还能付款的 Stripe Checkout 会话，随会话过期；存在期间超时关单会跳过这张单。
func StripeCheckoutKey(orderID uint) string {
	return fmt.Sprintf("stripe:checkout:%d", orderID)
}
