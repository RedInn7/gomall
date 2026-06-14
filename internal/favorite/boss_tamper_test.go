package favorite

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// TestFavoriteCreate_IgnoresTamperedBoss 回归用例：收藏时客户端篡改 boss_id 不再生效。
// 商品真实卖家是 fx.Boss；请求体把 BossId 改成 999。落库的收藏行卖家必须是真实卖家，
// 避免脏 boss 污染卖家维度统计 / 后续加购下单。
func TestFavoriteCreate_IgnoresTamperedBoss(t *testing.T) {
	initLogForTest()
	restore := initFavoriteTestConfig()
	defer restore()
	db, cleanup := setupSQLiteForFavorite(t)
	defer cleanup()

	fx := seedFavoriteFixture(t, db)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.Buyer.ID})

	if _, err := GetFavoriteSrv().FavoriteCreate(ctx,
		&FavoriteCreateReq{ProductId: fx.Product.ID, BossId: 999}); err != nil {
		t.Fatalf("FavoriteCreate: %v", err)
	}

	var row Favorite
	if err := db.Where("user_id=? AND product_id=?", fx.Buyer.ID, fx.Product.ID).First(&row).Error; err != nil {
		t.Fatalf("load favorite: %v", err)
	}
	if row.BossID != fx.Boss.ID {
		t.Fatalf("favorite.BossID = %d, want %d（商品真实卖家，拒绝客户端 999）", row.BossID, fx.Boss.ID)
	}
}
