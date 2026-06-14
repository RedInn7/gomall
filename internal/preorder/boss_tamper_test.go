package preorder

import (
	"context"
	"testing"
	"time"

	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestPreorder_PayDepositIgnoresTamperedBoss 回归用例：付定金时客户端篡改 boss_id 不再生效。
// 商品真实卖家是 fx.BossID；请求体把 BossID 改成不存在的攻击者账户 999。
// 定金必须仍记到真实卖家头上——订单落库 BossID、以及真实卖家钱包入账都证明这一点。
// 反证：若 999 真被采用，debitUser 查不到该用户会直接报错，PayDeposit 不可能成功。
func TestPreorder_PayDepositIgnoresTamperedBoss(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})
	resp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: 999, AddressID: 1, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayDeposit 应忽略篡改的 boss_id 并成功: %v", err)
	}

	// 订单落库的卖家必须是商品真实卖家，而不是 999
	var dbOrder order.Order
	if e := db.First(&dbOrder, resp.OrderID).Error; e != nil {
		t.Fatalf("load order: %v", e)
	}
	if dbOrder.BossID != fx.BossID {
		t.Fatalf("order.BossID = %d, want %d（商品真实卖家，拒绝客户端 999）", dbOrder.BossID, fx.BossID)
	}

	// 真实卖家钱包应入账定金：初始 100 + 定金 1000 = 1100
	var boss user.User
	if e := db.First(&boss, fx.BossID).Error; e != nil {
		t.Fatalf("load boss: %v", e)
	}
	bossMoney, e := boss.DecryptMoney()
	if e != nil {
		t.Fatalf("decrypt boss money: %v", e)
	}
	if bossMoney != 100+fx.Deposit {
		t.Fatalf("boss 钱包 = %d, want %d（定金记到真实卖家，而非攻击者）", bossMoney, 100+fx.Deposit)
	}
}
