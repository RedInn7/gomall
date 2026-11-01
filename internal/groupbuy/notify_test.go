package groupbuy

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 把 outbox 里某个团的事件按原样喂给通知消费者，并发重放 3 遍模拟 at-least-once。
func replayOutboxToNotify(t *testing.T, routingKey string) {
	t.Helper()
	var rows []outbox.OutboxEvent
	if err := dao.NewDBClient(context.Background()).Where("routing_key=?", routingKey).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("outbox 没有 %s 事件", routingKey)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		for _, r := range rows {
			wg.Add(1)
			go func(payload string) {
				defer wg.Done()
				if err := DispatchNotifyEvent(context.Background(), routingKey, []byte(payload)); err != nil {
					t.Errorf("notify %s: %v", routingKey, err)
				}
			}(r.Payload)
		}
	}
	wg.Wait()
}

func noticesOf(t *testing.T, userID uint) []*GroupbuyNotice {
	t.Helper()
	rows, err := GetGroupbuySrv().ListMyNotices(context.Background(), userID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// 参团通知团长、成团通知全体成员；事件重复投递不重复通知。
func TestNotifyJoinedAndSuccessIdempotent(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	seedGroupbuyUser(t, db, 2, 0)
	seedGroupbuyUser(t, db, 3, 100000)
	p := seedGroupbuyProduct(t, db, 8000)
	ctx := context.Background()
	created, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, 2, 0, 0, 0, testPayKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetGroupbuySrv().JoinGroup(ctx, 3, created.GroupID, 0, 0, testPayKey); err != nil {
		t.Fatal(err)
	}

	replayOutboxToNotify(t, "groupbuy.joined")
	replayOutboxToNotify(t, "groupbuy.success")

	leader := noticesOf(t, 1)
	if len(leader) != 2 || leader[0].Kind != NoticeKindSuccess || leader[1].Kind != NoticeKindJoined || leader[1].RefID != 3 {
		t.Fatalf("团长应收到参团 + 成团各一条: %+v", leader)
	}
	member := noticesOf(t, 3)
	if len(member) != 1 || member[0].Kind != NoticeKindSuccess {
		t.Fatalf("成员应只收到成团一条: %+v", member)
	}
}

// 人工关团通知全体成员，话术区分超时与关团。
func TestNotifyManualCloseToAllMembers(t *testing.T) {
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
	if _, err := GetGroupbuySrv().JoinGroup(ctx, 3, created.GroupID, 0, 0, testPayKey); err != nil {
		t.Fatal(err)
	}
	if err := GetGroupbuySrv().CloseGroup(ctx, created.GroupID, 99, "风控"); err != nil {
		t.Fatal(err)
	}
	replayOutboxToNotify(t, "groupbuy.expired")
	for _, uid := range []uint{1, 3} {
		var got *GroupbuyNotice
		for _, n := range noticesOf(t, uid) {
			if n.Kind == NoticeKindExpired {
				if got != nil {
					t.Fatalf("user=%d 收到重复关团通知", uid)
				}
				got = n
			}
		}
		if got == nil || got.Content != "拼团已被平台关闭，款项已原路退回钱包" {
			t.Fatalf("user=%d 关团通知不对: %+v", uid, got)
		}
	}
}

func TestNotifyRejectsPoisonMessage(t *testing.T) {
	openGroupbuyTestDB(t)
	if err := DispatchNotifyEvent(context.Background(), "groupbuy.created", []byte(`{}`)); !errors.Is(err, errSettlePoisonMessage) {
		t.Fatalf("未订阅的 key 应判毒消息, got %v", err)
	}
	if err := DispatchNotifyEvent(context.Background(), "groupbuy.joined", []byte(`{}`)); !errors.Is(err, errSettlePoisonMessage) {
		t.Fatalf("缺 group_id 应判毒消息, got %v", err)
	}
}
