package product

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/types"
)

type ProductDao struct {
	*gorm.DB
}

func NewProductDao(ctx context.Context) *ProductDao {
	return &ProductDao{dao.NewDBClient(ctx)}
}

func NewProductDaoByDB(db *gorm.DB) *ProductDao {
	return &ProductDao{db}
}

// GetProductById 通过 id 获取 product
func (d *ProductDao) GetProductById(id uint) (product *Product, err error) {
	err = d.DB.Model(&Product{}).
		Where("id=?", id).First(&product).Error

	return
}

// ShowProductById 通过 id 获取 product
func (d *ProductDao) ShowProductById(id uint) (product *Product, err error) {
	err = d.DB.Model(&Product{}).
		Where("id=?", id).First(&product).Error

	return
}

// ListProductByCondition 获取商品列表
func (d *ProductDao) ListProductByCondition(condition map[string]interface{}, page types.BasePage) (products []*Product, err error) {
	page.Normalize()
	err = d.DB.Where(condition).
		Offset((page.PageNum - 1) * page.PageSize).
		Limit(page.PageSize).
		Find(&products).Error

	return
}

// CreateProduct 创建商品
func (d *ProductDao) CreateProduct(product *Product) error {
	return d.DB.Model(&Product{}).
		Create(&product).Error
}

// CountProductByCondition 根据条件统计商品数量
func (d *ProductDao) CountProductByCondition(condition map[string]interface{}) (total int64, err error) {
	err = d.DB.Model(&Product{}).
		Where(condition).Count(&total).Error

	return
}

// DeleteProduct 删除商品
func (d *ProductDao) DeleteProduct(pId, uId uint) error {
	return d.DB.Model(&Product{}).
		Where("id = ? AND boss_id = ?", pId, uId).
		Delete(&Product{}).
		Error
}

// UpdateProduct 更新商品。
// 显式映射列名走 map 更新：gorm 对 struct 的 Updates 会跳过零值字段，
// 导致下架（on_sale=false）、库存清零（num=0）这类零值写入静默丢失。
// 字段集合与调用方组装的可更新字段一一对应，boss 维度与图片路径不在此处变更。
func (d *ProductDao) UpdateProduct(pId uint, product *Product) error {
	return d.DB.Model(&Product{}).
		Where("id=?", pId).
		Updates(map[string]interface{}{
			"name":           product.Name,
			"category_id":    product.CategoryID,
			"title":          product.Title,
			"info":           product.Info,
			"price":          product.Price,
			"discount_price": product.DiscountPrice,
			"num":            product.Num,
			"on_sale":        product.OnSale,
		}).Error
}

// SearchProduct 搜索商品
func (d *ProductDao) SearchProduct(info string, page types.BasePage) (products []*Product, count int64, err error) {
	page.Normalize()
	err = d.DB.Model(&Product{}).
		Where("name LIKE ? OR info LIKE ?", "%"+info+"%", "%"+info+"%").
		Offset((page.PageNum - 1) * page.PageSize).
		Limit(page.PageSize).
		Find(&products).Error

	if err != nil {
		return
	}

	err = d.DB.Model(&Product{}).
		Where("name LIKE ? OR info LIKE ?", "%"+info+"%", "%"+info+"%").
		Count(&count).
		Error

	return
}

func (d *ProductDao) RollbackStock(productId uint, num int) (bool, error) {
	res := d.DB.Model(&Product{}).
		Where("id=?", productId).
		Update("num", gorm.Expr("num+?", num))

	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, errors.New("回滚失败")
	}
	return true, nil
}

func NewProductDaoWithDB(db *gorm.DB) *ProductDao {
	return &ProductDao{DB: db}
}

// ListByIDs 按 id 批量取商品，结果顺序不保证，调用方按需重排
func (d *ProductDao) ListByIDs(ids []uint) (products []*Product, err error) {
	if len(ids) == 0 {
		return nil, nil
	}
	err = d.DB.Model(&Product{}).
		Where("id IN ?", ids).
		Find(&products).Error
	return
}
