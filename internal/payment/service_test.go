package payment

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// PayDown 的资金流测试：sqlite in-memory 承接事务回滚语义，Redis (DB 15) 承接
// 支付成功后的 reserved 桶核销。覆盖 5 条路径：
//   1) 正向支付：订单 WaitPay→WaitShip、买家扣款、卖家进账、库存核销、买家名下复制商品、outbox 落 order.paid
//   2) 余额不足：整个事务回滚，订单/余额/库存全部保持原样
//   3) 支付密码长度不合法：进事务前拒绝
//   4) 支付密码错误（长度合法但密钥不对）：拒付且无任何状态变化
//   5) 重复支付：订单已不在 WaitPay 态时直接拒绝
// 失败路径不会触达 Redis（CommitReservation 只在事务提交后调用），因此 2~5 不依赖 Redis。

// EncryptMoney/DecryptMoney 依赖 conf.Config.EncryptSecret.MoneySecret，
// 测试不绑定具体配置文件，密钥非空即可。
var paymentTestConfigOnce sync.Once

func initPaymentTestConfig() {
	paymentTestConfigOnce.Do(func() {
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				conf.Config = &conf.Conf{
					EncryptSecret: &conf.EncryptSecret{
						MoneySecret: "PaymentTestMoneySecret16Byte",
					},
				}
			}
			if conf.Config != nil && conf.Config.EncryptSecret.MoneySecret == "" {
				conf.Config.EncryptSecret.MoneySecret = "PaymentTestMoneySecret16Byte"
			}
		}()
		conf.InitConfigForTest(&re)
	})
}

func setupSQLiteForPayment(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:payment-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(
		&user.User{}, &orderpkg.Order{}, &product.Product{}, &outbox.OutboxEvent{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func setupRedisForPayment(t *testing.T) func() {
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

// paymentFixture: 买家 / 卖家 / 商品 / 一张 WaitPay 订单（单价 2000 分 x 2 件 = 4000 分）。
type paymentFixture struct {
	BuyerID    uint
	BossID     uint
	ProductID  uint
	OrderID    uint
	Key        string
	BuyerCents int64
	BossCents  int64
	TotalCents int64
	StockNum   int
	OrderNum   int
}

func seedPayment(t *testing.T, db *gorm.DB, buyerCents int64) paymentFixture {
	t.Helper()
	initPaymentTestConfig()
	const key = "abc123"
	const bossCents = int64(500)

	buyer := &user.User{UserName: "buyer-" + t.Name(), Money: strconv.FormatInt(buyerCents, 10)}
	enc, err := buyer.EncryptMoney(key)
	if err != nil {
		t.Fatalf("encrypt buyer money: %v", err)
	}
	buyer.Money = enc
	if err := db.Create(buyer).Error; err != nil {
		t.Fatalf("create buyer: %v", err)
	}

	boss := &user.User{UserName: "boss-" + t.Name(), Money: strconv.FormatInt(bossCents, 10)}
	enc, err = boss.EncryptMoney(key)
	if err != nil {
		t.Fatalf("encrypt boss money: %v", err)
	}
	boss.Money = enc
	if err := db.Create(boss).Error; err != nil {
		t.Fatalf("create boss: %v", err)
	}

	prod := &product.Product{
		Name:       "pay-item",
		CategoryID: 3,
		Title:      "pay-item-title",
		Info:       "pay-item-info",
		Price:      "20",
		Num:        10,
		OnSale:     true,
		BossID:     boss.ID,
		BossName:   boss.UserName,
	}
	if err := db.Create(prod).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	ord := &orderpkg.Order{
		UserID:    buyer.ID,
		ProductID: prod.ID,
		BossID:    boss.ID,
		AddressID: 1,
		Num:       2,
		Money:     2000,
		Type:      consts.OrderWaitPay,
		OrderNum:  880001,
	}
	if err := db.Create(ord).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}

	return paymentFixture{
		BuyerID:    buyer.ID,
		BossID:     boss.ID,
		ProductID:  prod.ID,
		OrderID:    ord.ID,
		Key:        key,
		BuyerCents: buyerCents,
		BossCents:  bossCents,
		TotalCents: ord.Money * int64(ord.Num),
		StockNum:   prod.Num,
		OrderNum:   ord.Num,
	}
}

// payDownRecovering 兜住底层 AES 解填充在密钥错误时抛出的 panic
// （secret 库 SecretDecrypt 失败直接 panic，事务由 gorm 回滚后向上传递），
// 统一折叠成 error 方便断言"拒付"。
func payDownRecovering(ctx context.Context, req *PaymentDownReq) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("paydown panic: %v", r)
		}
	}()
	_, err = GetPaymentSrv().PayDown(ctx, req)
	return
}

