package payment

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/cache"
)

// Stripe 结算路径端到端测试。settleStripeOrder 与钱包路径的差别在于：买家资金来自外部卡组织、
// 不扣内部钱包，故只给卖家入账、平台清算账户(user 0)记对手方 debit；并在结算前断言会话实付
// 金额/币种与订单应付一致。这些分支(尤其金额不符拒付)此前只有浅层 config/签名测试覆盖。
//
// 复用 service_test.go 的 setupSQLiteForPayment / setupRedisForPayment / seedPayment 等 harness。

// assertExternalSettlement 校验"外部资金入口"渠道(Stripe/Web3)结算成功后的统一终态：
// 订单→WaitShip、买家余额不动、卖家入账 total、库存真扣、买家名下复制商品、outbox 落 order.paid、
// 复式台账(卖家 credit + 平台清算账户 user0 debit)、Redis 预留核销。bizType 区分渠道账本类型。
func assertExternalSettlement(t *testing.T, db *gorm.DB, ctx context.Context, fx paymentFixture, bizType string) {
	t.Helper()

	var ord orderpkg.Order
	if err := db.First(&ord, fx.OrderID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if ord.Type != consts.OrderWaitShip {
		t.Fatalf("order type = %d, want WaitShip", ord.Type)
	}

	// 买家是外部付款，内部钱包不动。
	var buyer user.User
	if err := db.First(&buyer, fx.BuyerID).Error; err != nil {
		t.Fatalf("reload buyer: %v", err)
	}
	if m, err := buyer.DecryptMoney(); err != nil || m != fx.BuyerCents {
		t.Fatalf("buyer money = %d (err=%v), want 不变 %d", m, err, fx.BuyerCents)
	}

	// 卖家入账 total。
	var boss user.User
	if err := db.First(&boss, fx.BossID).Error; err != nil {
		t.Fatalf("reload boss: %v", err)
	}
	if m, err := boss.DecryptMoney(); err != nil || m != fx.BossCents+fx.TotalCents {
		t.Fatalf("boss money = %d (err=%v), want %d", m, err, fx.BossCents+fx.TotalCents)
	}

	// 库存真扣 + 买家名下复制下架商品。
	var prod product.Product
	if err := db.First(&prod, fx.ProductID).Error; err != nil {
		t.Fatalf("reload product: %v", err)
	}
	if prod.Num != fx.StockNum-fx.OrderNum {
		t.Fatalf("product num = %d, want %d", prod.Num, fx.StockNum-fx.OrderNum)
	}
	var copied product.Product
	if err := db.Where("boss_id = ? AND name = ?", fx.BuyerID, "pay-item").First(&copied).Error; err != nil {
		t.Fatalf("买家名下应复制出商品: %v", err)
	}
	if copied.Num != fx.OrderNum || copied.OnSale {
		t.Fatalf("复制商品 num=%d onSale=%v, want num=%d onSale=false", copied.Num, copied.OnSale, fx.OrderNum)
	}

	// outbox 落一条 order.paid。
	var cnt int64
	db.Model(&outbox.OutboxEvent{}).Where("routing_key=? AND aggregate_id=?", "order.paid", fx.OrderID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expect 1 outbox row for order.paid, got %d", cnt)
	}

	// 复式记账：卖家 credit(余额对得上) + 平台清算账户 user0 debit。
	var credit money.AccountTransaction
	if err := db.Where("ref_order_id=? AND direction=?", fx.OrderID, money.DirectionCredit).First(&credit).Error; err != nil {
		t.Fatalf("卖家入账流水缺失: %v", err)
	}
	if credit.UserID != fx.BossID || credit.AmountCents != fx.TotalCents || credit.BalanceAfterCents != fx.BossCents+fx.TotalCents || credit.BizType != bizType {
		t.Fatalf("credit 流水 user=%d amount=%d after=%d biz=%s, want user=%d amount=%d after=%d biz=%s",
			credit.UserID, credit.AmountCents, credit.BalanceAfterCents, credit.BizType,
			fx.BossID, fx.TotalCents, fx.BossCents+fx.TotalCents, bizType)
	}
	var debit money.AccountTransaction
	if err := db.Where("ref_order_id=? AND direction=?", fx.OrderID, money.DirectionDebit).First(&debit).Error; err != nil {
		t.Fatalf("平台清算账户对手方流水缺失: %v", err)
	}
	if debit.UserID != money.ExternalClearingUserID || debit.AmountCents != fx.TotalCents {
		t.Fatalf("debit 流水 user=%d amount=%d, want user=%d amount=%d",
			debit.UserID, debit.AmountCents, money.ExternalClearingUserID, fx.TotalCents)
	}

	// Redis 预留核销：available 不变(下单时已扣)，reserved 清零。
	avail, reserved, _ := cache.GetStockSnapshot(ctx, fx.ProductID)
	if avail != int64(fx.StockNum-fx.OrderNum) || reserved != 0 {
		t.Fatalf("avail/reserved = %d/%d, want %d/0", avail, reserved, fx.StockNum-fx.OrderNum)
	}
}

// seedExternalReservation 模拟下单链路：把 OrderNum 件挪进 Redis reserved 桶，结算后应被核销。
func seedExternalReservation(t *testing.T, ctx context.Context, fx paymentFixture) {
	t.Helper()
	if err := cache.InitStock(ctx, fx.ProductID, int64(fx.StockNum)); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	if err := cache.ReserveStock(ctx, fx.ProductID, int64(fx.OrderNum)); err != nil {
		t.Fatalf("reserve stock: %v", err)
	}
}

func TestSettleStripeOrder_Success(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	seedExternalReservation(t, ctx, fx)

	// 金额/币种与订单应付(TotalCents, 默认 usd)一致 → 结算成功。
	if err := GetStripePaymentSrv().settleStripeOrder(ctx, fx.OrderID, fx.BuyerID, fx.TotalCents, "usd"); err != nil {
		t.Fatalf("settleStripeOrder: %v", err)
	}
	assertExternalSettlement(t, db, ctx, fx, money.BizTypeStripePay)
}

func TestSettleStripeOrder_AmountMismatchRollsBack(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	// 会话实付比应付少 1 分：签名只证明来自 Stripe，金额对不上订单必须拒结、整体回滚。
	err := GetStripePaymentSrv().settleStripeOrder(context.Background(), fx.OrderID, fx.BuyerID, fx.TotalCents-1, "usd")
	if !errors.Is(err, ErrStripeAmountMismatch) {
		t.Fatalf("金额不符应 ErrStripeAmountMismatch, got %v", err)
	}
	assertPaymentUntouched(t, db, fx)
}

func TestSettleStripeOrder_CurrencyMismatchRollsBack(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	// 金额对但币种不符(eur≠usd)：同样拒结、回滚。
	err := GetStripePaymentSrv().settleStripeOrder(context.Background(), fx.OrderID, fx.BuyerID, fx.TotalCents, "eur")
	if !errors.Is(err, ErrStripeAmountMismatch) {
		t.Fatalf("币种不符应 ErrStripeAmountMismatch, got %v", err)
	}
	assertPaymentUntouched(t, db, fx)
}

func TestSettleStripeOrder_AlreadySettledIdempotent(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	// 订单已被结算到 WaitShip：webhook 重投到达时应幂等返回 nil，不重复入账。
	if err := db.Model(&orderpkg.Order{}).Where("id=?", fx.OrderID).
		Update("type", consts.OrderWaitShip).Error; err != nil {
		t.Fatalf("seed settled order: %v", err)
	}
	if err := GetStripePaymentSrv().settleStripeOrder(context.Background(), fx.OrderID, fx.BuyerID, fx.TotalCents, "usd"); err != nil {
		t.Fatalf("重复结算应幂等返回 nil, got %v", err)
	}
	// 卖家余额保持原样(没有重复入账)，台账零写入。
	var boss user.User
	if err := db.First(&boss, fx.BossID).Error; err != nil {
		t.Fatalf("reload boss: %v", err)
	}
	if m, err := boss.DecryptMoney(); err != nil || m != fx.BossCents {
		t.Fatalf("boss money = %d (err=%v), want 不变 %d", m, err, fx.BossCents)
	}
	var cnt int64
	db.Model(&money.AccountTransaction{}).Where("ref_order_id=?", fx.OrderID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("幂等跳过不应写台账, got %d", cnt)
	}
}
