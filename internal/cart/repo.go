package cart

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/repository/db/dao"
)

type CartDao struct {
	*gorm.DB
}

func NewCartDao(ctx context.Context) *CartDao {
	return &CartDao{dao.NewDBClient(ctx)}
}

// CreateCart 创建 cart pId(商品 id)、uId(用户id)、bId(店家id)
func (d *CartDao) CreateCart(pId, uId, bId uint) (cart *Cart, status int, err error) {
	// 查询有无此条商品
	cart, err = d.GetCartById(pId, uId, bId)
	// 空的，第一次加入
	if err == gorm.ErrRecordNotFound {
		cart = &Cart{
			UserID:    uId,
			ProductID: pId,
			BossID:    bId,
			Num:       1,
			MaxNum:    10,
			Check:     false,
		}
		err = d.DB.Create(&cart).Error
		if err != nil {
			return
		}
		return cart, e.SUCCESS, err
	}
	if cart.Num < cart.MaxNum {
		// 小于最大 num
		cart.Num++
		err = d.DB.Save(&cart).Error
		if err != nil {
			return
		}
		return cart, e.ErrorProductExistCart, err
	}
	// 大于最大 num
	return cart, e.ErrorProductMoreCart, err
}

// GetCartById 通过 id 获取 Cart
func (d *CartDao) GetCartById(pId, uId, bId uint) (cart *Cart, err error) {
	err = d.DB.Model(&Cart{}).
		Where("user_id = ? AND product_id = ? AND boss_id = ?",
			uId, pId, bId).
		First(&cart).Error

	return
}

// ListCartByUserId 通过 user_id 获取 Cart
func (d *CartDao) ListCartByUserId(uId uint) (cart []*CartResp, err error) {
	err = d.DB.Model(&Cart{}).
		Joins("AS c LEFT JOIN product AS p ON c.product_id = p.id").
		Where("c.user_id = ?", uId).
		Select("c.id AS id," +
			"c.user_id AS user_id," +
			"c.product_id AS product_id," +
			"UNIX_TIMESTAMP(c.created_at) AS created_at," +
			"c.num AS num," +
			"c.max_num AS max_num," +
			"c.`check` AS check_," +
			"p.img_path AS img_path," +
			"p.boss_id AS boss_id," +
			"p.boss_name AS boss_name," +
			"p.info AS info," +
			"p.discount_price AS discount_price").
		Find(&cart).Error

	return
}

// UpdateCartNumById 通过 id 更新 Cart 信息
func (d *CartDao) UpdateCartNumById(cId, uId, num uint) error {
	return d.DB.Model(&Cart{}).
		Where("id = ? AND user_id = ?", cId, uId).
		Update("num", num).Error
}

// DeleteCartById 通过 cart_id 删除 cart
func (d *CartDao) DeleteCartById(cId, uId uint) error {
	return d.DB.Model(&Cart{}).
		Where("id = ? AND user_id = ?", cId, uId).
		Delete(&Cart{}).Error
}
