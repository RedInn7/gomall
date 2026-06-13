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
	// Num / Money 由客户端传入，必须设上下界：
	//   - 下界 min=1 挡住 0 与负数（Money 为有符号 int，无下界时可传负数）；
	//   - 上界封顶让 unitCents*qty 远低于 int64 上限，杜绝乘法溢出。
	//     Money 上限 1e8 分（单价 100 万元），Num 上限 1e4，乘积 ≤ 1e12 ≪ 9.2e18。
	Num       uint `form:"num" json:"num" binding:"required,min=1,max=10000"`
	AddressID uint `form:"address_id" json:"address_id"`
	Money     int  `form:"money" json:"money" binding:"required,min=1,max=100000000"`
	BossID    uint `form:"boss_id" json:"boss_id"`
	UserID    uint `form:"user_id" json:"user_id"`
	OrderNum  uint `form:"order_num" json:"order_num"`
	Type      int  `form:"type" json:"type"`
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
