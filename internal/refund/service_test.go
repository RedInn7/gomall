package refund

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/promo"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/service/events"
)

func TestSettleRefundChoosesFundingSourceByClearingState(t *testing.T) {
	tests := []struct {
		name             string
		clearingStatus   string
		sellerBefore     int64
		wantSellerAfter  int64
		wantBizType      string
		wantDebitAccount string
		wantDebitUserID  uint
		fromType         uint
		wantRestock      bool
	}{
		{
			name:             "before settlement refund comes from escrow",
			clearingStatus:   clearing.StatusCleared,
			sellerBefore:     50,
			wantSellerAfter:  50,
			wantBizType:      money.BizTypeEscrowRefund,
			wantDebitAccount: money.AccountCodeMerchantEscrow,
			wantDebitUserID:  money.ExternalClearingUserID,
			fromType:         consts.OrderWaitShip,
			wantRestock:      true, // 没发货，货还在仓库
		},
		{
			name:             "after settlement refund comes from seller",
			clearingStatus:   clearing.StatusSettled,
			sellerBefore:     350,
			wantSellerAfter:  50,
			wantBizType:      money.BizTypeRefund,
			wantDebitAccount: money.AccountCodeUserWallet,
			wantDebitUserID:  22,
			fromType:         consts.OrderCompleted,
			wantRestock:      false, // 货在买家手里，等确认退货入库再回补
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openRefundTestDB(t)
			restocks := stubRestockRedis(t)
			seedRefundUser(t, db, 11, 100)
			seedRefundUser(t, db, 22, tt.sellerBefore)
			p := &product.Product{Name: "book", CategoryID: 1, Num: 0, BossID: 22}
			p.ID = 33
			if err := db.Create(p).Error; err != nil {
				t.Fatalf("seed product: %v", err)
			}
			o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: p.ID, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderRefunded, RefundFromType: tt.fromType}
			o.ID = 44
			if err := db.Create(o).Error; err != nil {
				t.Fatalf("seed order: %v", err)
			}
			record := &clearing.PaymentClearing{
				OrderID: o.ID, BuyerID: o.UserID, SellerID: o.BossID,
				Channel: clearing.ChannelWallet, GrossCents: 300, NetCents: 300,
				Currency: "USD", Status: tt.clearingStatus, ClearedAt: time.Now(),
			}
			if err := db.Create(record).Error; err != nil {
				t.Fatalf("seed clearing: %v", err)
			}

			if err := GetRefundSrv().SettleRefund(context.Background(), o.ID); err != nil {
				t.Fatalf("first refund settlement: %v", err)
			}
			if err := GetRefundSrv().SettleRefund(context.Background(), o.ID); err != nil {
				t.Fatalf("duplicate refund settlement: %v", err)
			}

			if got := refundUserBalance(t, db, 11); got != 400 {
				t.Fatalf("buyer balance got %d want 400", got)
			}
			if got := refundUserBalance(t, db, 22); got != tt.wantSellerAfter {
				t.Fatalf("seller balance got %d want %d", got, tt.wantSellerAfter)
			}
			var debit money.AccountTransaction
			if err := db.Where("ref_order_id=? AND direction=? AND biz_type=?", o.ID, money.DirectionDebit, tt.wantBizType).First(&debit).Error; err != nil {
				t.Fatalf("load refund debit: %v", err)
			}
			if debit.UserID != tt.wantDebitUserID || debit.AccountCode != tt.wantDebitAccount || debit.AmountCents != 300 {
				t.Fatalf("unexpected refund debit: %+v", debit)
			}
			var gotProduct product.Product
			if err := db.First(&gotProduct, p.ID).Error; err != nil {
				t.Fatalf("load product: %v", err)
			}
			if !tt.wantRestock {
				if gotProduct.Num != 0 || len(*restocks) != 0 {
					t.Fatalf("shipped refund must not restock before return: num=%d redis=%v", gotProduct.Num, *restocks)
				}
			} else {
				if gotProduct.Num != 1 {
					t.Fatalf("duplicate refund must restore stock once, got %d", gotProduct.Num)
				}
				if len(*restocks) != 1 || (*restocks)[0] != "stock:available:33+1" {
					t.Fatalf("redis available must be restocked once after commit, got %v", *restocks)
				}
			}
			var gotClearing clearing.PaymentClearing
			if err := db.First(&gotClearing, record.ID).Error; err != nil {
				t.Fatalf("load clearing: %v", err)
			}
			if gotClearing.Status != clearing.StatusRefunded || gotClearing.RefundedAt == nil {
				t.Fatalf("unexpected clearing state: %+v", gotClearing)
			}
		})
	}
}

