package migrate

import (
	"context"

	"gorm.io/gorm"

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
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/skill"
	"github.com/RedInn7/gomall/internal/user"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// Run 执行全部表结构的自动迁移。
// 放在独立的组合包里，由启动流程在 dao.InitMySQL 之后调用，
// 避免基础 db 包反向依赖各领域的 model。
func Run() error {
	db := dao.NewDBClient(context.Background())
	// 必须在 AutoMigrate 之前看：迁移完列就都在了，分不出哪些是这次新建的。
	fulfillmentMissing := order.MissingFulfillmentColumns(db)
	if err := db.Set("gorm:table_options", "charset=utf8mb4").
		AutoMigrate(
			&user.User{}, &favorite.Favorite{},
			&order.Order{}, &admin.Admin{}, &address.Address{},
			&cart.Cart{}, &category.Category{}, &carousel.Carousel{},
			&notice.Notice{}, &product.Product{},
			&product.ProductImg{}, &skill.SkillProduct{}, &skill.SkillUser{},
			&coupon.CouponBatch{}, &coupon.UserCoupon{},
			&redpacket.RedPacket{}, &redpacket.RedPacketClaim{},
			&promo.PromoRule{}, &promo.PromoRelease{},
			&groupbuy.GroupbuyGroup{}, &groupbuy.GroupbuyMember{}, &groupbuy.GroupbuyNotice{},
			&preorder.ProductPreorder{},
			&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{},
			&clearing.PaymentAnomalyTransition{},
			&payment.XcashPaymentIntent{}, &payment.XcashWebhookReceipt{},
			&outbox.OutboxEvent{},
		); err != nil {
		return err
	}
	if err := order.BackfillFulfillmentColumns(db, fulfillmentMissing); err != nil {
		return err
	}
	return backfillAccountCodes(db)
}

// backfillAccountCodes 把历史系统侧流水改到正确的 account_code，可重复执行。
// 滚动发布期间旧实例仍会把 groupbuy_* 写到 legacy_system，新实例启动时只回填一次；
// 旧实例全部下线后需要再重启一次（或手动执行同样的两条 UPDATE）收尾，否则 groupbuy_escrow 对账不平。
func backfillAccountCodes(db *gorm.DB) error {
	// account_code 是新增列：AutoMigrate 会把历史行填成默认 user_wallet。
	// user_id=0 的历史流水实际属于旧系统账户，统一回填 legacy_system，避免与真实用户钱包混淆。
	if err := db.Model(&money.AccountTransaction{}).
		Where("user_id = ? AND account_code = ?", money.ExternalClearingUserID, money.AccountCodeUserWallet).
		Update("account_code", money.AccountCodeLegacySystem).Error; err != nil {
		return err
	}
	// 拼团托管从 legacy_system 拆到独立的 groupbuy_escrow：历史 groupbuy_* 系统侧流水一并改口径，
	// 否则迁移前收进来的在途款在新账户上只有出账没有入账，对账对不平。
	res := db.Model(&money.AccountTransaction{}).
		Where("user_id = ? AND account_code = ? AND biz_type IN ?",
			money.ExternalClearingUserID, money.AccountCodeLegacySystem,
			[]string{groupbuy.BizTypeGroupBuyPay, groupbuy.BizTypeGroupBuySettle, groupbuy.BizTypeGroupBuyRefund}).
		Update("account_code", money.AccountCodeGroupbuyEscrow)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		util.LogrusObj.Infof("migrate: %d 条 groupbuy_* 流水从 legacy_system 回填到 groupbuy_escrow", res.RowsAffected)
	}
	// 红包托管历史流水从 legacy_system 拆到 redpacket_escrow，和拼团托管分开对账。幂等，可重复执行。
	return db.Model(&money.AccountTransaction{}).
		Where("user_id = ? AND account_code = ? AND biz_type IN ?", money.ExternalClearingUserID, money.AccountCodeLegacySystem,
			[]string{money.BizTypeRedPacketSend, money.BizTypeRedPacketClaim, money.BizTypeRedPacketRefund}).
		Update("account_code", money.AccountCodeRedPacketEscrow).Error
}
