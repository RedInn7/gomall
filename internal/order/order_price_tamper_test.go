package order

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestOrderCreate_IgnoresTamperedClientMoney 回归用例：客户端伪造低价不再生效。
// 商品权威单价 100 元（10000 分），请求体把 Money 篡改成 1 分；下单后订单单价与实付
// 必须以商品表为准（10000），证明 req.Money 已彻底退出计费链路。
func TestOrderCreate_IgnoresTamperedClientMoney(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-tamper", 10000) // DiscountPrice = "100.00"

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 99})
	resp, err := GetOrderSrv().OrderCreate(ctx, &OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		Money:     1, // 攻击者把单价改成 1 分
		AddressID: 1,
		BossID:    1,
	})
	if err != nil {
		t.Fatalf("OrderCreate: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}

	// 单价必须来自商品表（10000），而不是被篡改的 1
	if resp.Money != 10000 {
		t.Fatalf("order.Money = %d, want 10000（以商品表为准，拒绝客户端篡改）", resp.Money)
	}
	// 无满减规则 → 实付即原价；绝不能是 1*1=1
	if resp.FinalCents != 10000 {
		t.Fatalf("order.FinalCents = %d, want 10000", resp.FinalCents)
	}
}
