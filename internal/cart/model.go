package cart

import "github.com/jinzhu/gorm"

// Cart 购物车模型
type Cart struct {
	gorm.Model
	UserID    uint
	ProductID uint `gorm:"not null"`
	BossID    uint
	Num       uint
	MaxNum    uint
	Check     bool
}
