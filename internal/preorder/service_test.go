package preorder

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
)

// 测试场景下需要 AES MoneySecret，否则 EncryptMoney / DecryptMoney 走空 key panic。
// 测试不依赖具体配置文件，只要 MoneySecret 非空即可。
var preorderTestConfigOnce sync.Once

func initPreorderTestConfig() {
	preorderTestConfigOnce.Do(func() {
		// 优先用项目自带 yaml；找不到就构造一份最小的内存版，保证 MoneySecret 非空。
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				// yaml 不存在 / 解析失败 → 构造最小 config
				conf.Config = &conf.Conf{
					EncryptSecret: &conf.EncryptSecret{
						MoneySecret: "PreorderTestMoneySecret16Byte",
					},
				}
			}
			if conf.Config != nil && conf.Config.EncryptSecret.MoneySecret == "" {
				conf.Config.EncryptSecret.MoneySecret = "PreorderTestMoneySecret16Byte"
			}
		}()
		conf.InitConfigForTest(&re)
	})
}

// 预售流程的集成式测试：sqlite in-memory + Redis (DB 15)。
// Redis 不可用整组 skip；sqlite 不可用整组 skip（CGO 关闭场景）。
// 配套覆盖 5 个故事：
//   1) 定金期外 -> 82001
//   2) 付定金 -> reserved += 1 + PreorderStage=DepositPaid
//   3) 尾款期内付尾款 -> reserved->sold + Type=WaitShip + PreorderStage=FinalPaid
//   4) 尾款逾期 cron -> Forfeited + reserved 归还
//   5) 定金期内取消 -> 库存归还 + PreorderStage 重置 + Type=Closed

func setupSQLiteForPreorder(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	// 每个 case 独立 DSN，避免 cache=shared 时残留 user_name 唯一约束冲突。
	dsn := "file:preorder-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(
		&user.User{}, &order.Order{}, &product.Product{},
		&ProductPreorder{}, &model.OutboxEvent{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// 替换全局 _db。dao 通过 NewDBClient(ctx) 拿到 *gorm.DB，背后是 _db.WithContext。
	prev := dao.SetTestDB(db)
	return db, func() {
		dao.SetTestDB(prev)
	}
}

func setupRedisForPreorder(t *testing.T) func() {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis 127.0.0.1:6379 不可用：%v", err)
	}
	prev := cache.RedisClient
	cache.RedisClient = c
	return func() {
		c.FlushDB(context.Background())
		c.Close()
		cache.RedisClient = prev
	}
}

// seedPreorderFixture 把"商品 + 预售配置 + 用户余额密文 + 库存桶"一次性塞好。
// 返回值是夹具上下文，每个 case 取需要的字段。
type preorderFixture struct {
	UserID    uint
	BossID    uint
	ProductID uint
	Key       string
	Deposit   int64
	Final     int64
	DepStart  time.Time
	DepEnd    time.Time
	FinalEnd  time.Time
}

func seedPreorder(t *testing.T, db *gorm.DB, depositWindow, finalWindow time.Duration) preorderFixture {
	t.Helper()
	initPreorderTestConfig()
	const key = "abc123"

	// 用户 + 商家：跳过 AES 直接给原文，单测里 user.Money 字段就是明文
	// 与生产口径不一致，但本测试目标是状态机 + 库存，不是 AES 正确性，独立单测覆盖即可。
	// 为了让 EncryptMoney/DecryptMoney 可逆，这里手动用一次 EncryptMoney 写入。
	buyer := &user.User{UserName: "u-preorder", Money: "1000000"}
	buyer.Money, _ = buyer.EncryptMoney(key)
	if err := db.Create(buyer).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	boss := &user.User{UserName: "boss-preorder", Money: "100"}
	boss.Money, _ = boss.EncryptMoney(key)
	if err := db.Create(boss).Error; err != nil {
		t.Fatalf("create boss: %v", err)
	}

	product := &product.Product{Name: "preorder-item", Num: 10, BossID: boss.ID}
	if err := db.Create(product).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	now := time.Now()
	depStart := now.Add(-depositWindow)
	depEnd := now.Add(depositWindow)
	finalEnd := depEnd.Add(finalWindow)
	pp := &ProductPreorder{
		ProductID:      product.ID,
		DepositCents:   1000,
		FinalCents:     2000,
		DepositStartAt: depStart,
		DepositEndAt:   depEnd,
		FinalEndAt:     finalEnd,
		ShipAt:         finalEnd.Add(72 * time.Hour),
	}
	if err := db.Create(pp).Error; err != nil {
		t.Fatalf("create preorder: %v", err)
	}

	if err := cache.InitStock(context.Background(), product.ID, int64(product.Num)); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	return preorderFixture{
		UserID:    buyer.ID,
		BossID:    boss.ID,
		ProductID: product.ID,
		Key:       key,
		Deposit:   pp.DepositCents,
		Final:     pp.FinalCents,
		DepStart:  depStart,
		DepEnd:    depEnd,
		FinalEnd:  finalEnd,
	}
}

