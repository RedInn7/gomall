package skill

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/repository/db/dao"
)

type SkillGoodsDao struct {
	*gorm.DB
}

func NewSkillGoodsDao(ctx context.Context) *SkillGoodsDao {
	return &SkillGoodsDao{dao.NewDBClient(ctx)}
}

func (d *SkillGoodsDao) Create(in *SkillProduct) error {
	return d.Model(&SkillProduct{}).Create(&in).Error
}

func (d *SkillGoodsDao) BatchCreate(in []*SkillProduct) error {
	return d.Model(&SkillProduct{}).
		CreateInBatches(&in, consts.ProductBatchCreate).Error
}

func (d *SkillGoodsDao) CreateByList(in []*SkillProduct) error {
	return d.Model(&SkillProduct{}).Create(&in).Error
}

func (d *SkillGoodsDao) ListSkillGoods() (resp []*SkillProduct, err error) {
	err = d.Model(&SkillProduct{}).
		Where("num > 0").Find(&resp).Error

	return
}

func (d *SkillGoodsDao) GetByProductId(productId uint) (resp *SkillProduct, err error) {
	err = d.Model(&SkillProduct{}).
		Where("product_id = ?", productId).First(&resp).Error

	return
}
