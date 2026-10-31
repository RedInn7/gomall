package payment

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/clearing"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/repository/cache"
)

// 链上已到账但库存不足时记了 fulfillment_failed 异常单，这笔钱已转人工退款；
// 之后库存补回来，同一 txHash 的事件重投也不能再走正常结算，否则同一笔钱既发货又可能被退。
func TestWeb3RedeliveryAfterAnomalyDoesNotSettle(t *testing.T) {
	t.Setenv(envWeb3PayToken, "usdc")
	t.Setenv(envWeb3USDCDecimals, "6")
	t.Setenv(envWeb3CentsPerUSDC, "720")
	db := newPaymentTestDB(t, &user.User{}, &product.Product{}, &orderpkg.Order{},
		&money.AccountTransaction{}, &clearing.PaymentClearing{}, &clearing.PaymentAnomaly{}, &outbox.OutboxEvent{})
	const buyerAddr = "0xBuyer"
	stubWeb3Redis(t, buyerAddr)

	buyer := &user.User{UserName: "web3-buyer"}
	buyer.ID = 1
	if err := db.Create(buyer).Error; err != nil {
		t.Fatal(err)
	}
	item := &product.Product{Name: "keyboard", CategoryID: 1, Num: 0, BossID: 2}
	item.ID = 10
	if err := db.Create(item).Error; err != nil {
		t.Fatal(err)
	}
	o := &orderpkg.Order{
		UserID: 1, BossID: 2, ProductID: item.ID, AddressID: 20, Num: 1, Money: 1440,
		OrderNum: 20261031003, Type: consts.OrderWaitPay,
	}
	if err := db.Create(o).Error; err != nil {
		t.Fatal(err)
	}

	const txHash, amount = "0xtx-oversold", "2000000" // 1440 分 / ¥7.20 = 2 USDC
	if err := GetWeb3SettleSrv().SettleConfirmedOrder(context.Background(), o.ID, buyerAddr, amount, txHash); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	var anomalies int64
	if err := db.Model(&clearing.PaymentAnomaly{}).
		Where("channel = ? AND provider_ref = ? AND reason = ?", clearing.ChannelWeb3, txHash, clearing.AnomalyReasonFulfillmentFailed).
		Count(&anomalies).Error; err != nil {
		t.Fatal(err)
	}
	if anomalies != 1 {
		t.Fatalf("库存不足应记一笔 fulfillment_failed 异常单, got %d", anomalies)
	}

	// 库存补回后同一事件重投。
	if err := db.Model(item).Update("num", 5).Error; err != nil {
		t.Fatal(err)
	}
	if err := GetWeb3SettleSrv().SettleConfirmedOrder(context.Background(), o.ID, buyerAddr, amount, txHash); err != nil {
		t.Fatalf("redelivery: %v", err)
	}

	var clearings int64
	if err := db.Model(&clearing.PaymentClearing{}).Count(&clearings).Error; err != nil {
		t.Fatal(err)
	}
	var got orderpkg.Order
	if err := db.First(&got, o.ID).Error; err != nil {
		t.Fatal(err)
	}
	if clearings != 0 || got.Type != consts.OrderWaitPay {
		t.Fatalf("已记异常单的 txHash 重投不能再清算: clearings=%d order type=%d", clearings, got.Type)
	}
}

// web3PendingHook 不连真实 Redis：HGET park 占位返回买家钱包地址，Lua 脚本返回 1，其余命令直接放行。
type web3PendingHook struct{ addr string }

func (web3PendingHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h web3PendingHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch c := cmd.(type) {
		case *redis.StringCmd:
			c.SetVal(h.addr)
		case *redis.Cmd:
			c.SetVal(int64(1))
		}
		return nil
	}
}
func (web3PendingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func stubWeb3Redis(t *testing.T, addr string) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(web3PendingHook{addr: addr})
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
}
