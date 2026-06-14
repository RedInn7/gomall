package order

import (
	"context"
	"testing"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/repository/cache"
)

// TestOrderCreate_RejectsForeignAddress 回归用例：用别人的 address_id 下单被拒。
// 地址属于用户 7；攻击者用户 8 拿这个地址 id 下单，必须失败，证明 address_id 不再被盲信。
func TestOrderCreate_RejectsForeignAddress(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForOrder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflakeForOrder()

	product := seedOrderProduct(t, db, "p-foreign-addr", 10000)
	victimAddr := seedOrderAddress(t, db, 7) // 地址归用户 7

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 8}) // 攻击者是用户 8
	if _, err := GetOrderSrv().OrderCreate(ctx, &OrderCreateReq{
		ProductID: product.ID,
		Num:       1,
		AddressID: victimAddr,
	}); err == nil {
		t.Fatal("用他人地址 id 下单应被拒，却成功了")
	}
}

// TestOrderEnqueue_RejectsForeignAddress 异步入队同样在投递前拦住他人地址，避免白白预扣库存。
func TestOrderEnqueue_RejectsForeignAddress(t *testing.T) {
	initLogForTest()
	cleanup := setupTestRedisForService(t)
	defer cleanup()
	db, dcleanup := setupSQLiteForOrder(t)
	defer dcleanup()
	ensureSnowflake()

	_, _, restore := installAsyncTestDeps(t)
	defer restore()

	const pid = 7001
	// 必须给商品初始化库存：否则去掉地址校验后流程会先卡在"库存未初始化"而报错，
	// 断言便误判通过（假绿）。种上库存，让本用例真正卡在地址归属校验上。
	if err := cache.InitStock(context.Background(), pid, 5); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	victimAddr := seedOrderAddress(t, db, 7)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 8})
	if _, err := GetOrderSrv().OrderEnqueue(ctx, &OrderCreateReq{
		ProductID: pid,
		Num:       1,
		AddressID: victimAddr,
	}); err == nil {
		t.Fatal("异步入队用他人地址应被拒，却成功了")
	}
}
