package migrate

import (
	"context"

	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/admin"
	"github.com/RedInn7/gomall/internal/carousel"
	"github.com/RedInn7/gomall/internal/cart"
	"github.com/RedInn7/gomall/internal/category"
	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/favorite"
	"github.com/RedInn7/gomall/internal/notice"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/redpacket"
	"github.com/RedInn7/gomall/internal/skill"
	"github.com/RedInn7/gomall/internal/user"
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
			&user.User{}, &favorite.Favorite{},
			&model.Order{}, &admin.Admin{}, &address.Address{},
			&cart.Cart{}, &category.Category{}, &carousel.Carousel{},
			&notice.Notice{}, &product.Product{},
			&product.ProductImg{}, &skill.SkillProduct{},
			&skill.SkillProduct2MQ{},
			&coupon.CouponBatch{}, &coupon.UserCoupon{},
			&redpacket.RedPacket{}, &redpacket.RedPacketClaim{},
			&model.PromoRule{},
			&model.GroupbuyGroup{}, &model.GroupbuyMember{},
			&model.ProductPreorder{},
		)
}
