package preorder

import (
	"context"
	"testing"
	"time"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestPreorder_PayDepositRejectsForeignAddress 回归用例：用别人的 address_id 付定金被拒。
// seedPreorder 给买家(fx.UserID)种了 id=1 的地址；攻击者(另一个用户)拿这个地址 id 付定金，
// 必须在扣款前被地址归属校验拦下。
func TestPreorder_PayDepositRejectsForeignAddress(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour) // 地址 id=1 归买家 fx.UserID

	const attacker = uint(99999)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: attacker})
	if _, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: fx.BossID, AddressID: 1, Key: fx.Key,
	}); err == nil {
		t.Fatal("用他人地址付定金应被拒，却成功了")
	}
}
