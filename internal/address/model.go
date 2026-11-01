package address

import "github.com/RedInn7/gomall/internal/shared/dbmodel"

type Address struct {
	dbmodel.Model
	UserID  uint   `gorm:"not null"`
	Name    string `gorm:"type:varchar(20) not null"`
	Phone   string `gorm:"type:varchar(11) not null"`
	Address string `gorm:"type:varchar(50) not null"`
}

// Snapshot 下单时拷进订单的收货地址。订单以 embedded 方式落成 address_name / address_phone / address_detail 三列，
// 列宽与 Address 一致。
type Snapshot struct {
	Name   string `gorm:"type:varchar(20);not null;default:''" json:"name"`
	Phone  string `gorm:"type:varchar(11);not null;default:''" json:"phone"`
	Detail string `gorm:"type:varchar(50);not null;default:''" json:"detail"`
}
