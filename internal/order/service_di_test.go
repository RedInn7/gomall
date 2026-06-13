package order

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// promoCalcStub 满减依赖替身：按注入的错误驱动下单链路的两个降级分支。
type promoCalcStub struct {
	calcResp *promo.PromoApplyResp
	calcErr  error
	applyErr error
}

func (s *promoCalcStub) CalculateBestDiscount(ctx context.Context, items []promo.CartItem) (*promo.PromoApplyResp, error) {
	return s.calcResp, s.calcErr
}

func (s *promoCalcStub) ApplyDiscountInTx(tx *gorm.DB, orderID, ruleID uint, discountCents int64) error {
	return s.applyErr
}

// TestOrderCreate_PromoDownDegrades 满减引擎计算失败时下单不受阻：
// 订单按原价落库、不带满减字段、无 promo.applied 事件。
func TestOrderCreate_PromoDownDegrades(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-promo-down", 10000)
	srv := NewOrderSrv(&promoCalcStub{calcErr: errors.New("promo unavailable")})

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	resp, err := srv.OrderCreate(ctx, &OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     10000,
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("promo 故障不应阻断下单: %v", err)
	}
	order := resp
	if order.PromoRuleID != 0 || order.PromoDiscountCents != 0 {
		t.Fatalf("降级订单不应带满减字段: rule=%d discount=%d", order.PromoRuleID, order.PromoDiscountCents)
	}
	if order.FinalCents != 10000 {
		t.Fatalf("FinalCents = %d, want 原价 10000", order.FinalCents)
	}

	var promoEvt int64
	db.Model(&outbox.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "promo.applied", order.ID).Count(&promoEvt)
	if promoEvt != 0 {
		t.Fatalf("降级路径不应产生 promo.applied 事件, got %d", promoEvt)
	}
}

// TestOrderCreate_BudgetExhaustedFallsBack 预算扣减失败（耗尽）时事务内改写回原价。
func TestOrderCreate_BudgetExhaustedFallsBack(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-budget-out", 10000)
	srv := NewOrderSrv(&promoCalcStub{
		calcResp: &promo.PromoApplyResp{
			RuleID:        77,
			RuleName:      "满 80 减 10",
			OriginalCents: 10000,
			DiscountCents: 1000,
			FinalCents:    9000,
		},
		applyErr: promo.ErrPromoBudgetExhausted,
	})

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	resp, err := srv.OrderCreate(ctx, &OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     10000,
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("预算耗尽应降级而非失败: %v", err)
	}
	order := resp
	if order.PromoRuleID != 0 || order.PromoDiscountCents != 0 || order.FinalCents != 10000 {
		t.Fatalf("预算耗尽订单应回落原价: rule=%d discount=%d final=%d",
			order.PromoRuleID, order.PromoDiscountCents, order.FinalCents)
	}

	// 落库的订单行同样应是无折扣状态
	var dbOrder Order
	if err := db.First(&dbOrder, order.ID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if dbOrder.PromoRuleID != 0 || dbOrder.FinalCents != 10000 {
		t.Fatalf("DB 订单未回落: rule=%d final=%d", dbOrder.PromoRuleID, dbOrder.FinalCents)
	}
}
