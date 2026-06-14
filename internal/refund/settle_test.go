package refund

import (
	"context"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/db/dao"
)

var settleCfgOnce sync.Once

// AES 加密余额需要非空 MoneySecret，缺配置时给最小内存配置。
func initSettleConfig() {
	settleCfgOnce.Do(func() {
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				conf.Config = &conf.Conf{
					EncryptSecret: &conf.EncryptSecret{MoneySecret: "MoneyTestSecret16Byte"},
				}
			}
			if conf.Config != nil && conf.Config.EncryptSecret.MoneySecret == "" {
				conf.Config.EncryptSecret.MoneySecret = "MoneyTestSecret16Byte"
			}
		}()
		conf.InitConfigForTest(&re)
	})
}

func setupSQLiteForSettle(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:refund-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&user.User{}, &orderpkg.Order{}, &product.Product{}, &money.AccountTransaction{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func seedMoneyUser(t *testing.T, db *gorm.DB, name, cents string) *user.User {
	t.Helper()
	initSettleConfig()
	u := &user.User{UserName: name, Money: cents}
	enc, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("EncryptMoney: %v", err)
	}
	u.Money = enc
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func decBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("reload user %d: %v", id, err)
	}
	v, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt user %d: %v", id, err)
	}
	return v
}

// TestRefund_SettleCreditsBuyerDebitsSellerRestocksAndIdempotent 覆盖退款结算闭环：
// 买家 +实付、卖家 -实付、库存回补，且重复投递不二次入账（幂等）。
func TestRefund_SettleCreditsBuyerDebitsSellerRestocksAndIdempotent(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForSettle(t)
	defer cleanup()

	buyer := seedMoneyUser(t, db, "buyer", "1000")
	seller := seedMoneyUser(t, db, "seller", "5000")

	prod := &product.Product{Name: "p", Num: 7, BossID: seller.ID}
	if err := db.Create(prod).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	const amount int64 = 1500 // Money*Num = 300*5，无促销
	o := &orderpkg.Order{
		UserID:    buyer.ID,
		ProductID: prod.ID,
		BossID:    seller.ID,
		Num:       5,
		OrderNum:  88888,
		Type:      consts.OrderRefunded, // ApproveRefund 已推进到终态
		Money:     300,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	ctx := context.Background()
	if err := GetRefundSrv().SettleRefund(ctx, o.ID); err != nil {
		t.Fatalf("SettleRefund: %v", err)
	}

	if got := decBalance(t, db, buyer.ID); got != 1000+amount {
		t.Fatalf("买家余额 = %d, want %d", got, 1000+amount)
	}
	if got := decBalance(t, db, seller.ID); got != 5000-amount {
		t.Fatalf("卖家余额 = %d, want %d", got, 5000-amount)
	}

	var reloaded product.Product
	if err := db.First(&reloaded, prod.ID).Error; err != nil {
		t.Fatalf("reload product: %v", err)
	}
	if reloaded.Num != 12 { // 7 + 5
		t.Fatalf("库存 = %d, want 12", reloaded.Num)
	}

	var credit, debit int64
	db.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND direction=? AND biz_type=?", o.ID, money.DirectionCredit, money.BizTypeRefund).Count(&credit)
	db.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND direction=? AND biz_type=?", o.ID, money.DirectionDebit, money.BizTypeRefund).Count(&debit)
	if credit != 1 || debit != 1 {
		t.Fatalf("流水条数 credit=%d debit=%d, want 1/1", credit, debit)
	}

	// 幂等：重复投递不得二次入账，余额 / 库存 / 流水均不变。
	if err := GetRefundSrv().SettleRefund(ctx, o.ID); err != nil {
		t.Fatalf("SettleRefund(重复): %v", err)
	}
	if got := decBalance(t, db, buyer.ID); got != 1000+amount {
		t.Fatalf("幂等后买家余额 = %d, want %d", got, 1000+amount)
	}
	if got := decBalance(t, db, seller.ID); got != 5000-amount {
		t.Fatalf("幂等后卖家余额 = %d, want %d", got, 5000-amount)
	}
	if err := db.First(&reloaded, prod.ID).Error; err != nil {
		t.Fatalf("reload product: %v", err)
	}
	if reloaded.Num != 12 {
		t.Fatalf("幂等后库存 = %d, want 12（不得二次回补）", reloaded.Num)
	}

	var total int64
	db.Model(&money.AccountTransaction{}).Where("ref_order_id=?", o.ID).Count(&total)
	if total != 2 {
		t.Fatalf("幂等后总流水 = %d, want 2", total)
	}
}

// TestRefund_SettleSkipsNonRefundedOrder 未获批（非 Refunded）的订单不结算、不入账。
func TestRefund_SettleSkipsNonRefundedOrder(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForSettle(t)
	defer cleanup()

	buyer := seedMoneyUser(t, db, "b2", "1000")
	seller := seedMoneyUser(t, db, "s2", "5000")
	o := &orderpkg.Order{
		UserID: buyer.ID, BossID: seller.ID, Num: 1, OrderNum: 99999,
		Type: consts.OrderRefunding, Money: 100, // 尚未获批
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	if err := GetRefundSrv().SettleRefund(context.Background(), o.ID); err != nil {
		t.Fatalf("SettleRefund: %v", err)
	}
	if got := decBalance(t, db, buyer.ID); got != 1000 {
		t.Fatalf("未获批不应退钱，买家余额 = %d, want 1000", got)
	}
	var total int64
	db.Model(&money.AccountTransaction{}).Where("ref_order_id=?", o.ID).Count(&total)
	if total != 0 {
		t.Fatalf("未获批不应有流水, got %d", total)
	}
}
