package address

import "github.com/RedInn7/gomall/types"

// 字段上限与 address 表列宽一致（varchar 按字符计，validator 的 max 也按字符计）；
// 手机号按大陆 11 位数字校验。

type AddressServiceReq struct {
	Id      uint   `form:"id" json:"id" binding:"required"`
	Name    string `form:"name" json:"name" binding:"required,max=20"`
	Phone   string `form:"phone" json:"phone" binding:"required,numeric,len=11"`
	Address string `form:"address" json:"address" binding:"required,max=50"`
}

type AddressListReq struct {
	types.BasePage
}

type AddressDeleteReq struct {
	Id uint `form:"id" json:"id" binding:"required"`
}

type AddressCreateReq struct {
	Name    string `form:"name" json:"name" binding:"required,max=20"`
	Phone   string `form:"phone" json:"phone" binding:"required,numeric,len=11"`
	Address string `form:"address" json:"address" binding:"required,max=50"`
}

type AddressResp struct {
	ID        uint   `json:"id"`
	UserID    uint   `json:"user_id"`
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Address   string `json:"address"`
	CreatedAt int64  `json:"created_at"`
}
