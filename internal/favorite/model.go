package favorite

import (
	"github.com/jinzhu/gorm"

	"github.com/RedInn7/gomall/repository/db/model"
)

type Favorite struct {
	gorm.Model
	User      model.User    `gorm:"ForeignKey:UserID"`
	UserID    uint          `gorm:"not null"`
	Product   model.Product `gorm:"ForeignKey:ProductID"`
	ProductID uint          `gorm:"not null"`
	Boss      model.User    `gorm:"ForeignKey:BossID"`
	BossID    uint          `gorm:"not null"`
}
