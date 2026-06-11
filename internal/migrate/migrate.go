package migrate

import (
	"context"

	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
)

// Run 执行全部表结构的自动迁移。
// 放在独立的组合包里，由启动流程在 dao.InitMySQL 之后调用，
// 避免基础 db 包反向依赖各领域的 model。
func Run() error {
	db := dao.NewDBClient(context.Background())
	return db.Set("gorm:table_options", "charset=utf8mb4").
		AutoMigrate(
			&model.User{}, &model.Favorite{},
			&model.Order{}, &model.Admin{}, &address.Address{},
			&model.Cart{}, &model.Category{}, &model.Carousel{},
			&model.Notice{}, &model.Product{},
			&model.ProductImg{}, &model.SkillProduct{},
			&model.SkillProduct2MQ{},
			&model.CouponBatch{}, &model.UserCoupon{},
			&model.RedPacket{}, &model.RedPacketClaim{},
			&model.PromoRule{},
			&model.GroupbuyGroup{}, &model.GroupbuyMember{},
			&model.ProductPreorder{},
		)
}
