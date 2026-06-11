package product

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type ProductImgDao struct {
	*gorm.DB
}

func NewProductImgDao(ctx context.Context) *ProductImgDao {
	return &ProductImgDao{dao.NewDBClient(ctx)}
}

func NewProductImgDaoByDB(db *gorm.DB) *ProductImgDao {
	return &ProductImgDao{db}
}

// CreateProductImg 创建商品图片
func (d *ProductImgDao) CreateProductImg(productImg *ProductImg) (err error) {
	err = d.DB.Model(&ProductImg{}).Create(&productImg).Error

	return
}

// ListProductImgByProductId 根据商品 id 获取商品图片
func (d *ProductImgDao) ListProductImgByProductId(pId uint) (r []*ProductImgResp, err error) {
	err = d.DB.Model(&ProductImg{}).
		Where("product_id=?", pId).
		Find(&r).Error

	return
}
