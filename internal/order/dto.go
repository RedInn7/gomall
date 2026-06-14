package order

import (
	"time"

	"github.com/RedInn7/gomall/types"
)

type OrderServiceReq struct {
	OrderId   uint `form:"order_id" json:"order_id"`
	ProductID uint `form:"product_id" json:"product_id"`
	Num       uint `form:"num" json:"num"`
	AddressID uint `form:"address_id" json:"address_id"`
	Money     int  `form:"money" json:"money"`
	BossID    uint `form:"boss_id" json:"boss_id"`
	UserID    uint `form:"user_id" json:"user_id"`
	OrderNum  uint `form:"order_num" json:"order_num"`
	Type      int  `form:"type" json:"type"`
	*types.BasePage
}

type OrderCreateReq struct {
	OrderId   uint `form:"order_id" json:"order_id"`
	ProductID uint `form:"product_id" json:"product_id" binding:"required"`
	// Num 由客户端传入，必须设上下界：下界 min=1 挡住 0 与负数；上界封顶让单价*数量远低于
	// int64 上限，杜绝乘法溢出（Num 上限 1e4，配合服务端单价上限，乘积 ≪ 9.2e18）。
	Num       uint `form:"num" json:"num" binding:"required,min=1,max=10000"`
	AddressID uint `form:"address_id" json:"address_id"`
	// Money 不参与计费：单价一律由服务端从商品表反查（见 service.resolveProductPricing）。
	// 金额是安全敏感字段，信客户端等于把定价权交给买家。保留该字段仅为兼容旧客户端报文，
	// 服务端不读它；omitempty 让老客户端可继续传、新客户端可不传，传了也只做格式校验。
	Money    int  `form:"money" json:"money" binding:"omitempty,min=1,max=100000000"`
	BossID   uint `form:"boss_id" json:"boss_id"`
	UserID   uint `form:"user_id" json:"user_id"`
	OrderNum uint `form:"order_num" json:"order_num"`
	Type     int  `form:"type" json:"type"`
}

type OrderListReq struct {
	Type int `form:"type" json:"type"`
	types.BasePage
}

type OrderShowReq struct {
	OrderId uint `json:"order_id" form:"order_id"`
}

type OrderDeleteReq struct {
	OrderId uint `json:"order_id" form:"order_id"`
}

type OrderListResp struct {
	LastId int                  `json:"last_id"`
	List   []*OrderListRespItem `json:"list"`
}

type OrderListRespItem struct {
	ID            uint      `json:"id"`
	OrderNum      uint64    `json:"order_num"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	UserID        uint      `json:"user_id"`
	ProductID     uint      `json:"product_id"`
	BossID        uint      `json:"boss_id"`
	Num           uint      `json:"num"`
	AddressPhone  string    `json:"address_phone"`
	Address       string    `json:"address"`
	Type          uint      `json:"type"`
	Name          string    `json:"name"`
	ImgPath       string    `json:"img_path"`
	DiscountPrice string    `json:"discount_price"`
}
