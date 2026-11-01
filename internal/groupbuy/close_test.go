package groupbuy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/service/events"
)

func TestCloseGroupRouteIsAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	public := router.Group("/api/v1")
	authed := public.Group("/")
	RegisterRoutes(public, authed, authed.Group("/"), authed.Group("/admin"))
	for _, r := range router.Routes() {
		if r.Method == "POST" && r.Path == "/api/v1/admin/groupbuy/:id/close" {
			return
		}
	}
	t.Fatal("缺少 admin 关团路由")
}

// 人工关团：原路退款、关单、记审计，事件 reason=manual；重复关 / 不填原因都拒绝。
func TestCloseGroupRefundsAndAudits(t *testing.T) {
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

	if err := GetGroupbuySrv().CloseGroup(ctx, created.GroupID, 99, "  "); !errors.Is(err, ErrGroupbuyCloseReason) || e.CodeOf(err) != e.InvalidParams {
		t.Fatalf("不填原因应按参数错误拒绝, got %v code=%d", err, e.CodeOf(err))
	}
	if err := GetGroupbuySrv().CloseGroup(ctx, 999999, 99, "x"); e.CodeOf(err) != e.ErrGroupbuyNotFound {
		t.Fatalf("团不存在应透出 81005, got %v code=%d", err, e.CodeOf(err))
	}
	if err := GetGroupbuySrv().CloseGroup(ctx, created.GroupID, 99, "疑似刷单"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := GetGroupbuySrv().CloseGroup(ctx, created.GroupID, 99, "再关一次"); !errors.Is(err, ErrGroupbuyClosed) {
		t.Fatalf("重复关团应返回 81004, got %v", err)
	}

	var g GroupbuyGroup
	db.First(&g, created.GroupID)
	if g.Status != GroupbuyStatusClosed || g.ClosedBy != 99 || g.CloseReason != "疑似刷单" {
		t.Fatalf("关团状态 / 审计不对: %+v", g)
	}
	// 免登录分享落地页不能带出关团操作人、风控原因和成员订单 id。
	sg, sm, err := GetGroupbuySrv().ShowGroup(ctx, created.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(GroupbuyShowResp{Group: sg, Members: sm})
	for _, leak := range []string{"疑似刷单", "ClosedBy", "CloseReason", "OrderID"} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("落地页泄露 %q: %s", leak, body)
		}
	}
	for _, uid := range []uint{1, 3} {
		if got := groupbuyUserBalance(t, db, uid); got != 100000 {
			t.Fatalf("user=%d 应全额退回, balance=%d", uid, got)
		}
	}
	for _, oid := range []uint{created.OrderID, joined.OrderID} {
		var o orderpkg.Order
		db.First(&o, oid)
		if o.Type != consts.OrderClosed {
			t.Fatalf("order=%d type=%d, want Closed", oid, o.Type)
		}
	}
	var evt outbox.OutboxEvent
	if err := db.Where("routing_key=?", "groupbuy.expired").First(&evt).Error; err != nil {
		t.Fatalf("load outbox: %v", err)
	}
	var payload events.GroupbuyExpired
	_ = json.Unmarshal([]byte(evt.Payload), &payload)
	if payload.Reason != "manual" {
		t.Fatalf("event reason=%q, want manual", payload.Reason)
	}
	// 关团后的事件重放：消费者兜底补退不能重复退钱。
	if err := GetGroupbuySrv().SettleExpiredRefund(ctx, created.GroupID); err != nil {
		t.Fatal(err)
	}
	if got := groupbuyUserBalance(t, db, 1); got != 100000 {
		t.Fatalf("重放不能重复退款, balance=%d", got)
	}
}

// 人工关团与抢最后一个名额并发：要么成团（关团返回 81004），要么关团（所有人全额退回），钱总量守恒。
func TestCloseGroupRacesWithLastJoin(t *testing.T) {
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("round%d", i), func(t *testing.T) {
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

			barrierHook(t, 2)
			var (
				wg       sync.WaitGroup
				joinResp *JoinGroupResp
				joinErr  error
				closeErr error
			)
			wg.Add(2)
			go func() {
				defer wg.Done()
				joinResp, joinErr = GetGroupbuySrv().JoinGroup(ctx, 3, created.GroupID, 0, 0, testPayKey)
			}()
			go func() {
				defer wg.Done()
				joinTxHook()
				closeErr = GetGroupbuySrv().CloseGroup(ctx, created.GroupID, 99, "风控")
			}()
			wg.Wait()

			var g GroupbuyGroup
			db.First(&g, created.GroupID)
			switch g.Status {
			case GroupbuyStatusSuccess:
				if !errors.Is(closeErr, ErrGroupbuyClosed) || joinErr != nil || !joinResp.IsSuccess {
					t.Fatalf("成团分支: close=%v join=%v resp=%+v", closeErr, joinErr, joinResp)
				}
			case GroupbuyStatusClosed:
				if closeErr != nil {
					t.Fatalf("关团分支 close err=%v", closeErr)
				}
				if joinErr == nil && joinResp.IsSuccess {
					t.Fatal("团已关闭却告诉用户成团")
				}
				for _, uid := range []uint{1, 3} {
					if got := groupbuyUserBalance(t, db, uid); got != 100000 {
						t.Fatalf("关团后 user=%d 应全额退回, balance=%d", uid, got)
					}
				}
			default:
				t.Fatalf("unexpected status %d", g.Status)
			}
			total := groupbuyUserBalance(t, db, 1) + groupbuyUserBalance(t, db, 2) + groupbuyUserBalance(t, db, 3) +
				accountNet(t, db, money.AccountCodeGroupbuyEscrow) + accountNet(t, db, money.AccountCodeMerchantEscrow)
			if total != 200000 {
				t.Fatalf("资金不守恒 total=%d", total)
			}
		})
	}
}
