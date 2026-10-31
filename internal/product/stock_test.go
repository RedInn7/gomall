package product

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 库存桶不存在（比如商品绕过建商品接口直接写库）时，第一次下单就按 DB 库存建桶并预占成功。
func TestReserveStockInitsMissingBucketFromDB(t *testing.T) {
	db := openProductOutboxTestDB(t, false)
	useTestDB(t, db)
	p := seedProduct(t, db)
	f := useFakeRedis(t, -2) // 第一次预占：桶不存在

	if err := ReserveStock(context.Background(), p.ID, 2); err != nil {
		t.Fatalf("ReserveStock() error = %v", err)
	}
	evals := f.calls("evalsha")
	if len(evals) != 3 {
		t.Fatalf("lua calls = %v, want reserve -> init -> reserve", evals)
	}
	// EVALSHA sha numkeys available reserved num
	if init := evals[1]; init[3] != cache.StockAvailableKey(p.ID) || init[5] != int64(p.Num) {
		t.Fatalf("init args = %v, want bucket built from DB num %d", init, p.Num)
	}
}

func TestReserveStockRetriesOnlyOnce(t *testing.T) {
	db := openProductOutboxTestDB(t, false)
	useTestDB(t, db)
	p := seedProduct(t, db)
	f := useFakeRedis(t, -2, 1, -2)

	if err := ReserveStock(context.Background(), p.ID, 1); !errors.Is(err, cache.ErrStockNotInit) {
		t.Fatalf("ReserveStock() error = %v, want %v", err, cache.ErrStockNotInit)
	}
	if n := len(f.calls("evalsha")); n != 3 {
		t.Fatalf("lua calls = %d, want 3", n)
	}
}

// 只改上下架不动 Redis 库存；补货 10 -> 15 时 available 按差值 +5。
func TestProductUpdateAdjustsAvailableByDelta(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	useTestDB(t, db)
	p := seedProduct(t, db)
	f := useFakeRedis(t)
	discardLog(t)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: p.BossID})

	for _, req := range []*ProductUpdateReq{{ID: p.ID, OnSale: new(false)}, {ID: p.ID, Num: new(15)}} {
		if _, err := GetProductSrv().ProductUpdate(ctx, req); err != nil {
			t.Fatalf("ProductUpdate(%+v) error = %v", req, err)
		}
	}
	evals := f.calls("evalsha")
	// EVALSHA sha numkeys available delta
	if len(evals) != 1 || evals[0][3] != cache.StockAvailableKey(p.ID) || evals[0][4] != int64(5) {
		t.Fatalf("lua calls = %v, want one available += 5", evals)
	}
	// 等两次更新的延迟双删 goroutine 都删完，再还原 Redis 客户端
	for deadline := time.Now().Add(2 * time.Second); len(f.calls("del")) < 4; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("延迟双删没有执行")
		}
	}
}

func discardLog(t *testing.T) {
	t.Helper()
	prev := log.LogrusObj
	log.LogrusObj = logrus.New()
	log.LogrusObj.Out = io.Discard
	t.Cleanup(func() { log.LogrusObj = prev })
}

func useTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	prev := dao.SetTestDB(db)
	t.Cleanup(func() { dao.SetTestDB(prev) })
}

// fakeRedis 记录发到 Redis 的命令，不连真实 Redis：Lua 脚本按 evalRet 依次返回（用完后返回 1），
// GET 一律未命中，SET NX 一律抢到，其余命令直接成功。
type fakeRedis struct {
	mu      sync.Mutex
	cmds    [][]interface{}
	evalRet []int64
}

func useFakeRedis(t *testing.T, evalRet ...int64) *fakeRedis {
	t.Helper()
	f := &fakeRedis{evalRet: evalRet}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(f)
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
	return f
}

// calls 返回某个命令（小写，如 "evalsha"、"del"）每次调用的完整参数。
func (f *fakeRedis) calls(name string) [][]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]interface{}
	for _, args := range f.cmds {
		if args[0] == name {
			out = append(out, args)
		}
	}
	return out
}

func (f *fakeRedis) DialHook(next redis.DialHook) redis.DialHook { return next }
func (f *fakeRedis) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (f *fakeRedis) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.cmds = append(f.cmds, cmd.Args())
		switch c := cmd.(type) {
		case *redis.Cmd: // Lua 脚本
			ret := int64(1)
			if len(f.evalRet) > 0 {
				ret, f.evalRet = f.evalRet[0], f.evalRet[1:]
			}
			c.SetVal(ret)
		case *redis.StringCmd:
			return redis.Nil // hook 的返回值会覆盖命令的 err
		case *redis.BoolCmd:
			c.SetVal(true)
		}
		return nil
	}
}