func ensureSnowflakeForPreorder() {
	defer func() { _ = recover() }()
	snowflake.InitSnowflake(11)
}

func TestPreorder_OutOfDepositWindowRejected(t *testing.T) {
	initLogForTest()
	initPreorderTestConfig()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()
	_ = db

	// 定金期 -1h ~ -10min（已结束）
	now := time.Now()
	buyer := &user.User{UserName: "u-out", Money: "1000000"}
	key := "abc123"
	buyer.Money, _ = buyer.EncryptMoney(key)
	if err := db.Create(buyer).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	boss := &user.User{UserName: "boss-out", Money: "0"}
	boss.Money, _ = boss.EncryptMoney(key)
	if err := db.Create(boss).Error; err != nil {
		t.Fatalf("create boss: %v", err)
	}
	product := &product.Product{Name: "out-of-window", Num: 5, BossID: boss.ID}
	if err := db.Create(product).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}
	pp := &ProductPreorder{
		ProductID:      product.ID,
		DepositCents:   100,
		FinalCents:     200,
		DepositStartAt: now.Add(-1 * time.Hour),
		DepositEndAt:   now.Add(-10 * time.Minute),
		FinalEndAt:     now.Add(50 * time.Minute),
		ShipAt:         now.Add(24 * time.Hour),
	}
	if err := db.Create(pp).Error; err != nil {
		t.Fatalf("create preorder: %v", err)
	}
	if err := cache.InitStock(context.Background(), product.ID, 5); err != nil {
		t.Fatalf("init stock: %v", err)
	}

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: buyer.ID})
	_, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: product.ID, BossID: boss.ID, AddressID: 1, Key: key,
	})
	if err == nil {
		t.Fatal("应当返回不在定金期错误")
	}
	if CodeOf(err) != e.ErrPreorderNotInDepositWindow {
		t.Fatalf("expect code %d, got %d (err=%v)", e.ErrPreorderNotInDepositWindow, CodeOf(err), err)
	}

	// 库存桶不应被改动
	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), product.ID)
	if avail != 5 || reserved != 0 {
		t.Fatalf("库存不该改动，got avail=%d reserved=%d", avail, reserved)
	}
}

func TestPreorder_PayDepositLocksStockAndAdvancesStage(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})
	resp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: fx.BossID, AddressID: 1, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayDeposit: %v", err)
	}
	if resp.PreorderStage != PreorderStageDepositPaid {
		t.Fatalf("stage = %d, want %d", resp.PreorderStage, PreorderStageDepositPaid)
	}
	if resp.OrderType != consts.OrderWaitPay {
		t.Fatalf("order type = %d, want WaitPay", resp.OrderType)
	}

	// 库存：available 9 / reserved 1
	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), fx.ProductID)
	if avail != 9 || reserved != 1 {
		t.Fatalf("avail/reserved = %d/%d, want 9/1", avail, reserved)
	}

	// DB：order 已落 + deposit_paid_at 非空
	var dbOrder order.Order
	if err := db.First(&dbOrder, resp.OrderID).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	if dbOrder.PreorderStage != PreorderStageDepositPaid {
		t.Fatalf("dbOrder stage = %d", dbOrder.PreorderStage)
	}
	if dbOrder.DepositPaidAt == nil {
		t.Fatal("deposit_paid_at 应该被写入")
	}

	// outbox：preorder.deposit.paid 必须落表
	var outboxCount int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "preorder.deposit.paid", dbOrder.ID).
		Count(&outboxCount)
	if outboxCount != 1 {
		t.Fatalf("expect 1 outbox row for deposit.paid, got %d", outboxCount)
	}
}