// assertPaymentUntouched 校验失败路径下买卖双方/订单/库存/outbox 全部保持原样。
func assertPaymentUntouched(t *testing.T, db *gorm.DB, fx paymentFixture) {
	t.Helper()
	var ord orderpkg.Order
	if err := db.First(&ord, fx.OrderID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if ord.Type != consts.OrderWaitPay {
		t.Fatalf("order type = %d, want WaitPay（事务应回滚）", ord.Type)
	}

	var buyer user.User
	if err := db.First(&buyer, fx.BuyerID).Error; err != nil {
		t.Fatalf("reload buyer: %v", err)
	}
	if money, err := buyer.DecryptMoney(fx.Key); err != nil || money != fx.BuyerCents {
		t.Fatalf("buyer money = %d (err=%v), want %d", money, err, fx.BuyerCents)
	}

	var boss user.User
	if err := db.First(&boss, fx.BossID).Error; err != nil {
		t.Fatalf("reload boss: %v", err)
	}
	if money, err := boss.DecryptMoney(fx.Key); err != nil || money != fx.BossCents {
		t.Fatalf("boss money = %d (err=%v), want %d", money, err, fx.BossCents)
	}

	var prod product.Product
	if err := db.First(&prod, fx.ProductID).Error; err != nil {
		t.Fatalf("reload product: %v", err)
	}
	if prod.Num != fx.StockNum {
		t.Fatalf("product num = %d, want %d", prod.Num, fx.StockNum)
	}

	var cnt int64
	db.Model(&outbox.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "order.paid", fx.OrderID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("失败路径不应写 order.paid outbox，got %d", cnt)
	}
}

func TestPayDown_Success(t *testing.T) {
	initLogForTest()
	rcleanup := setupRedisForPayment(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	ctx := context.Background()
	if err := cache.InitStock(ctx, fx.ProductID, int64(fx.StockNum)); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	// 下单链路已把 2 件挪进 reserved 桶，支付后 CommitReservation 应核销掉
	if err := cache.ReserveStock(ctx, fx.ProductID, int64(fx.OrderNum)); err != nil {
		t.Fatalf("reserve stock: %v", err)
	}

	uctx := ctl.NewContext(ctx, &ctl.UserInfo{Id: fx.BuyerID})
	if _, err := GetPaymentSrv().PayDown(uctx, &PaymentDownReq{OrderId: fx.OrderID, Key: fx.Key}); err != nil {
		t.Fatalf("PayDown: %v", err)
	}

	// 订单推进到待发货
	var ord orderpkg.Order
	if err := db.First(&ord, fx.OrderID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if ord.Type != consts.OrderWaitShip {
		t.Fatalf("order type = %d, want WaitShip", ord.Type)
	}

	// 买家扣款 / 卖家进账，金额对得上总价
	var buyer user.User
	if err := db.First(&buyer, fx.BuyerID).Error; err != nil {
		t.Fatalf("reload buyer: %v", err)
	}
	if money, err := buyer.DecryptMoney(fx.Key); err != nil || money != fx.BuyerCents-fx.TotalCents {
		t.Fatalf("buyer money = %d (err=%v), want %d", money, err, fx.BuyerCents-fx.TotalCents)
	}
	var boss user.User
	if err := db.First(&boss, fx.BossID).Error; err != nil {
		t.Fatalf("reload boss: %v", err)
	}
	if money, err := boss.DecryptMoney(fx.Key); err != nil || money != fx.BossCents+fx.TotalCents {
		t.Fatalf("boss money = %d (err=%v), want %d", money, err, fx.BossCents+fx.TotalCents)
	}

	// 卖家商品库存真扣
	var prod product.Product
	if err := db.First(&prod, fx.ProductID).Error; err != nil {
		t.Fatalf("reload product: %v", err)
	}
	if prod.Num != fx.StockNum-fx.OrderNum {
		t.Fatalf("product num = %d, want %d", prod.Num, fx.StockNum-fx.OrderNum)
	}

	// 买家名下复制出一份下架状态的同名商品（二手交易模型）
	var copied product.Product
	if err := db.Where("boss_id = ? AND name = ?", fx.BuyerID, "pay-item").First(&copied).Error; err != nil {
		t.Fatalf("买家名下应复制出商品: %v", err)
	}
	if copied.Num != fx.OrderNum || copied.OnSale {
		t.Fatalf("复制商品 num=%d onSale=%v, want num=%d onSale=false", copied.Num, copied.OnSale, fx.OrderNum)
	}

	// outbox 落一条 order.paid，投递交给 publisher 异步处理
	var cnt int64
	db.Model(&outbox.OutboxEvent{}).
		Where("routing_key=? AND aggregate_id=?", "order.paid", fx.OrderID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expect 1 outbox row for order.paid, got %d", cnt)
	}

	// Redis reserved 桶核销：available 不变（下单时已扣），reserved 清零
	avail, reserved, _ := cache.GetStockSnapshot(ctx, fx.ProductID)
	if avail != int64(fx.StockNum-fx.OrderNum) || reserved != 0 {
		t.Fatalf("avail/reserved = %d/%d, want %d/0", avail, reserved, fx.StockNum-fx.OrderNum)
	}
}

func TestPayDown_InsufficientBalanceRollsBack(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	// 余额 1000 分 < 总价 4000 分
	fx := seedPayment(t, db, 1000)
	uctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.BuyerID})

	_, err := GetPaymentSrv().PayDown(uctx, &PaymentDownReq{OrderId: fx.OrderID, Key: fx.Key})
	if err == nil {
		t.Fatal("余额不足应当拒付")
	}
	assertPaymentUntouched(t, db, fx)
}

func TestPayDown_WrongKeyLengthRejected(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	uctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.BuyerID})

	_, err := GetPaymentSrv().PayDown(uctx, &PaymentDownReq{OrderId: fx.OrderID, Key: "abc"})
	if err == nil {
		t.Fatalf("密码长度非 %d 应当拒付", consts.EncryptMoneyKeyLength)
	}
	assertPaymentUntouched(t, db, fx)
}

