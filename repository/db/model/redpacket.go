package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

// 红包状态
const (
	RedPacketStatusActive   uint = 1 // 进行中
	RedPacketStatusExpired  uint = 2 // 已过期 (cron 标记)
	RedPacketStatusFinished uint = 3 // 已抢完
	RedPacketStatusRefunded uint = 4 // 已退款给发包人 (过期回收)
)

// RedPacket 红包发放记录
//
//	权威剩余份数在 Redis LIST 里 (RPUSH 预拆 + LPOP 抢)
//	DB.Remaining 仅为最终一致快照，用于历史查询/对账
type RedPacket struct {
	gorm.Model
	UserID    uint      `gorm:"not null;index"`               // 发包人
	Total     int64     `gorm:"not null"`                     // 总额，单位：分
	Count     int       `gorm:"not null"`                     // 红包总份数
	Remaining int       `gorm:"not null"`                     // DB 同步剩余份数 (Redis 为权威)
	ExpireAt  time.Time `gorm:"not null;index:idx_rp_expire"` // 过期时间
	Status    uint      `gorm:"not null;default:1;index"`     // 1 active / 2 expired / 3 finished / 4 refunded
	Greeting  string    `gorm:"size:128"`                     // 祝福语
}

func (RedPacket) TableName() string { return "red_packet" }

// RedPacketClaim 用户领取的具体一份红包
//
//	uniq(red_packet_id, user_id) 兜底 DB 层面的"同用户不可重复领"
type RedPacketClaim struct {
	gorm.Model
	RedPacketID uint  `gorm:"not null;uniqueIndex:uk_rp_user,priority:1"`
	UserID      uint  `gorm:"not null;uniqueIndex:uk_rp_user,priority:2;index"`
	Amount      int64 `gorm:"not null"` // 抢到金额，单位：分
}

func (RedPacketClaim) TableName() string { return "red_packet_claim" }