func TestZeroAmountLegacyRefundIsIdempotent(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	seedRefundUser(t, db, 11, 100)
	seedRefundUser(t, db, 22, 50)
	p := &product.Product{Name: "free", CategoryID: 1, Num: 0, BossID: 22}
	p.ID = 55
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	o := &orderpkg.Order{
		UserID: 11, BossID: 22, ProductID: p.ID, AddressID: 1, Num: 1,
		Money: 300, PromoRuleID: 1, FinalCents: 0, Type: consts.OrderRefunded, RefundFromType: consts.OrderWaitShip,
	}
	o.ID = 66
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := GetRefundSrv().SettleRefund(context.Background(), o.ID); err != nil {
			t.Fatalf("refund attempt %d: %v", i+1, err)
		}
	}
	var gotProduct product.Product
	if err := db.First(&gotProduct, p.ID).Error; err != nil {
		t.Fatalf("load product: %v", err)
	}
	if gotProduct.Num != 1 {
		t.Fatalf("zero refund restored stock %d times, want once", gotProduct.Num)
	}
	var entries int64
	if err := db.Model(&money.AccountTransaction{}).
		Where("ref_order_id=? AND biz_type=?", o.ID, money.BizTypeRefund).Count(&entries).Error; err != nil {
		t.Fatalf("count marker ledger: %v", err)
	}
	if entries != 2 {
		t.Fatalf("zero refund should write balanced idempotency markers, got %d entries", entries)
	}
}

// order.completed / order.refunded 事件丢了（outbox 死信、消费者进 DLQ）：补偿任务按订单与清算单状态补结算、补退款，
// 刚完成的订单留给正常事件链路。
func TestSettleCompensationSettlesMissedOrders(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	seedRefundUser(t, db, 11, 0)
	seedRefundUser(t, db, 22, 0)
	p := &product.Product{Name: "book", CategoryID: 1, Num: 0, BossID: 22}
	p.ID = 33
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	seed := func(id, orderType uint, updatedAt time.Time) {
		o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: p.ID, AddressID: 1, Num: 1, Money: 300, Type: orderType}
		o.ID, o.UpdatedAt = id, updatedAt
		if err := db.Create(o).Error; err != nil {
			t.Fatalf("seed order: %v", err)
		}
		if err := db.Create(&clearing.PaymentClearing{
			OrderID: id, BuyerID: 11, SellerID: 22, Channel: clearing.ChannelWallet,
			GrossCents: 300, NetCents: 300, Currency: "CNY", Status: clearing.StatusCleared, ClearedAt: old,
		}).Error; err != nil {
			t.Fatalf("seed clearing: %v", err)
		}
	}
	seed(44, consts.OrderCompleted, old)
	seed(45, consts.OrderRefunded, old)
	seed(46, consts.OrderCompleted, time.Now())

	GetRefundSrv().RunSettleCompensation()

	for orderID, want := range map[uint]string{44: clearing.StatusSettled, 45: clearing.StatusRefunded, 46: clearing.StatusCleared} {
		var c clearing.PaymentClearing
		if err := db.Where("order_id = ?", orderID).First(&c).Error; err != nil {
			t.Fatal(err)
		}
		if c.Status != want {
			t.Fatalf("order %d clearing status = %s, want %s", orderID, c.Status, want)
		}
	}
	if got := refundUserBalance(t, db, 22); got != 300 {
		t.Fatalf("seller balance = %d, want 300 from the settled order", got)
	}
	if got := refundUserBalance(t, db, 11); got != 300 {
		t.Fatalf("buyer balance = %d, want 300 from the refunded order", got)
	}
}

