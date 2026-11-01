package groupbuy

import (
	"time"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"
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
	dbmodel.Model
	ProductID    uint      `gorm:"not null;index:idx_gb_product"`
	LeaderID     uint      `gorm:"not null;index:idx_gb_leader"`
	TargetCount  int       `gorm:"not null"`
	CurrentCount int       `gorm:"not null;default:0"`
	PriceCents   int64     `gorm:"not null"`
	Status       uint      `gorm:"not null;default:0;index:idx_gb_status_expire,priority:1"`
	ExpireAt     time.Time `gorm:"not null;index:idx_gb_status_expire,priority:2"`
	// 人工关团审计：谁关的、为什么关。超时散团不写。
	// 不对外输出：分享落地页免登录，会把运营 id 和风控原因暴露给任何人。
	ClosedBy    uint   `gorm:"not null;default:0" json:"-"`
	CloseReason string `gorm:"size:255;not null;default:''" json:"-"`
}

func (GroupbuyGroup) TableName() string { return "groupbuy_group" }

// GroupbuyMember 拼团成员，与订单 1:1 关联。
//
//	uniq(group_id, user_id) 由 DB 层兜底重复加入；
//	order_id 在团长发起 / 成员加入路径上同事务写入，保证不会出现"团里有人但没订单"。
type GroupbuyMember struct {
	dbmodel.Model
	GroupID uint  `gorm:"not null;uniqueIndex:uk_gb_group_user,priority:1"`
	UserID  uint  `gorm:"not null;uniqueIndex:uk_gb_group_user,priority:2;index"`
	OrderID int64 `gorm:"not null" json:"-"` // 落地页免登录可看，不对外暴露订单 id
	Status  uint  `gorm:"not null;default:0"`
}

func (GroupbuyMember) TableName() string { return "groupbuy_member" }

// 拼团站内通知类型。
const (
	NoticeKindJoined  = "joined"  // 团长：有新成员参团
	NoticeKindSuccess = "success" // 全体成员：成团，等待发货
	NoticeKindExpired = "expired" // 全体成员：超时散团 / 人工关团，款项已退回
)

// GroupbuyNotice 拼团站内通知，由 groupbuy.notify 消费者按事件写入。
//
//	uk_gb_notice(user_id, group_id, kind, ref_id) 吸收 at-least-once 重复投递；
//	joined 的 ref_id 是新成员 user_id（团长每来一人收一条），其余为 0。
type GroupbuyNotice struct {
	dbmodel.Model
	UserID  uint   `gorm:"not null;uniqueIndex:uk_gb_notice,priority:1"`
	GroupID uint   `gorm:"not null;uniqueIndex:uk_gb_notice,priority:2"`
	Kind    string `gorm:"size:16;not null;uniqueIndex:uk_gb_notice,priority:3"`
	RefID   uint   `gorm:"not null;default:0;uniqueIndex:uk_gb_notice,priority:4"`
	Content string `gorm:"size:255;not null"`
}

func (GroupbuyNotice) TableName() string { return "groupbuy_notice" }
