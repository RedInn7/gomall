package groupbuy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/internal/money"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

const testPayKey = "123456"

// 客户端在请求里带 price_cents=1 也没用：团和订单都按商家在商品上配置的拼团价收钱。
func TestCreateGroupIgnoresClientPrice(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	p := seedGroupbuyProduct(t, db, 8000)

	body := fmt.Sprintf(`{"product_id":%d,"target_count":2,"price_cents":1,"key":%q}`, p.ID, testPayKey)
	if status := callCreateHandler(t, 1, body); status != 200 {
		t.Fatalf("开团失败 status=%d", status)
	}

	var g GroupbuyGroup
	if err := db.Where("product_id = ?", p.ID).First(&g).Error; err != nil {
		t.Fatalf("load group: %v", err)
	}
	var o orderpkg.Order
	if err := db.Where("user_id = ?", 1).First(&o).Error; err != nil {
		t.Fatalf("load leader order: %v", err)
	}
	if g.PriceCents != 8000 || o.Money != 8000 {
		t.Fatalf("拼团价应取商家配置 8000 分: group=%d order=%d", g.PriceCents, o.Money)
	}
	if got := groupbuyUserBalance(t, db, 1); got != 100000-8000 {
		t.Fatalf("团长应按拼团价扣款, balance=%d", got)
	}
}

func TestCreateGroupRejectsProductNotOpen(t *testing.T) {
	db := openGroupbuyTestDB(t)
	seedGroupbuyUser(t, db, 1, 100000)
	p := seedGroupbuyProduct(t, db, 0)

	_, err := GetGroupbuySrv().CreateGroup(context.Background(), 1, p.ID, 2, 0, 0, 0, testPayKey)
	if !errors.Is(err, product.ErrGroupbuyNotOpen) {
		t.Fatalf("未开放拼团的商品不能开团, got %v", err)
	}
	var groups int64
	if err := db.Model(&GroupbuyGroup{}).Count(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if groups != 0 {
		t.Fatalf("不应建团, groups=%d", groups)
	}
}

func TestGroupbuyRequiresPayPassword(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	p := seedGroupbuyProduct(t, db, 8000)
	g := &GroupbuyGroup{
		ProductID: p.ID, LeaderID: 9, TargetCount: 2, CurrentCount: 1,
		PriceCents: 8000, Status: GroupbuyStatusOpen, ExpireAt: time.Now().Add(time.Hour),
	}
	if err := db.Create(g).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}

	ctx := context.Background()
	if _, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, 2, 0, 0, 0, "000000"); !errors.Is(err, user.ErrMoneyKeyIncorrect) {
		t.Fatalf("开团支付密码错误应拒绝, got %v", err)
	}
	if _, err := GetGroupbuySrv().JoinGroup(ctx, 1, g.ID, 0, 0, ""); !errors.Is(err, user.ErrMoneyKeyIncorrect) {
		t.Fatalf("参团支付密码错误应拒绝, got %v", err)
	}
	if got := groupbuyUserBalance(t, db, 1); got != 100000 {
		t.Fatalf("密码错误不能扣款, balance=%d", got)
	}
}

// 成团才真正卖出：每个成员扣 1 件 DB 库存，否则 DB 水位虚高，后续按 DB 校验的支付会超卖。
func TestMarkGroupSuccessDeductsProductStock(t *testing.T) {
	db := openGroupbuyTestDB(t)
	stubInventoryLua(t)
	seedGroupbuyUser(t, db, 1, 100000)
	seedGroupbuyUser(t, db, 2, 0) // 卖家
	seedGroupbuyUser(t, db, 3, 100000)
	p := seedGroupbuyProduct(t, db, 8000)

	ctx := context.Background()
	created, err := GetGroupbuySrv().CreateGroup(ctx, 1, p.ID, 2, 0, 0, 0, testPayKey)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	joined, err := GetGroupbuySrv().JoinGroup(ctx, 3, created.GroupID, 0, 0, testPayKey)
	if err != nil || !joined.IsSuccess {
		t.Fatalf("join should complete the group: resp=%+v err=%v", joined, err)
	}

	var got product.Product
	if err := db.First(&got, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Num != 3 {
		t.Fatalf("product stock after group success = %d, want 3", got.Num)
	}
}

// okLuaHook 让所有 Redis 命令直接放行、不连真实 Redis：库存 Lua 返回 1，
// 支付密码限次的滑动窗口脚本返回 {1, 1}。
type okLuaHook struct{}

func (okLuaHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (okLuaHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		if c, ok := cmd.(*redis.Cmd); ok {
			// 滑动窗口脚本是 1 个 key + 4 个参数：evalsha sha 1 key a b c d 共 8 个 args
			if len(c.Args()) == 8 {
				c.SetVal([]interface{}{int64(1), int64(1)})
				return nil
			}
			c.SetVal(int64(1))
		}
		return nil
	}
}
func (okLuaHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func stubInventoryLua(t *testing.T) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	client.AddHook(okLuaHook{})
	prev := cache.RedisClient
	cache.RedisClient = client
	t.Cleanup(func() {
		cache.RedisClient = prev
		_ = client.Close()
	})
}

func callCreateHandler(t *testing.T, userID uint, body string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groupbuy/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req.WithContext(ctl.NewContext(req.Context(), &ctl.UserInfo{Id: userID}))
	GroupbuyCreateHandler()(c)
	var resp struct {
		Status int    `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	if resp.Status != 200 {
		t.Logf("handler error: %s", resp.Error)
	}
	return resp.Status
}

func openGroupbuyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	prevConf, prevLog := conf.Config, util.LogrusObj
	conf.Config = &conf.Conf{EncryptSecret: &conf.EncryptSecret{MoneySecret: "groupbuy-test-secret"}}
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
	snowflake.InitSnowflake(1)

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:groupbuy-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	prevDB := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(prevDB)
		conf.Config, util.LogrusObj = prevConf, prevLog
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&user.User{}, &product.Product{}, &orderpkg.Order{}, &GroupbuyGroup{}, &GroupbuyMember{},
		&money.AccountTransaction{}, &outbox.OutboxEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedGroupbuyUser(t *testing.T, db *gorm.DB, id uint, balance int64) {
	t.Helper()
	u := &user.User{UserName: fmt.Sprintf("gb-u%d", id), Money: strconv.FormatInt(balance, 10)}
	u.ID = id
	cipher, err := u.EncryptMoney()
	if err != nil {
		t.Fatalf("encrypt balance: %v", err)
	}
	u.Money = cipher
	if err := u.SetMoneyPassword(testPayKey); err != nil {
		t.Fatalf("set pay key: %v", err)
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// seedGroupbuyProduct 零售价 100 元（10000 分），groupbuyCents=0 表示未开放拼团。
func seedGroupbuyProduct(t *testing.T, db *gorm.DB, groupbuyCents int64) *product.Product {
	t.Helper()
	p := &product.Product{Name: "kettle", CategoryID: 1, Price: "100.00", Num: 5, OnSale: true, BossID: 2, GroupbuyPriceCents: groupbuyCents}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return p
}

func groupbuyUserBalance(t *testing.T, db *gorm.DB, id uint) int64 {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	bal, err := u.DecryptMoney()
	if err != nil {
		t.Fatalf("decrypt balance: %v", err)
	}
	return bal
}