func TestRejectRefundRestoresOriginalState(t *testing.T) {
	tests := []uint{consts.OrderWaitShip, consts.OrderWaitReceive, consts.OrderCompleted}
	for _, fromType := range tests {
		t.Run(fmt.Sprintf("from-%d", fromType), func(t *testing.T) {
			db := openRefundTestDB(t)
			seedRoleUser(t, db, 22, user.RoleMerchant)
			o := &orderpkg.Order{
				UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1,
				Money: 300, Type: fromType, OrderNum: uint64(1000 + fromType),
			}
			o.ID = 100 + fromType
			if err := db.Create(o).Error; err != nil {
				t.Fatalf("seed order: %v", err)
			}
			ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: o.UserID})
			if err := GetRefundSrv().RequestRefund(ctx, o.OrderNum, "changed mind"); err != nil {
				t.Fatalf("request refund: %v", err)
			}
			var refunding orderpkg.Order
			if err := db.First(&refunding, o.ID).Error; err != nil {
				t.Fatalf("load refunding order: %v", err)
			}
			if refunding.Type != consts.OrderRefunding || refunding.RefundFromType != fromType {
				t.Fatalf("refund origin not preserved: type=%d from=%d", refunding.Type, refunding.RefundFromType)
			}
			merchantCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: o.BossID})
			if err := GetRefundSrv().RejectRefund(merchantCtx, o.OrderNum, "not approved"); err != nil {
				t.Fatalf("reject refund: %v", err)
			}
			var restored orderpkg.Order
			if err := db.First(&restored, o.ID).Error; err != nil {
				t.Fatalf("load restored order: %v", err)
			}
			if restored.Type != fromType {
				t.Fatalf("restored type got %d want %d", restored.Type, fromType)
			}
			var evtRow outbox.OutboxEvent
			if err := db.Where("routing_key=? AND aggregate_id=?", "order.refund_rejected", o.ID).First(&evtRow).Error; err != nil {
				t.Fatalf("load rejection event: %v", err)
			}
			var evt events.OrderRefundRejected
			if err := json.Unmarshal([]byte(evtRow.Payload), &evt); err != nil {
				t.Fatalf("decode rejection event: %v", err)
			}
			if evt.RestoredType != fromType {
				t.Fatalf("event restored type got %d want %d", evt.RestoredType, fromType)
			}
		})
	}
}

