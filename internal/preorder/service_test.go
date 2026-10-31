package preorder

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/money"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

const testPayKey = "123456"

// 自买自卖：定金从自己钱包出去又回到自己钱包，定金期内取消后余额必须回到原值。
// 旧实现退款时先扣"卖家"，再用买家扣款前的旧快照写回，覆盖掉那笔扣款，每取消一次凭空多一笔定金。
func TestSelfDealPreorderCancelRestoresBalance(t *testing.T) {
	db := openPreorderTestDB(t)
	stubPreorderRedis(t)
	now := time.Now()
	prevNow := nowFn
	nowFn = func() time.Time { return now }
	t.Cleanup(func() { nowFn = prevNow })

	const seller uint = 7
	seedPreorderUser(t, db, seller, 10000)
	p := &product.Product{Name: "console", CategoryID: 1, Num: 5, OnSale: true, BossID: seller}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.Create(&ProductPreorder{
		ProductID: p.ID, DepositCents: 3000, FinalCents: 7000,
		DepositStartAt: now.Add(-time.Hour), DepositEndAt: now.Add(time.Hour), FinalEndAt: now.Add(2 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("seed preorder: %v", err)
	}

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: seller})
	resp, err := GetPreorderSrv().PayDeposit(ctx, &PreorderDepositReq{ProductID: p.ID, Key: testPayKey})
	if err != nil {
		t.Fatalf("pay deposit: %v", err)
	}
	if got := preorderUserBalance(t, db, seller); got != 10000 {
		t.Fatalf("自买自卖付定金后余额应不变, got %d", got)
	}
	if _, err := GetPreorderSrv().CancelPreorderInDepositWindow(ctx, &PreorderCancelReq{OrderID: resp.OrderID, Key: testPayKey}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := preorderUserBalance(t, db, seller); got != 10000 {
		t.Fatalf("自买自卖取消后余额应回到 10000, got %d", got)
	}
	// 余额不变也可能是退款根本没执行，再确认退款的成对流水确实落了。
	var refunds int64
	if err := db.Model(&money.AccountTransaction{}).
		Where("ref_order_id = ? AND biz_type = ?", resp.OrderID, money.BizTypePreorderRefund).
		Count(&refunds).Error; err != nil {
		t.Fatalf("count refund ledger: %v", err)
	}
	if refunds != 2 {
		t.Fatalf("退款应落卖家 debit + 买家 credit 两条流水, got %d", refunds)
	}
}

// okLuaHook 不连真实 Redis：库存 Lua 返回 1，支付密码限次的滑动窗口脚本返回 {1, 1}，其余命令直接放行。
type okLuaHook struct{}

func (okLuaHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (okLuaHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if c, ok := cmd.(*redis.Cmd); ok {
			// 滑动窗口脚本是 1 个 key + 4 个参数：evalsha sha 1 key a b c d 共 8 个 args
			if len(c.Args()) == 8 {
				c.SetVal([]interface{}{int64(1), int64(1)})
				return nil
			}
			c.SetVal(int64(1))
		}
		return nil
	}
}
func (okLuaHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func stubPreorderRedis(t *testing.T) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(okLuaHook{})
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
}

func openPreorderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	prevConf, prevLog := conf.Config, util.LogrusObj
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "preorder-test-secret"}}
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
	snowflake.InitSnowflake(1)

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:preorder-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	prevDB := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(prevDB)
		conf.Config, util.LogrusObj = prevConf, prevLog
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&user.User{}, &product.Product{}, &order.Order{}, &ProductPreorder{},
		&money.AccountTransaction{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedPreorderUser(t *testing.T, db *gorm.DB, id uint, balance int64) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("pre-u%d", id), Money: strconv.FormatInt(balance, 10)}
	u.ID = id
	cipher, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt balance: %v", err)
	}
	u.Money = cipher
	if err := u.SetMoneyPassword(testPayKey); err != nil {
		t.Fatalf("set pay key: %v", err)
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func preorderUserBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	bal, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt balance: %v", err)
	}
	return bal
}
