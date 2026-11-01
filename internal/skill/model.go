package skill

import "time"

// SkillProduct 秒杀活动：商品 ProductId 在 [StartAt, EndAt) 内以 PriceCents 限量 Num 件出售，每人限购 PerUserLimit 件。
// (product_id, start_at) 唯一：同一商品同一场次只有一个活动，admin 重复创建按幂等返回已有活动。
type SkillProduct struct {
	Id           uint      `gorm:"primarykey" json:"id"`
	ProductId    uint      `gorm:"not null;uniqueIndex:uk_skill_product_start,priority:1" json:"product_id"`
	BossId       uint      `gorm:"not null" json:"boss_id"` // 卖家，创建时取自商品表
	Title        string    `json:"title"`
	PriceCents   int64     `gorm:"not null;default:0" json:"price_cents"`  // 秒杀价（分）
	OriginCents  int64     `gorm:"not null;default:0" json:"origin_cents"` // 创建时的商品原价（分），仅展示
	Num          int       `gorm:"not null" json:"num"`                    // 秒杀库存
	Sold         int       `gorm:"not null;default:0" json:"-"`            // 有效成单件数（关单归还），DB 兜底：sold + n <= num
	PerUserLimit int       `gorm:"not null;default:1" json:"per_user_limit"`
	StartAt      time.Time `gorm:"uniqueIndex:uk_skill_product_start,priority:2" json:"start_at"`
	EndAt        time.Time `gorm:"index" json:"end_at"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
}

// SkillUser 用户在某场秒杀的有效成单件数。(skill_product_id, user_id) 唯一，是 Redis 名额之外防重复成单的 DB 兜底。
type SkillUser struct {
	Id             uint `gorm:"primarykey"`
	SkillProductId uint `gorm:"not null;uniqueIndex:uk_skill_user,priority:1"`
	UserId         uint `gorm:"not null;uniqueIndex:uk_skill_user,priority:2"`
	Bought         int  `gorm:"not null;default:0"`
}