func TestMerchantCannotHandleAnotherMerchantsRefund(t *testing.T) {
	db := openRefundTestDB(t)
	seedRoleUser(t, db, 21, user.RoleMerchant)
	seedRoleUser(t, db, 22, user.RoleMerchant)
	seedRoleUser(t, db, 99, user.RoleAdmin)
	asUser := func(id uint) context.Context {
		return ctl.NewContext(context.Background(), &ctl.UserInfo{Id: id})
	}
	approveOrder := &orderpkg.Order{
		UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
		Type: consts.OrderRefunding, RefundFromType: consts.OrderWaitShip, OrderNum: 7001,
	}
	rejectOrder := &orderpkg.Order{
		UserID: 11, BossID: 22, ProductID: 1, AddressID: 1, Num: 1, Money: 300,
		Type: consts.OrderRefunding, RefundFromType: consts.OrderWaitShip, OrderNum: 7002,
	}
	for _, o := range []*orderpkg.Order{approveOrder, rejectOrder} {
		if err := db.Create(o).Error; err != nil {
			t.Fatalf("seed order: %v", err)
		}
	}

	if err := GetRefundSrv().ApproveRefund(asUser(21), approveOrder.OrderNum); !errors.Is(err, orderpkg.ErrOrderForbidden) {
		t.Fatalf("merchant A approved merchant B's refund: err=%v", err)
	}
	if err := GetRefundSrv().RejectRefund(asUser(21), rejectOrder.OrderNum, "no"); !errors.Is(err, orderpkg.ErrOrderForbidden) {
		t.Fatalf("merchant A rejected merchant B's refund: err=%v", err)
	}
	assertRefundOrderType(t, db, approveOrder.ID, consts.OrderRefunding)
	assertRefundOrderType(t, db, rejectOrder.ID, consts.OrderRefunding)

	if err := GetRefundSrv().ApproveRefund(asUser(99), approveOrder.OrderNum); err != nil {
		t.Fatalf("admin approve: %v", err)
	}
	if err := GetRefundSrv().RejectRefund(asUser(99), rejectOrder.OrderNum, "no"); err != nil {
		t.Fatalf("admin reject: %v", err)
	}
	assertRefundOrderType(t, db, approveOrder.ID, consts.OrderRefunded)
	assertRefundOrderType(t, db, rejectOrder.ID, consts.OrderWaitShip)
}

func seedRoleUser(t *testing.T, db *gorm.DB, id uint, role string) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("refund-role-u%d", id), Role: role}
	u.ID = id
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func assertRefundOrderType(t *testing.T, db *gorm.DB, id uint, want uint) {
	t.Helper()
	var o orderpkg.Order
	if err := db.First(&o, id).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	if o.Type != want {
		t.Fatalf("order %d type got %d want %d", id, o.Type, want)
	}
}

func openRefundTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "refund-test-secret"}}
	if util.LogrusObj == nil {
		util.LogrusObj = logrus.New()
	}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:refund-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	previous := dao.SetTestDB(db)
	previousLogger := util.LogrusObj
	util.LogrusObj = logrus.New()
	t.Cleanup(func() {
		dao.SetTestDB(previous)
		util.LogrusObj = previousLogger
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&user.User{}, &orderpkg.Order{}, &product.Product{}, &money.AccountTransaction{}, &clearing.PaymentClearing{},
		&clearing.PaymentAnomaly{}, &clearing.PaymentAnomalyTransition{}, &outbox.OutboxEvent{},
		&promo.PromoRule{}, &promo.PromoRelease{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// stubRestockRedis 不连真实 Redis，记录退款回补 available 的调用（key+件数）。
func stubRestockRedis(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(restockHook{calls: &calls})
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
	return &calls
}

type restockHook struct{ calls *[]string }

func (restockHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (restockHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h restockHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if c, ok := cmd.(*redis.Cmd); ok {
			// evalsha sha 1 key n
			args := c.Args()
			*h.calls = append(*h.calls, fmt.Sprintf("%v+%v", args[3], args[4]))
			c.SetVal(int64(1))
		}
		return nil
	}
}

func seedRefundUser(t *testing.T, db *gorm.DB, id uint, balance int64) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("refund-u%d", id), Email: fmt.Sprintf("u%d@example.com", id), Money: strconv.FormatInt(balance, 10)}
	u.ID = id
	cipher, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt balance: %v", err)
	}
	u.Money = cipher
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func refundUserBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	balance, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt balance: %v", err)
	}
	return balance
}

