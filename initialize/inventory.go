package initialize

import (
	"context"

	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/service/inventory"
)

// InitInventory 启动时把 DB 中的 product.num 同步到 Redis 库存桶
func InitInventory(ctx context.Context) {
	if err := inventory.SeedFromDB(ctx, 500); err != nil {
		util.LogrusObj.Errorf("inventory seed failed: %v", err)
		return
	}
	util.LogrusObj.Infoln("inventory seeded from DB")
}