func TestPayDown_WrongKeyRejectedNoStateChange(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	uctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.BuyerID})

	// 长度合法但密钥错误：解出来要么是乱码（按余额不足拒付），
	// 要么解填充失败由 payDownRecovering 折叠成 error；两种结果都必须是拒付。
	err := payDownRecovering(uctx, &PaymentDownReq{OrderId: fx.OrderID, Key: "zzz999"})
	if err == nil {
		t.Fatal("错误支付密码应当拒付")
	}
	assertPaymentUntouched(t, db, fx)
}

func TestPayDown_AlreadyPaidRejected(t *testing.T) {
	initLogForTest()
	db, dcleanup := setupSQLiteForPayment(t)
	defer dcleanup()

	fx := seedPayment(t, db, 100000)
	if err := db.Model(&orderpkg.Order{}).Where("id=?", fx.OrderID).
		Update("type", consts.OrderWaitShip).Error; err != nil {
		t.Fatalf("seed order type: %v", err)
	}
	uctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.BuyerID})

	_, err := GetPaymentSrv().PayDown(uctx, &PaymentDownReq{OrderId: fx.OrderID, Key: fx.Key})
	if err == nil {
		t.Fatal("非 WaitPay 态订单应当拒绝重复支付")
	}

	// 订单保持已支付态，资金不动
	var ord orderpkg.Order
	if err := db.First(&ord, fx.OrderID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if ord.Type != consts.OrderWaitShip {
		t.Fatalf("order type = %d, want WaitShip", ord.Type)
	}
	var buyer user.User
	if err := db.First(&buyer, fx.BuyerID).Error; err != nil {
		t.Fatalf("reload buyer: %v", err)
	}
	if money, err := buyer.DecryptMoney(fx.Key); err != nil || money != fx.BuyerCents {
		t.Fatalf("buyer money = %d (err=%v), want %d", money, err, fx.BuyerCents)
	}
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