func TestPreorder_PayFinalConsumesStockAndAdvancesState(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})

	depResp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: fx.BossID, AddressID: 1, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayDeposit: %v", err)
	}

	// 推进时钟：跳到尾款期内（depEnd + 1min）
	origNow := nowFn
	nowFn = func() time.Time { return fx.DepEnd.Add(1 * time.Minute) }
	defer func() { nowFn = origNow }()

	finalResp, err := GetPreorderSrv().PayFinal(ctx, &PreorderFinalReq{
		OrderID: depResp.OrderID, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayFinal: %v", err)
	}
	if finalResp.PreorderStage != PreorderStageFinalPaid {
		t.Fatalf("stage = %d, want %d", finalResp.PreorderStage, PreorderStageFinalPaid)
	}
	if finalResp.OrderType != consts.OrderWaitShip {
		t.Fatalf("order type = %d, want WaitShip", finalResp.OrderType)
	}

	// 库存：reserved -> 0；available 仍 9（commit 桶不还）
	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), fx.ProductID)
	if avail != 9 || reserved != 0 {
		t.Fatalf("avail/reserved = %d/%d, want 9/0", avail, reserved)
	}

	// product.Num 真扣
	var product product.Product
	if err := db.First(&product, fx.ProductID).Error; err != nil {
		t.Fatalf("load product: %v", err)
	}
	if product.Num != 9 {
		t.Fatalf("product.Num = %d, want 9", product.Num)
	}

	// outbox 多一条
	var cnt int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "preorder.final.paid", depResp.OrderID).
		Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expect 1 outbox row for final.paid, got %d", cnt)
	}
}

func TestPreorder_FinalWindowExpiredCronForfeits(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})

	depResp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: fx.BossID, AddressID: 1, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayDeposit: %v", err)
	}

	// 推进时钟：FinalEnd + 5min（逾期），cron 走人
	origNow := nowFn
	nowFn = func() time.Time { return fx.FinalEnd.Add(5 * time.Minute) }
	defer func() { nowFn = origNow }()

	if err := GetPreorderSrv().ForfeitDepositsForUnpaidFinals(context.Background()); err != nil {
		t.Fatalf("ForfeitDepositsForUnpaidFinals: %v", err)
	}

	var dbOrder order.Order
	if err := db.First(&dbOrder, depResp.OrderID).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	if dbOrder.PreorderStage != PreorderStageForfeited {
		t.Fatalf("stage = %d, want Forfeited(3)", dbOrder.PreorderStage)
	}
	if dbOrder.Type != consts.OrderClosed {
		t.Fatalf("type = %d, want Closed", dbOrder.Type)
	}

	// 库存归还：reserved -> 0，available 回到 10
	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), fx.ProductID)
	if avail != 10 || reserved != 0 {
		t.Fatalf("avail/reserved = %d/%d, want 10/0", avail, reserved)
	}

	// outbox：preorder.forfeited
	var cnt int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "preorder.forfeited", depResp.OrderID).
		Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expect 1 outbox row for forfeited, got %d", cnt)
	}

	// 二次跑 cron：幂等，order 状态不再变
	if err := GetPreorderSrv().ForfeitDepositsForUnpaidFinals(context.Background()); err != nil {
		t.Fatalf("forfeit second run: %v", err)
	}
	var cnt2 int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "preorder.forfeited", depResp.OrderID).
		Count(&cnt2)
	if cnt2 != 1 {
		t.Fatalf("二次 cron 不应再写 outbox，got %d", cnt2)
	}
}

