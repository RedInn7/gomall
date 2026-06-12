package address

import "github.com/RedInn7/gomall/internal/shared/dbmodel"

type Address struct {
	dbmodel.Model
	UserID  uint   `gorm:"not null"`
	Name    string `gorm:"type:varchar(20) not null"`
	Phone   string `gorm:"type:varchar(11) not null"`
	Address string `gorm:"type:varchar(50) not null"`
}
