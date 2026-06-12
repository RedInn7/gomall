package carousel

import "github.com/RedInn7/gomall/internal/shared/dbmodel"

type Carousel struct {
	dbmodel.Model
	ImgPath   string
	ProductID uint `gorm:"not null"`
}
