package carousel

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/repository/db/dao"
)

type CarouselDao struct {
	*gorm.DB
}

func NewCarouselDao(ctx context.Context) *CarouselDao {
	return &CarouselDao{dao.NewDBClient(ctx)}
}

func NewCarouselDaoByDB(db *gorm.DB) *CarouselDao {
	return &CarouselDao{db}
}

func (d *CarouselDao) ListCarousel() (r []*ListCarouselResp, err error) {
	err = d.DB.Model(&Carousel{}).
		Select("id, img_path, product_id, UNIX_TIMESTAMP(created_at)").
		Find(&r).Error

	return
}
