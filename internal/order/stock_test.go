package order_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/shared/dbmodel"
	"github.com/RedInn7/gomall/repository/cache"
)

// 拼团中（WaitGroup）订单的预占、支付后没核销掉的预占都是合法占用，对账不能当泄漏退回 available。
func TestStockReconcileKeepsGroupbuyAndCommitPendingReservations(t *testing.T) {
	db := openOrderTestDB(t)
	stock := stubStockRedis(t, map[string]int64{
		cache.StockAvailableKey(7): 4, cache.StockReservedKey(7): 1,
		cache.StockAvailableKey(8): 4, cache.StockReservedKey(8): 1,
	})
	seedOrder(t, db, &order.Order{UserID: 11, BossID: 22, ProductID: 7, Type: consts.OrderWaitGroup, OrderNum: 9001})
	paid := seedOrder(t, db, &order.Order{
		UserID: 11, BossID: 22, ProductID: 8, Type: consts.OrderWaitShip, OrderNum: 9002,
		StockCommitPending: true, Model: dbmodel.Model{UpdatedAt: time.Now().Add(-time.Hour)},
	})

	new(order.OrderTaskService).RunStockReservationReconcile()

	if stock[cache.StockReservedKey(7)] != 1 || stock[cache.StockAvailableKey(7)] != 4 {
		t.Fatalf("groupbuy reservation reclaimed: %v", stock)
	}
	// 待核销的预占被补核销，而不是退回 available。
	if stock[cache.StockReservedKey(8)] != 0 || stock[cache.StockAvailableKey(8)] != 4 {
		t.Fatalf("commit-pending reservation not committed: %v", stock)
	}
	var got order.Order
	if err := db.First(&got, paid.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.StockCommitPending {
		t.Fatal("pending flag should be cleared after commit")
	}
}

// stubStockRedis 用内存 map 顶替 Redis，只实现库存相关命令：GET / SCAN / EXISTS 与三个库存 Lua。
func stubStockRedis(t *testing.T, kv map[string]int64) map[string]int64 {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(stockHook(kv))
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
	return kv
}

type stockHook map[string]int64

func (stockHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (stockHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (kv stockHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		switch c := cmd.(type) {
		case *redis.ScanCmd:
			var keys []string
			for k := range kv {
				if strings.HasPrefix(k, "stock:reserved:") {
					keys = append(keys, k)
				}
			}
			c.SetVal(keys, 0)
		case *redis.StringCmd:
			v, ok := kv[args[1].(string)]
			if !ok {
				c.SetErr(redis.Nil)
				return redis.Nil
			}
			c.SetVal(strconv.FormatInt(v, 10))
		case *redis.IntCmd:
			if cmd.Name() == "exists" {
				var n int64
				for _, k := range args[1:] {
					if _, ok := kv[k.(string)]; ok {
						n++
					}
				}
				c.SetVal(n)
			}
		case *redis.Cmd:
			// evalsha sha numkeys key... n：commit 只有 reserved 一个 key；release 是 reserved→available；reserve 反之。
			numKeys := args[2].(int)
			keys := args[3 : 3+numKeys]
			n, _ := strconv.ParseInt(fmt.Sprint(args[len(args)-1]), 10, 64)
			from := keys[0].(string)
			if kv[from] < n {
				c.SetVal(int64(-1))
				return nil
			}
			kv[from] -= n
			if numKeys == 2 {
				kv[keys[1].(string)] += n
			}
			c.SetVal(int64(1))
		}
		return nil
	}
}
