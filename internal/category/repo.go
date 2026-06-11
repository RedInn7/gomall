package category

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type CategoryDao struct {
	*gorm.DB
}

func NewCategoryDao(ctx context.Context) *CategoryDao {
	return &CategoryDao{dao.NewDBClient(ctx)}
}

func NewCategoryDaoByDB(db *gorm.DB) *CategoryDao {
	return &CategoryDao{db}
}

// ListCategory 分类列表
func (d *CategoryDao) ListCategory() (r []*Category, err error) {
	err = d.DB.Model(&Category{}).Find(&r).Error
	return
}
