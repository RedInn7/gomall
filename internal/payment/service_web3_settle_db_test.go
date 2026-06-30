package payment

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/cache"
)

// Web3 链上结算端到端测试。SettleConfirmedOrder 由 listener 收到链上 PaymentConfirmed 事件触发：
// 先校验链上 buyer == 签名 park 阶段写入 Redis 的钱包地址(防越权结算)，再在事务内校验金额覆盖、
// 给卖家入账、平台清算账户记对手方 debit，走共享结算尾段。此前只有金额换算纯函数被测，
// 结算事务本身(buyer 绑定 / 金额不足 / 幂等)端到端未覆盖。
//
// 复用 service_stripe_settle_test.go 的 assertExternalSettlement / seedExternalReservation 及
// service_test.go 的 setup/seed harness。USDC 1:1：payable 4000 分 → 40000000 base units(6 位精度)。

const testWeb3Buyer = "0x1111111111111111111111111111111111111111"

func setWeb3Env(t *testing.T) {
	t.Helper()
	t.Setenv(envWeb3PayToken, "usdc")
	t.Setenv(envWeb3USDCDecimals, "6")
	t.Setenv(envWeb3ToleranceBps, "50")
}

// onchainUSDC 把法币分按 USDC 6 位精度换算成链上最小单位字符串。
func onchainUSDC(cents int64) string {
	return strconv.FormatInt(cents*10000, 10)
}

func TestSettleConfirmedOrder_Success(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()
	setWeb3Env(t)

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	seedExternalReservation(t, ctx, fx)
	// 签名 park 阶段写入的钱包地址：结算时链上 buyer 必须与之一致。
	if err := cache.SetWeb3Pending(ctx, fx.OrderID, testWeb3Buyer); err != nil {
		t.Fatalf("set web3 pending: %v", err)
	}

	if err := GetWeb3SettleSrv().SettleConfirmedOrder(ctx, fx.OrderID, testWeb3Buyer, onchainUSDC(fx.TotalCents)); err != nil {
		t.Fatalf("SettleConfirmedOrder: %v", err)
	}
	assertExternalSettlement(t, db, ctx, fx, money.BizTypeWeb3Pay)

	// 结算成功后 park 占位应被清掉。
	parked, err := cache.RedisClient.HGet(ctx, cache.Web3PendingKey(fx.OrderID), "addr").Result()
	if err == nil && parked != "" {
		t.Fatalf("结算后 park 占位应被删除, 仍有 addr=%s", parked)
	}
}

func TestSettleConfirmedOrder_BuyerMismatchRejected(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()
	setWeb3Env(t)

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	// park 写的是 buyer A，链上事件 buyer 是 B → 拒绝结算(防凑金额越权结算他人订单)。
	if err := cache.SetWeb3Pending(ctx, fx.OrderID, testWeb3Buyer); err != nil {
		t.Fatalf("set web3 pending: %v", err)
	}
	other := "0x2222222222222222222222222222222222222222"
	err := GetWeb3SettleSrv().SettleConfirmedOrder(ctx, fx.OrderID, other, onchainUSDC(fx.TotalCents))
	if !errors.Is(err, ErrWeb3BuyerMismatch) {
		t.Fatalf("buyer 不匹配应 ErrWeb3BuyerMismatch, got %v", err)
	}
	assertPaymentUntouched(t, db, fx)
}

func TestSettleConfirmedOrder_PendingMissingRejected(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()
	setWeb3Env(t)

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	// 不写 park 占位(过期 / 从未签名)→ 无可信绑定来源，拒绝结算。
	err := GetWeb3SettleSrv().SettleConfirmedOrder(ctx, fx.OrderID, testWeb3Buyer, onchainUSDC(fx.TotalCents))
	if !errors.Is(err, ErrWeb3PendingMissing) {
		t.Fatalf("park 占位缺失应 ErrWeb3PendingMissing, got %v", err)
	}
	assertPaymentUntouched(t, db, fx)
}

func TestSettleConfirmedOrder_AmountInsufficientRollsBack(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()
	setWeb3Env(t)

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	if err := cache.SetWeb3Pending(ctx, fx.OrderID, testWeb3Buyer); err != nil {
		t.Fatalf("set web3 pending: %v", err)
	}
	// 链上确认金额明显低于应付(超出容差)→ 拒结、事务回滚。
	low := onchainUSDC(fx.TotalCents - 1000) // 少付 $10，远超 0.5% 容差
	err := GetWeb3SettleSrv().SettleConfirmedOrder(ctx, fx.OrderID, testWeb3Buyer, low)
	if !errors.Is(err, ErrWeb3AmountMismatch) {
		t.Fatalf("金额不足应 ErrWeb3AmountMismatch, got %v", err)
	}
	assertPaymentUntouched(t, db, fx)
}

func TestSettleConfirmedOrder_AlreadySettledIdempotent(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()
	setWeb3Env(t)

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	if err := cache.SetWeb3Pending(ctx, fx.OrderID, testWeb3Buyer); err != nil {
		t.Fatalf("set web3 pending: %v", err)
	}
	// 订单已结算到 WaitShip：链上事件重投到达时幂等返回 nil，不重复入账。
	if err := db.Model(&orderpkg.Order{}).Where("id=?", fx.OrderID).
		Update("type", consts.OrderWaitShip).Error; err != nil {
		t.Fatalf("seed settled order: %v", err)
	}
	if err := GetWeb3SettleSrv().SettleConfirmedOrder(ctx, fx.OrderID, testWeb3Buyer, onchainUSDC(fx.TotalCents)); err != nil {
		t.Fatalf("重复结算应幂等返回 nil, got %v", err)
	}
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