func TestPreorder_CancelInDepositWindowRefundsAndReleases(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})

	depResp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: fx.BossID, AddressID: 1, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayDeposit: %v", err)
	}

	// 仍在定金期内取消
	cancelResp, err := GetPreorderSrv().CancelPreorderInDepositWindow(ctx,
		&PreorderCancelReq{OrderID: depResp.OrderID, Key: fx.Key})
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelResp.PreorderStage != PreorderStageNone {
		t.Fatalf("stage = %d, want None", cancelResp.PreorderStage)
	}
	if cancelResp.OrderType != consts.OrderClosed {
		t.Fatalf("type = %d, want Closed", cancelResp.OrderType)
	}

	// 库存：available 回到 10
	avail, reserved, _ := cache.GetStockSnapshot(context.Background(), fx.ProductID)
	if avail != 10 || reserved != 0 {
		t.Fatalf("avail/reserved = %d/%d, want 10/0", avail, reserved)
	}

	// outbox：preorder.cancelled
	var cnt int64
	db.Model(&model.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "preorder.cancelled", depResp.OrderID).
		Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expect 1 outbox row for cancelled, got %d", cnt)
	}

	// 用户余额已退回（明文写回 1000000 - 1000 + 1000 = 1000000）
	var u user.User
	if err := db.First(&u, fx.UserID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	money, err := u.DecryptMoney(fx.Key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if money != 1000000 {
		t.Fatalf("user money = %d, want 1000000", money)
	}
}

func TestPreorder_CancelAfterDepositEndForfeits(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})

	depResp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{
		ProductID: fx.ProductID, BossID: fx.BossID, AddressID: 1, Key: fx.Key,
	})
	if err != nil {
		t.Fatalf("PayDeposit: %v", err)
	}

	// 推进到尾款期内：定金期已过
	origNow := nowFn
	nowFn = func() time.Time { return fx.DepEnd.Add(1 * time.Minute) }
	defer func() { nowFn = origNow }()

	_, err = GetPreorderSrv().CancelPreorderInDepositWindow(ctx,
		&PreorderCancelReq{OrderID: depResp.OrderID, Key: fx.Key})
	if err == nil {
		t.Fatal("预售期结束后取消应被拒")
	}
	if CodeOf(err) != e.ErrPreorderForfeitedDeposit {
		t.Fatalf("expect code %d, got %d (err=%v)",
			e.ErrPreorderForfeitedDeposit, CodeOf(err), err)
	}
}

func TestPreorder_FinalRequiresDepositPaid(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	ensureSnowflakeForPreorder()

	fx := seedPreorder(t, db, time.Hour, time.Hour)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.UserID})

	// 直接造一个 stage=None 的订单，绕过 PayDeposit
	order := &order.Order{
		UserID: fx.UserID, ProductID: fx.ProductID, BossID: fx.BossID,
		Num: 1, Money: fx.Deposit + fx.Final, Type: consts.OrderWaitPay,
		OrderNum: 99001,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	origNow := nowFn
	nowFn = func() time.Time { return fx.DepEnd.Add(1 * time.Minute) }
	defer func() { nowFn = origNow }()

	_, err := GetPreorderSrv().PayFinal(ctx, &PreorderFinalReq{
		OrderID: order.ID, Key: fx.Key,
	})
	if err == nil {
		t.Fatal("应当报未付定金错误")
	}
	if CodeOf(err) != e.ErrPreorderDepositNotPaid {
		t.Fatalf("expect code %d, got %d", e.ErrPreorderDepositNotPaid, CodeOf(err))
	}
}

// TestPreorder_ShowReturnsPhase 验证公共接口对四阶段的 phase 输出
func TestPreorder_ShowReturnsPhase(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPreorder(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPreorder(t)
	defer dcleanup()
	_ = db

	fx := seedPreorder(t, db, time.Hour, time.Hour)

	// deposit 阶段
	resp, err := GetPreorderSrv().ShowPreorder(context.Background(), fx.ProductID)
	if err != nil {
		t.Fatalf("ShowPreorder: %v", err)
	}
	if resp.Phase != "deposit" {
		t.Fatalf("phase = %s, want deposit", resp.Phase)
	}
	if resp.TotalCents != fx.Deposit+fx.Final {
		t.Fatalf("total = %d", resp.TotalCents)
	}

	// 推到 final 阶段
	origNow := nowFn
	nowFn = func() time.Time { return fx.DepEnd.Add(1 * time.Minute) }
	resp, _ = GetPreorderSrv().ShowPreorder(context.Background(), fx.ProductID)
	if resp.Phase != "final" {
		nowFn = origNow
		t.Fatalf("phase = %s, want final", resp.Phase)
	}

	// 推到失效阶段
	nowFn = func() time.Time { return fx.FinalEnd.Add(time.Minute) }
	resp, _ = GetPreorderSrv().ShowPreorder(context.Background(), fx.ProductID)
	nowFn = origNow
	if resp.Phase != "forfeited" {
		t.Fatalf("phase = %s, want forfeited", resp.Phase)
	}
}

// safeCallPreorder 兜住 dao 在某些极端情况下的 nil-pointer panic
func safeCallPreorder(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("recovered panic")
		}
	}()
	return fn()
}

var _ = safeCallPreorder

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
