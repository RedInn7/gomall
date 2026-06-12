package product

import "github.com/RedInn7/gomall/internal/shared/dbmodel"

type ProductImg struct {
	dbmodel.Model
	ProductID uint `gorm:"not null"`
	ImgPath   string
}
