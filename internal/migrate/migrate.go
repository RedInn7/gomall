package migrate

import (
	"context"

	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/admin"
	"github.com/RedInn7/gomall/internal/carousel"
	"github.com/RedInn7/gomall/internal/cart"
	"github.com/RedInn7/gomall/internal/category"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/coupon"
	"github.com/RedInn7/gomall/internal/favorite"
	"github.com/RedInn7/gomall/internal/groupbuy"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/notice"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/payment"
	"github.com/RedInn7/gomall/internal/preorder"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/redpacket"
	"github.com/RedInn7/gomall/internal/skill"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// Run 执行全部表结构的自动迁移。
// 放在独立的组合包里，由启动流程在 dao.InitMySQL 之后调用，
// 避免基础 db 包反向依赖各领域的 model。
func Run() error {
	db := dao.NewDBClient(context.Background())
	if err := db.Set("gorm:table_options", "charset=utf8mb4").
		AutoMigrate(
			&user.User{}, &favorite.Favorite{},
			&order.Order{}, &admin.Admin{}, &address.Address{},
			&cart.Cart{}, &category.Category{}, &carousel.Carousel{},
			&notice.Notice{}, &product.Product{},
			&product.ProductImg{}, &skill.SkillProduct{},
			&skill.SkillProduct2MQ{},
			&coupon.CouponBatch{}, &coupon.UserCoupon{},
			&redpacket.RedPacket{}, &redpacket.RedPacketClaim{},
			&promo.PromoRule{}, &promo.PromoRelease{},
			&groupbuy.GroupbuyGroup{}, &groupbuy.GroupbuyMember{},
			&preorder.ProductPreorder{},
			&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{},
			&clearing.PaymentAnomalyTransition{},
			&payment.XcashPaymentIntent{}, &payment.XcashWebhookReceipt{},
		); err != nil {
		return err
	}
	// account_code 是新增列：AutoMigrate 会把历史行填成默认 user_wallet。
	// user_id=0 的历史流水实际属于旧系统账户，统一回填 legacy_system，避免与真实用户钱包混淆。
	return db.Model(&money.AccountTransaction{}).
		Where("user_id = ? AND account_code = ?", money.ExternalClearingUserID, money.AccountCodeUserWallet).
		Update("account_code", money.AccountCodeLegacySystem).Error
}
