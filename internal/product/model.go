package product

import (
	"strconv"

	"github.com/RedInn7/gomall/internal/shared/dbmodel"

	"github.com/RedInn7/gomall/repository/cache"
)

// Product 商品模型
type Product struct {
	dbmodel.Model
	Name          string `gorm:"size:255;index"`
	CategoryID    uint   `gorm:"not null"`
	Title         string
	Info          string `gorm:"size:1000"`
	ImgPath       string
	Price         string
	DiscountPrice string
	OnSale        bool `gorm:"default:false"`
	Num           int
	BossID        uint
	BossName      string
	BossAvatar    string
}

func (Product) TableName() string {
	return "product"
}

// View 获取点击数
func (product *Product) View() uint64 {
	countStr, _ := cache.RedisClient.Get(cache.RedisContext, cache.ProductViewKey(product.ID)).Result()
	count, _ := strconv.ParseUint(countStr, 10, 64)
	return count
}

// AddView 增加商品点击数及排行榜计数
func (product *Product) AddView() {
	cache.RedisClient.Incr(cache.RedisContext, cache.ProductViewKey(product.ID))
	cache.RedisClient.ZIncrBy(cache.RedisContext, cache.RankKey, 1, strconv.Itoa(int(product.ID)))
}
