package refund

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 退款三流程(RequestRefund / ApproveRefund / RejectRefund)端到端测试。
// 这三个方法只做两件事：推进订单状态机 + 写 outbox 事件（真正的资金回退在 SettleRefund，
// 已由 settle_test.go 覆盖）。此前 refund_test.go 只测了守卫常量这类浅层逻辑，三条完整
// 状态跃迁 + outbox 落库未端到端覆盖——本文件补齐。
//
// 评审 #2 结论：refund 该补 sqlite 测，而非套 DI 空接口。

const refundTestOrderNum = uint64(990001)

func initRefundFlowLog() {
	if util.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		util.LogrusObj = l
	}
}

// setupRefundFlowDB 迁移流程涉及的 order + outbox 两张表（资金/库存归 SettleRefund 测），
// 并接通 Redis：订单状态机推进会失效用户订单列表缓存(invalidateUserOrderListCache)，
// RedisClient 为 nil 会 panic，故测试必须连真 Redis(DB 14)。
func setupRefundFlowDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	rc := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 14})
	if err := rc.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis 127.0.0.1:6379 不可用：%v", err)
	}
	prevRedis := cache.RedisClient
	cache.RedisClient = rc

	dsn := "file:refundflow-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		rc.Close()
		cache.RedisClient = prevRedis
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&orderpkg.Order{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prevDB := dao.SetTestDB(db)
	return db, func() {
		dao.SetTestDB(prevDB)
		rc.FlushDB(context.Background())
		rc.Close()
		cache.RedisClient = prevRedis
	}
}

// seedRefundOrder 造一张指定归属与状态的订单（单价 2000 分 x 2 件 = 应退 4000）。
func seedRefundOrder(t *testing.T, db *gorm.DB, userID, typ uint) *orderpkg.Order {
	t.Helper()
	ord := &orderpkg.Order{
		UserID:    userID,
		ProductID: 7,
		BossID:    9,
		Num:       2,
		Money:     2000,
		Type:      typ,
		OrderNum:  refundTestOrderNum,
	}
	if err := db.Create(ord).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	return ord
}

func reloadOrderType(t *testing.T, db *gorm.DB, id uint) uint {
	t.Helper()
	var o orderpkg.Order
	if err := db.First(&o, id).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	return o.Type
}

func countOutbox(t *testing.T, db *gorm.DB, routingKey string, aggID uint) int64 {
	t.Helper()
	var n int64
	db.Model(&outbox.OutboxEvent{}).Where("routing_key=? AND aggregate_id=?", routingKey, aggID).Count(&n)
	return n
}

// ---------- RequestRefund ----------

func TestRequestRefund_Success(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	const uid = uint(42)
	ord := seedRefundOrder(t, db, uid, consts.OrderWaitShip)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: uid})

	if err := GetRefundSrv().RequestRefund(ctx, refundTestOrderNum, "不想要了"); err != nil {
		t.Fatalf("RequestRefund: %v", err)
	}
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderRefunding {
		t.Fatalf("order type = %d, want Refunding", got)
	}
	if n := countOutbox(t, db, "order.refunding", ord.ID); n != 1 {
		t.Fatalf("expect 1 order.refunding outbox, got %d", n)
	}
}

func TestRequestRefund_WrongUserRejected(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	ord := seedRefundOrder(t, db, 42, consts.OrderWaitShip)
	// 用别人的身份发起：必须拒绝（防越权退他人订单），状态/outbox 不变。
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 99})

	if err := GetRefundSrv().RequestRefund(ctx, refundTestOrderNum, "x"); err == nil {
		t.Fatal("非订单归属者发起退款应被拒绝")
	}
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderWaitShip {
		t.Fatalf("order type = %d, want 不变 WaitShip", got)
	}
	if n := countOutbox(t, db, "order.refunding", ord.ID); n != 0 {
		t.Fatalf("拒绝路径不应写 outbox, got %d", n)
	}
}

func TestRequestRefund_InvalidStateRejected(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	const uid = uint(42)
	// WaitPay 不在可退状态(未付款应走 cancel 关单)。
	ord := seedRefundOrder(t, db, uid, consts.OrderWaitPay)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: uid})

	err := GetRefundSrv().RequestRefund(ctx, refundTestOrderNum, "x")
	if !errors.Is(err, orderpkg.ErrInvalidOrderStateTransition) {
		t.Fatalf("WaitPay 发起退款应 ErrInvalidOrderStateTransition, got %v", err)
	}
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderWaitPay {
		t.Fatalf("order type = %d, want 不变 WaitPay", got)
	}
}

// ---------- ApproveRefund ----------

func TestApproveRefund_Success(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	ord := seedRefundOrder(t, db, 42, consts.OrderRefunding)

	if err := GetRefundSrv().ApproveRefund(context.Background(), refundTestOrderNum); err != nil {
		t.Fatalf("ApproveRefund: %v", err)
	}
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderRefunded {
		t.Fatalf("order type = %d, want Refunded", got)
	}
	if n := countOutbox(t, db, "order.refunded", ord.ID); n != 1 {
		t.Fatalf("expect 1 order.refunded outbox, got %d", n)
	}
}

func TestApproveRefund_InvalidStateRejected(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	// 非 Refunding(这里 WaitShip)不可同意退款。
	ord := seedRefundOrder(t, db, 42, consts.OrderWaitShip)

	err := GetRefundSrv().ApproveRefund(context.Background(), refundTestOrderNum)
	if !errors.Is(err, orderpkg.ErrInvalidOrderStateTransition) {
		t.Fatalf("非 Refunding 同意退款应 ErrInvalidOrderStateTransition, got %v", err)
	}
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderWaitShip {
		t.Fatalf("order type = %d, want 不变 WaitShip", got)
	}
	if n := countOutbox(t, db, "order.refunded", ord.ID); n != 0 {
		t.Fatalf("拒绝路径不应写 outbox, got %d", n)
	}
}

// ---------- RejectRefund ----------

func TestRejectRefund_Success(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	ord := seedRefundOrder(t, db, 42, consts.OrderRefunding)

	if err := GetRefundSrv().RejectRefund(context.Background(), refundTestOrderNum, "凭证不足"); err != nil {
		t.Fatalf("RejectRefund: %v", err)
	}
	// 驳回后订单回到 Completed。
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderCompleted {
		t.Fatalf("order type = %d, want Completed", got)
	}
	if n := countOutbox(t, db, "order.refund_rejected", ord.ID); n != 1 {
		t.Fatalf("expect 1 order.refund_rejected outbox, got %d", n)
	}
}

func TestRejectRefund_InvalidStateRejected(t *testing.T) {
	initRefundFlowLog()
	db, cleanup := setupRefundFlowDB(t)
	defer cleanup()

	// 已是 Completed(非 Refunding)不可驳回。
	ord := seedRefundOrder(t, db, 42, consts.OrderCompleted)

	err := GetRefundSrv().RejectRefund(context.Background(), refundTestOrderNum, "x")
	if !errors.Is(err, orderpkg.ErrInvalidOrderStateTransition) {
		t.Fatalf("非 Refunding 驳回应 ErrInvalidOrderStateTransition, got %v", err)
	}
	if got := reloadOrderType(t, db, ord.ID); got != consts.OrderCompleted {
		t.Fatalf("order type = %d, want 不变 Completed", got)
	}
}
