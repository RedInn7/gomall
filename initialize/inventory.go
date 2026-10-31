package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/inventory"
)

// InitInventory 启动时在后台把 DB 中的 product.num 同步到 Redis 库存桶，ctx 取消时提前结束
// （没建的桶下单时会按 DB 补建）。
func InitInventory(ctx context.Context) {
	background.Go(func() {
		if err := inventory.SeedFromDB(ctx, 500); err != nil {
			util.LogrusObj.Errorf("inventory seed failed: %v", err)
			return
		}
		util.LogrusObj.Infoln("inventory seeded from DB")
	})
}
