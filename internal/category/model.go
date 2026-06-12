package category

import "github.com/RedInn7/gomall/internal/shared/dbmodel"

type Category struct {
	dbmodel.Model
	CategoryName string
}
