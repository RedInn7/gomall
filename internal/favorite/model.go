package favorite

import (
	"github.com/RedInn7/gomall/internal/shared/dbmodel"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/user"
)

type Favorite struct {
	dbmodel.Model
	User      user.User       `gorm:"ForeignKey:UserID"`
	UserID    uint            `gorm:"not null"`
	Product   product.Product `gorm:"ForeignKey:ProductID"`
	ProductID uint            `gorm:"not null"`
	Boss      user.User       `gorm:"ForeignKey:BossID"`
	BossID    uint            `gorm:"not null"`
}
