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
// uId 为商品归属 boss，WHERE 同时过滤 boss_id 防止越权覆盖他人商品（IDOR）。
// 返回受影响行数：0 表示商品不存在或调用方不是归属 boss，调用方据此拒绝请求。
func (d *ProductDao) UpdateProduct(pId, uId uint, product *Product) (int64, error) {
	res := d.DB.Model(&Product{}).
		Where("id=? AND boss_id=?", pId, uId).
		Updates(map[string]interface{}{
			"name":           product.Name,
			"category_id":    product.CategoryID,
			"title":          product.Title,
			"info":           product.Info,
			"price":          product.Price,
			"discount_price": product.DiscountPrice,
			"num":            product.Num,
			"on_sale":        product.OnSale,
		})
	return res.RowsAffected, res.Error
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

// DeductStock 原子扣减库存：UPDATE ... SET num=num-? WHERE id=? AND num>=?。
// 把"读-判断够不够-写回"塌缩成单条条件 UPDATE，彻底消除两个买家并发读到同一水位
// 各自扣减导致的超卖（TOCTOU）。ok=false 表示库存不足（影响 0 行），调用方据此拒单。
// 需在业务事务内用 tx 绑定的 DAO 调用（NewProductDaoWithDB(tx)），与扣款同进同退。
func (d *ProductDao) DeductStock(productId uint, num int) (ok bool, err error) {
	res := d.DB.Model(&Product{}).
		Where("id=? AND num>=?", productId, num).
		Update("num", gorm.Expr("num-?", num))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
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

// ListAfterID keyset 分页：取 id > lastID 的商品按 id 升序，limit 限量。
// 供全表遍历型任务（如 ES backfill）游标式拉取。
func (d *ProductDao) ListAfterID(lastID uint, limit int) (products []*Product, err error) {
	q := d.DB.Model(&Product{}).Order("id ASC").Limit(limit)
	if lastID > 0 {
		q = q.Where("id > ?", lastID)
	}
	err = q.Find(&products).Error
	return
}
