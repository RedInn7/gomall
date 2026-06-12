package cart

import "github.com/RedInn7/gomall/internal/shared/dbmodel"

// Cart 购物车模型
type Cart struct {
	dbmodel.Model
	UserID    uint
	ProductID uint `gorm:"not null"`
	BossID    uint
	Num       uint
	MaxNum    uint
	Check     bool
}
