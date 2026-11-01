package groupbuy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

// barrierHook 让 n 个并发参团请求都越过预检后再一起进事务。
func barrierHook(t *testing.T, n int) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(n)
	joinTxHook = func() {
		wg.Done()
		wg.Wait()
	}
	t.Cleanup(func() { joinTxHook = nil })
}

func concurrentJoin(groupID uint, userIDs ...uint) []error {
	errs := make([]error, len(userIDs))
	var wg sync.WaitGroup
	for i, uid := range userIDs {
		wg.Add(1)
		go func(i int, uid uint) {
			defer wg.Done()
			_, errs[i] = GetGroupbuySrv().JoinGroup(context.Background(), uid, groupID, 0, 0, testPayKey)
		}(i, uid)
	}
	wg.Wait()
	return errs
}

// 开团 / 参团必须带 Idempotency-Key，连点不会建两个团、扣两次钱。
func TestCreateAndJoinRequireIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	public := router.Group("/api/v1")
	authed := public.Group("/")
	authed.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(ctl.NewContext(c.Request.Context(), &ctl.UserInfo{Id: 1}))
	})
	RegisterRoutes(public, authed, authed.Group("/"), authed.Group("/admin"))

	for _, path := range []string{"/api/v1/groupbuy/create", "/api/v1/groupbuy/1/join"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"product_id":1,"target_count":2}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		var resp struct {
			Status int    `json:"status"`
			Data   string `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s decode %q: %v", path, w.Body.String(), err)
		}
		if resp.Status != e.InvalidParams || resp.Data != "缺少 Idempotency-Key" {
			t.Fatalf("%s 未挂幂等中间件: %s", path, w.Body.String())
		}
	}
}

// 同一用户并发参团两次：都过了 HasUserJoined 预检，后到的撞唯一索引，必须返回 81003 而不是原始 DB 错误，且只扣一次钱。
func TestConcurrentDuplicateJoinReturnsDuplicateCode(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	seedGroupbuyUser(t, db, 2, 0)
	seedGroupbuyUser(t, db, 3, 100000)
	p := seedGroupbuyProduct(t, db, 8000)
	created, err := GetGroupbuySrv().CreateGroup(context.Background(), 1, p.ID, 3, 0, 0, 0, testPayKey)
	if err != nil {
		t.Fatal(err)
	}

	barrierHook(t, 2)
	errs := concurrentJoin(created.GroupID, 3, 3)

	ok, dup := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrGroupbuyDuplicateJoin):
			dup++
		default:
			t.Fatalf("重复参团应返回 81003, got %v", err)
		}
	}
	if ok != 1 || dup != 1 {
		t.Fatalf("ok=%d dup=%d, want 1/1", ok, dup)
	}
	if got := groupbuyUserBalance(t, db, 3); got != 100000-8000 {
		t.Fatalf("只能扣一次钱, balance=%d", got)
	}
	var g GroupbuyGroup
	db.First(&g, created.GroupID)
	if g.CurrentCount != 2 {
		t.Fatalf("current_count=%d, want 2", g.CurrentCount)
	}
}

// 两人并发抢最后一个名额：只有一人成功，另一人拿到满员 / 已关闭业务码且不扣钱。
func TestConcurrentLastSlotOnlyOneWins(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	seedGroupbuyUser(t, db, 2, 0)
	seedGroupbuyUser(t, db, 3, 100000)
	seedGroupbuyUser(t, db, 4, 100000)
	p := seedGroupbuyProduct(t, db, 8000)
	created, err := GetGroupbuySrv().CreateGroup(context.Background(), 1, p.ID, 2, 0, 0, 0, testPayKey)
	if err != nil {
		t.Fatal(err)
	}

	barrierHook(t, 2)
	errs := concurrentJoin(created.GroupID, 3, 4)

	winners := 0
	for i, err := range errs {
		uid := uint(3 + i)
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, ErrGroupbuyFull) && !errors.Is(err, ErrGroupbuyClosed) {
			t.Fatalf("抢名额失败应返回 81001/81004, got %v", err)
		}
		if got := groupbuyUserBalance(t, db, uid); got != 100000 {
			t.Fatalf("没抢到名额不能扣钱 user=%d balance=%d", uid, got)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d, want 1", winners)
	}
	var g GroupbuyGroup
	db.First(&g, created.GroupID)
	if g.CurrentCount != 2 || g.Status != GroupbuyStatusSuccess {
		t.Fatalf("group count=%d status=%d, want 2/success", g.CurrentCount, g.Status)
	}
}
