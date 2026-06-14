package groupbuy

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestCreateGroup_RejectsForeignAddress 回归用例：用别人的 address_id 发起拼团被拒。
// 地址属于用户 7；用户 8 拿它发起拼团（价格合法），必须失败。
func TestCreateGroup_RejectsForeignAddress(t *testing.T) {
	initLogForTest()
	rcleanup := setupGroupbuyRedis(t)
	defer rcleanup()
	db, dcleanup := setupGroupbuyDB(t)
	defer dcleanup()
	ensureSnowflakeGroupbuy()

	p := seedGroupbuyProduct(t, db, "100.00", 7, 10)
	victimAddr := seedGroupbuyAddress(t, db, 7) // 地址归用户 7

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 8})
	// 价格 8000 合法，但地址不属于发起人 → 必须被拒
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 8, p.ID, 3, 8000, 0, 7, victimAddr); err == nil {
		t.Fatal("用他人地址发起拼团应被拒，却成功了")
	}
}
