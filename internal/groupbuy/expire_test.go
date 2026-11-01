package groupbuy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/service/events"
)

// 超时散团：多个 cron 实例并发散同一个团，钱只退一次；groupbuy.expired 重放不重复退。
func TestExpireGroupRefundsOnceUnderConcurrencyAndReplay(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	seedGroupbuyUser(t, db, 2, 0)
	seedGroupbuyUser(t, db, 3, 100000)
	p := seedGroupbuyProduct(t, db, 8000)
	ctx := context.Background()
	created, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, 3, 0, 0, 0, testPayKey)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := GetGroupbuySrv().JoinGroup(ctx, 3, created.GroupID, 0, 0, testPayKey)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&GroupbuyGroup{}).Where("id=?", created.GroupID).Update("expire_at", time.Now().Add(-time.Minute))

	ids, err := NewGroupbuyDao(ctx).ExpireOpenGroupsBefore(time.Now(), 10)
	if err != nil || len(ids) != 1 || ids[0] != created.GroupID {
		t.Fatalf("应扫到过期团: ids=%v err=%v", ids, err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = GetGroupbuySrv().ExpireGroup(ctx, created.GroupID)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("expire: %v", err)
		}
	}

	payload, _ := json.Marshal(events.GroupbuyExpired{GroupID: created.GroupID, Reason: "timeout"})
	for i := 0; i < 2; i++ {
		if err := DispatchSettleEvent(ctx, "groupbuy.expired", payload); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}

	for _, uid := range []uint{1, 3} {
		if got := groupbuyUserBalance(t, db, uid); got != 100000 {
			t.Fatalf("user=%d 应只退一次, balance=%d", uid, got)
		}
	}
	if got := accountNet(t, db, money.AccountCodeGroupbuyEscrow); got != 0 {
		t.Fatalf("散团后拼团托管应清零, got %d", got)
	}
	for _, oid := range []uint{created.OrderID, joined.OrderID} {
		var o orderpkg.Order
		db.First(&o, oid)
		if o.Type != consts.OrderClosed {
			t.Fatalf("order=%d type=%d, want Closed", oid, o.Type)
		}
	}
	var g GroupbuyGroup
	db.First(&g, created.GroupID)
	if g.Status != GroupbuyStatusExpired {
		t.Fatalf("status=%d, want expired", g.Status)
	}
}

func TestDispatchSettleEventRejectsPoisonMessage(t *testing.T) {
	openGroupbuyTestDB(t) // 初始化日志
	ctx := context.Background()
	if err := DispatchSettleEvent(ctx, "groupbuy.unknown", []byte(`{}`)); !errors.Is(err, errSettlePoisonMessage) {
		t.Fatalf("未知 routing key 应进 DLQ, got %v", err)
	}
	if err := DispatchSettleEvent(ctx, "groupbuy.expired", []byte(`not json`)); !errors.Is(err, errSettlePoisonMessage) {
		t.Fatalf("坏 payload 应进 DLQ, got %v", err)
	}
}

func TestCreateGroupValidatesTargetAndTTL(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	p := seedGroupbuyProduct(t, db, 8000)
	ctx := context.Background()
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, MaxGroupbuyTarget+1, 0, 0, 0, testPayKey); err == nil {
		t.Fatal("成团人数超上限应拒绝")
	}
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, 2, 30*24*time.Hour, 0, 0, testPayKey); err == nil {
		t.Fatal("超长时效应拒绝")
	}
	if got := groupbuyUserBalance(t, db, 1); got != 100000 {
		t.Fatalf("校验失败不能扣钱, balance=%d", got)
	}
}
