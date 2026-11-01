package skill

import "time"

// SkillProductInitReq admin 创建秒杀活动。卖家与原价取自商品表，不由请求指定。
type SkillProductInitReq struct {
	ProductId    uint      `json:"product_id" binding:"required"`
	PriceCents   int64     `json:"price_cents" binding:"required,min=1"`
	Num          int       `json:"num" binding:"required,min=1"`
	PerUserLimit int       `json:"per_user_limit" binding:"omitempty,min=1,max=99"` // 0 = 默认 1
	StartAt      time.Time `json:"start_at" binding:"required"`
	EndAt        time.Time `json:"end_at" binding:"required,gtfield=StartAt"`
}

// SkillProductReq 秒杀下单：每单 1 件，价格 / 卖家都由活动决定，客户端只指定场次和收货地址。
// 场次给 skill_product_id；兼容旧调用只给 product_id，取该商品进行中的场次。address_id 不给用用户的第一个地址。
type SkillProductReq struct {
	SkillProductId uint `json:"skill_product_id" form:"skill_product_id"`
	ProductId      uint `json:"product_id" form:"product_id"`
	AddressId      uint `json:"address_id" form:"address_id"`
}

// GetSkillProductReq 活动详情：skill_product_id，或旧调用方式的 product_id（取该商品进行中 / 最近一场）。
type GetSkillProductReq struct {
	SkillProductId uint `json:"skill_product_id" form:"skill_product_id"`
	ProductId      uint `json:"product_id" form:"product_id"`
}

// SkillProductDetail 活动详情 + 实时剩余库存（Redis）。
type SkillProductDetail struct {
	*SkillProduct
	Remaining int64 `json:"remaining"`
}
