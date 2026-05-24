package model

import (
	"time"

	"github.com/jinzhu/gorm"
)

// 团 / 成员状态。
const (
	GroupbuyStatusOpen    uint = 0 // 进行中，可加入
	GroupbuyStatusSuccess uint = 1 // 成团：成员订单已从 WaitGroup → WaitShip
	GroupbuyStatusExpired uint = 2 // 超时未凑齐 → Saga 散团
	GroupbuyStatusClosed  uint = 3 // 人工 / 风控关团

	GroupbuyMemberJoined   uint = 0 // 已加入，订单处于 WaitGroup
	GroupbuyMemberSucceed  uint = 1 // 成团：订单已推进到 WaitShip
	GroupbuyMemberRefunded uint = 2 // 散团：订单已 Closed，预扣库存已归还
)

// GroupbuyGroup 一次拼团活动的"团"实体，团长发起后所有团员加入同一行。
//
//	current_count vs target_count 是并发争用点：JoinGroupAtomic 用单条
//	UPDATE ... WHERE current_count<target_count AND status=0 AND expire_at>NOW()
//	兜底，避免读后写竞态。
//	价格 / 商品维度不允许中途改：price_cents / product_id 一旦写入即不可变。
type GroupbuyGroup struct {
	gorm.Model
	ProductID    uint      `gorm:"not null;index:idx_gb_product"`
	LeaderID     uint      `gorm:"not null;index:idx_gb_leader"`
	TargetCount  int       `gorm:"not null"`
	CurrentCount int       `gorm:"not null;default:0"`
	PriceCents   int64     `gorm:"not null"`
	Status       uint      `gorm:"not null;default:0;index:idx_gb_status_expire,priority:1"`
	ExpireAt     time.Time `gorm:"not null;index:idx_gb_status_expire,priority:2"`
}

func (GroupbuyGroup) TableName() string { return "groupbuy_group" }

// GroupbuyMember 拼团成员，与订单 1:1 关联。
//
//	uniq(group_id, user_id) 由 DB 层兜底重复加入；
//	order_id 在团长发起 / 成员加入路径上同事务写入，保证不会出现"团里有人但没订单"。
type GroupbuyMember struct {
	gorm.Model
	GroupID uint  `gorm:"not null;uniqueIndex:uk_gb_group_user,priority:1"`
	UserID  uint  `gorm:"not null;uniqueIndex:uk_gb_group_user,priority:2;index"`
	OrderID int64 `gorm:"not null"`
	Status  uint  `gorm:"not null;default:0"`
}

func (GroupbuyMember) TableName() string { return "groupbuy_member" }
