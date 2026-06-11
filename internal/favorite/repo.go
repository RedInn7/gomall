package favorite

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type FavoritesDao struct {
	*gorm.DB
}

func NewFavoritesDao(ctx context.Context) *FavoritesDao {
	return &FavoritesDao{dao.NewDBClient(ctx)}
}

func NewFavoritesDaoByDB(db *gorm.DB) *FavoritesDao {
	return &FavoritesDao{db}
}

// ListFavoriteByUserId 通过 user_id 获取收藏夹列表
func (d *FavoritesDao) ListFavoriteByUserId(uId uint, pageSize, pageNum int) (r []*FavoriteListResp, total int64, err error) {
	// 总数
	err = d.DB.Model(&Favorite{}).Preload("User").
		Where("user_id=?", uId).Count(&total).Error
	if err != nil {
		return
	}
	err = d.DB.Model(&Favorite{}).
		Joins("AS f LEFT JOIN user AS u on u.id = f.boss_id").
		Joins("LEFT JOIN product AS p ON p.id = f.product_id").
		Joins("LEFT JOIN category AS c ON c.id = p.category_id").
		Where("f.user_id = ?", uId).
		Offset((pageNum - 1) * pageSize).Limit(pageSize).
		Select("f.user_id AS user_id," +
			"f.product_id AS product_id," +
			"UNIX_TIMESTAMP(f.created_at) AS created_at," +
			"p.title AS title," +
			"p.info AS info," +
			"p.name AS name," +
			"c.id AS category_id," +
			"c.category_name AS category_name," +
			"u.id AS boss_id," +
			"u.user_name AS boss_name," +
			"u.avatar AS boss_avatar," +
			"p.price AS price," +
			"p.img_path AS img_path," +
			"p.discount_price AS discount_price," +
			"p.num AS num," +
			"p.on_sale AS on_sale").
		Find(&r).Error

	return
}

// CreateFavorite 创建收藏夹
func (d *FavoritesDao) CreateFavorite(favorite *Favorite) (err error) {
	err = d.DB.Create(&favorite).Error
	return
}

// FavoriteExistOrNot 判断是否存在
func (d *FavoritesDao) FavoriteExistOrNot(uId uint, pid uint) (exist bool, err error) {
	var count int64
	db := d.DB.Model(&Favorite{}).
		Where("user_id=?", uId)
	if pid != 0 {
		db = db.Where("product_id=?", pid)
	}
	err = db.Count(&count).Error

	if err != nil {
		return
	}
	return count > 0, nil
}

// DeleteFavoriteById 删除收藏夹
func (d *FavoritesDao) DeleteFavoriteById(fId uint) error {
	return d.DB.Where("id=?", fId).Delete(&Favorite{}).Error
}

func (d *FavoritesDao) DeleteFavoriteByUserIdAndProductId(userId uint, productId uint) error {
	db := d.DB.Where("user_id=?", userId)
	if productId != 0 {
		db = db.Where("product_id=?", productId)
	}
	return db.Delete(&Favorite{}).Error
}
