package redpacket

import (
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
)

// 红包状态。2 曾是「已过期」，从没有代码写入过（过期判断看 expire_at，回收直接 active->refunded），已删除，值保留不复用。
const (
	RedPacketStatusActive   uint = 1 // 进行中
	RedPacketStatusFinished uint = 3 // 已抢完
	RedPacketStatusRefunded uint = 4 // 已退款给发包人 (过期回收)
)

// RedPacket 红包发放记录
//
//	Redis LIST 只负责分发份额 (RPUSH 预拆 + LPOP 抢)
//	资金以 DB 为准：剩余金额 = Total - SUM(领取金额)，Remaining 在领取事务内递减，过期回收按 DB 退款
type RedPacket struct {
	dbmodel.Model
	UserID    uint      `gorm:"not null;index"`               // 发包人
	Total     int64     `gorm:"not null"`                     // 总额，单位：分
	Count     int       `gorm:"not null"`                     // 红包总份数
	Remaining int       `gorm:"not null"`                     // 剩余份数，领取事务内递减
	ExpireAt  time.Time `gorm:"not null;index:idx_rp_expire"` // 过期时间
	Status    uint      `gorm:"not null;default:1;index"`     // 1 active / 3 finished / 4 refunded
	Greeting  string    `gorm:"size:128"`                     // 祝福语
	// ShareCode 领取口令：随机生成，只给发包人，由发包人分享出去。id 是自增主键可以扫号，
	// 拿着口令才能领、才能看领取明细。迁移前的老红包为空串，按老规则放行。
	ShareCode string `gorm:"size:16"`
}

func (RedPacket) TableName() string { return "red_packet" }

// RedPacketClaim 用户领取的具体一份红包
//
//	uniq(red_packet_id, user_id) 兜底 DB 层面的"同用户不可重复领"
type RedPacketClaim struct {
	dbmodel.Model
	RedPacketID uint  `gorm:"not null;uniqueIndex:uk_rp_user,priority:1"`
	UserID      uint  `gorm:"not null;uniqueIndex:uk_rp_user,priority:2;index"`
	Amount      int64 `gorm:"not null"` // 抢到金额，单位：分
}

func (RedPacketClaim) TableName() string { return "red_packet_claim" }