// 结算后退款要从卖家余额扣回：卖家余额不够时整笔回滚，不能把卖家扣成负数，也不能只给买家入账。
func TestSettleRefundRejectsNegativeSellerBalance(t *testing.T) {
	db := openRefundTestDB(t)
	stubRestockRedis(t)
	seedRefundUser(t, db, 11, 100)
	seedRefundUser(t, db, 22, 100)
	p := &product.Product{Name: "book", CategoryID: 1, Num: 0, BossID: 22}
	p.ID = 77
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	o := &orderpkg.Order{UserID: 11, BossID: 22, ProductID: p.ID, AddressID: 1, Num: 1, Money: 300, Type: consts.OrderRefunded}
	o.ID, o.UpdatedAt = 88, time.Now().Add(-time.Hour)
	if err := db.Create(o).Error; err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := db.Create(&clearing.PaymentClearing{
		OrderID: o.ID, BuyerID: 11, SellerID: 22, Channel: clearing.ChannelWallet,
		GrossCents: 300, NetCents: 300, Currency: "CNY", Status: clearing.StatusSettled, ClearedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("seed clearing: %v", err)
	}

	if err := GetRefundSrv().SettleRefund(context.Background(), o.ID); !errors.Is(err, ErrSellerBalanceInsufficient) {
		t.Fatalf("want ErrSellerBalanceInsufficient, got %v", err)
	}
	if got := refundUserBalance(t, db, 22); got != 100 {
		t.Fatalf("seller balance = %d, must stay 100", got)
	}
	if got := refundUserBalance(t, db, 11); got != 100 {
		t.Fatalf("buyer balance = %d, must stay 100 when refund rolls back", got)
	}

	// 消费者重投与补偿任务并发撞上同一单：异常单只落一张，进人工队列。
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = GetRefundSrv().SettleRefund(context.Background(), o.ID)
		}()
	}
	wg.Wait()
	anomaly := func() clearing.PaymentAnomaly {
		var list []clearing.PaymentAnomaly
		if err := db.Where("order_id = ?", o.ID).Find(&list).Error; err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].Reason != clearing.AnomalyReasonRefundSellerShortfall || list[0].ProviderAmount != "300" {
			t.Fatalf("want exactly one shortfall anomaly, got %+v", list)
		}
		return list[0]
	}
	if a := anomaly(); a.Status != clearing.AnomalyStatusPendingReview {
		t.Fatalf("anomaly status = %s, want pending_review", a.Status)
	}
	setSeller := func(balance int64) {
		u := &user.User{Money: strconv.FormatInt(balance, 10)}
		cipher, err := u.EncryptMoney()
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&user.User{}).Where("id = ?", 22).Update("money", cipher).Error; err != nil {
			t.Fatal(err)
		}
	}
	resolve := func() {
		if err := db.Model(&clearing.PaymentAnomaly{}).Where("order_id = ?", o.ID).
			Update("status", clearing.AnomalyStatusResolved).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 运营标 resolved 但卖家仍不够：补偿重放失败，异常单退回待审核并留系统流转记录。
	resolve()
	GetRefundSrv().RunSettleCompensation()
	if a := anomaly(); a.Status != clearing.AnomalyStatusPendingReview {
		t.Fatalf("retry still short: anomaly status = %s, want pending_review", a.Status)
	}
	var transitions int64
	db.Model(&clearing.PaymentAnomalyTransition{}).Count(&transitions)
	if transitions != 1 {
		t.Fatalf("want one system transition, got %d", transitions)
	}

	// 卖家补足余额但异常单未处理完：补偿任务跳过，不能自己退。
	setSeller(1000)
	GetRefundSrv().RunSettleCompensation()
	if got := refundUserBalance(t, db, 11); got != 100 {
		t.Fatalf("pending anomaly must be skipped by compensation, buyer balance = %d", got)
	}

	// 运营标 resolved：补偿任务自动重放这笔退款。
	resolve()
	GetRefundSrv().RunSettleCompensation()
	if got := refundUserBalance(t, db, 11); got != 400 {
		t.Fatalf("buyer balance = %d, want 400 after resolved retry", got)
	}
	if got := refundUserBalance(t, db, 22); got != 700 {
		t.Fatalf("seller balance = %d, want 700 after resolved retry", got)
	}
}
