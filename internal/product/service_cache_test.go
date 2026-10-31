package product

import (
	"context"
	"testing"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/repository/cache"
)

// 领头请求的 ctx 已经取消：单飞里查库、写缓存用的是脱钩的 ctx，照样完成并写进缓存。
func TestProductShowLoadSurvivesCanceledLeader(t *testing.T) {
	db := openProductOutboxTestDB(t, false)
	useTestDB(t, db)
	p := seedProduct(t, db)
	f := useFakeRedis(t)
	discardLog(t)
	prevConf := conf.Config
	conf.Config = &conf.Conf{System: &conf.System{}}
	t.Cleanup(func() { conf.Config = prevConf })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := GetProductSrv().ProductShow(ctx, &ProductShowReq{ID: p.ID})
	if err != nil || got.ID != p.ID {
		t.Fatalf("ProductShow() = %+v, %v; want product %d", got, err, p.ID)
	}
	for _, args := range f.calls("set") {
		if args[1] == cache.ProductDetailKey(p.ID) {
			return
		}
	}
	t.Fatalf("详情没有写进缓存, set calls = %v", f.calls("set"))
}
