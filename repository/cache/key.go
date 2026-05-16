package cache

import (
	"fmt"
	"strconv"
)

const (
	// RankKey 每日排名
	RankKey             = "rank"
	SkillProductKey     = "skill:product:%d"
	SkillProductListKey = "skill:product_list"
	SkillProductUserKey = "skill:user:%s"

	// IdempotencyKey idempotency token，按用户隔离
	IdempotencyKey = "idemp:%d:%s"
)

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
