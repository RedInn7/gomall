package cart

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestCartCreate_IgnoresTamperedBoss 回归用例：加购时客户端篡改 boss_id 不再生效。
// 商品真实卖家是 realBoss；请求体把 BossID 改成 999。落库的购物车行卖家必须是 realBoss，
// 防止脏 boss 顺着后续结算链路把货款引到错误账户。
func TestCartCreate_IgnoresTamperedBoss(t *testing.T) {
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const realBoss = uint(7)
	p := seedCartProduct(t, db, realBoss)

	const userID = uint(42)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID})
	if _, err := GetCartSrv().CartCreate(ctx, &CartCreateReq{ProductId: p.ID, BossID: 999}); err != nil {
		t.Fatalf("CartCreate: %v", err)
	}

	var row Cart
	if err := db.Where("user_id=? AND product_id=?", userID, p.ID).First(&row).Error; err != nil {
		t.Fatalf("load cart: %v", err)
	}
	if row.BossID != realBoss {
		t.Fatalf("cart.BossID = %d, want %d（商品真实卖家，拒绝客户端 999）", row.BossID, realBoss)
	}
}
