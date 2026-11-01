package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	bwsnowflake "github.com/bwmarrin/snowflake"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/address"
	"github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	util "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

const testBossID = 77

// 1000 个并发请求（200 个用户各抢 5 次）抢 10 件：成交恰好 10 单、每人最多一单，全部按秒杀价、活动卖家建单。
func TestSeckillConcurrentGrabNeverOversells(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 10, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	const users, tries = 200, 5
	env.seedAddresses(t, users)

	var wg sync.WaitGroup
	for i := 0; i < users*tries; i++ {
		wg.Add(1)
		go func(uid uint) {
			defer wg.Done()
			_, _ = GetSkillProductSrv().SkillProduct(asUser(uid), &SkillProductReq{SkillProductId: sp.Id, AddressId: uid})
		}(uint(i%users) + 1)
	}
	wg.Wait()
	env.drainConcurrent(t, 8)

	var orders []order.Order
	if err := env.db.Where("seckill_id = ?", sp.Id).Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if len(orders) != 10 {
		t.Fatalf("应成交 10 单, got %d", len(orders))
	}
	seen := map[uint]bool{}
	for _, o := range orders {
		if seen[o.UserID] {
			t.Fatalf("用户 %d 成交了多单", o.UserID)
		}
		seen[o.UserID] = true
		if o.Money != sp.PriceCents || o.BossID != testBossID || o.Type != consts.OrderWaitPay || o.Num != 1 {
			t.Fatalf("订单字段不对: %+v", o)
		}
	}
	got := env.reload(t, sp.Id)
	if got.Sold != 10 {
		t.Fatalf("sold=%d want 10", got.Sold)
	}
	if n := env.redisInt(t, cache.SkillStockKey(sp.Id)); n != 0 {
		t.Fatalf("redis 秒杀库存应为 0, got %d", n)
	}
	if n := env.redisInt(t, cache.StockReservedKey(sp.ProductId)); n != 10 {
		t.Fatalf("商品两桶 reserved 应为 10, got %d", n)
	}
}

// Redis 不是唯一防线：Redis 库存被放大（丢数据后错误预热等），DB 的条件 UPDATE 仍只放过 num 件，
// 被挡下的请求归还 Redis 名额与商品预占。
func TestSeckillDBGuardWhenRedisDrifts(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 2, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 5)
	env.mr.Set(cache.SkillStockKey(sp.Id), "100")

	for uid := uint(1); uid <= 5; uid++ {
		if _, err := GetSkillProductSrv().SkillProduct(asUser(uid), &SkillProductReq{SkillProductId: sp.Id, AddressId: uid}); err != nil {
			t.Fatalf("user %d grab: %v", uid, err)
		}
	}
	env.drainConcurrent(t, 5) // 5 个 consumer 同时落库，DB 条件 UPDATE 在并发事务下也只放过 num 件

	if n := env.countOrders(t, sp.Id); n != 2 {
		t.Fatalf("DB 兜底应只成交 2 单, got %d", n)
	}
	if n := env.redisInt(t, cache.StockReservedKey(sp.ProductId)); n != 2 {
		t.Fatalf("被挡下的单应释放商品预占, reserved=%d", n)
	}
	// DB 判售罄的单只还用户名额、不加回库存：Redis 多出来的那件就此消掉，不会一直放请求打到 DB
	if n := env.redisInt(t, cache.SkillStockKey(sp.Id)); n != 95 {
		t.Fatalf("售罄拒单不应加回库存, stock=%d", n)
	}
	if n, _ := env.rdb.HLen(context.Background(), cache.SkillUsersKey(sp.Id)).Result(); n != 2 {
		t.Fatalf("用户名额只应剩成交的 2 人, got %d", n)
	}
	env.assertNoInflight(t, sp.Id)
	RunSeckillReconcile() // 整体对账把剩下的虚高纠回 num - sold
	if n := env.redisInt(t, cache.SkillStockKey(sp.Id)); n != 0 {
		t.Fatalf("对账后 Redis 库存应为 0, got %d", n)
	}
}

// 同一请求重复 init：只有一条活动、Redis 库存不翻倍；配置不同报冲突；商品不存在报错。
func TestInitSkillGoodsIdempotent(t *testing.T) {
	env := newSkillEnv(t)
	start := time.Now().Add(time.Hour).Truncate(time.Second)
	req := &SkillProductInitReq{ProductId: env.productID, PriceCents: 990, Num: 5, StartAt: start, EndAt: start.Add(time.Hour)}

	first, err := GetSkillProductSrv().InitSkillGoods(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	env.rdb.Decr(context.Background(), cache.SkillStockKey(first.Id)) // 模拟已被抢走一件
	second, err := GetSkillProductSrv().InitSkillGoods(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if second.Id != first.Id || second.PerUserLimit != 1 || second.BossId != testBossID || second.OriginCents != 10000 {
		t.Fatalf("重复 init 应返回同一活动: first=%+v second=%+v", first, second)
	}
	var n int64
	env.db.Model(&SkillProduct{}).Count(&n)
	if n != 1 {
		t.Fatalf("活动行数=%d want 1", n)
	}
	if got := env.redisInt(t, cache.SkillStockKey(first.Id)); got != 4 {
		t.Fatalf("重复预热不能覆盖运行中的库存, got %d", got)
	}

	conflict := *req
	conflict.Num = 6
	if _, err := GetSkillProductSrv().InitSkillGoods(context.Background(), &conflict); e.CodeOf(err) != e.ErrSkillConflict {
		t.Fatalf("配置不同应冲突, got %v", err)
	}
	missing := *req
	missing.ProductId = 999
	if _, err := GetSkillProductSrv().InitSkillGoods(context.Background(), &missing); e.CodeOf(err) != e.ErrorNotExistProduct {
		t.Fatalf("商品不存在应报错, got %v", err)
	}
	tooCheap := *req
	tooCheap.PriceCents = 20000
	tooCheap.StartAt = start.Add(time.Minute)
	if _, err := GetSkillProductSrv().InitSkillGoods(context.Background(), &tooCheap); err == nil {
		t.Fatal("秒杀价高于原价应拒绝")
	}
}

func TestSeckillRejectsOutsideWindow(t *testing.T) {
	env := newSkillEnv(t)
	env.seedAddresses(t, 1)
	notStarted := env.activity(t, 5, 1, time.Now().Add(time.Hour), time.Now().Add(2*time.Hour))
	ended := env.activity(t, 5, 1, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))

	_, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: notStarted.Id, AddressId: 1})
	if e.CodeOf(err) != e.ErrSkillNotStarted {
		t.Fatalf("未开始应拒绝, got %v", err)
	}
	_, err = GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: ended.Id, AddressId: 1})
	if e.CodeOf(err) != e.ErrSkillEnded {
		t.Fatalf("已结束应拒绝, got %v", err)
	}
	if got := env.redisInt(t, cache.SkillStockKey(notStarted.Id)); got != 5 {
		t.Fatalf("被拒请求不能扣名额, stock=%d", got)
	}
}

// 投递失败、consumer 建单失败都归还 Redis 名额和商品预占，用户可以再抢。
func TestSeckillCompensatesOnFailure(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 1, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)
	req := &SkillProductReq{SkillProductId: sp.Id, AddressId: 1}

	env.publishErr = errors.New("mq down")
	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), req); err == nil {
		t.Fatal("投递失败应报错")
	}
	env.assertQuotaReturned(t, sp)

	env.publishErr = nil
	order.SetAsyncOrderWriter(func(context.Context, order.AsyncOrderTask, *order.Order) error {
		return errors.New("db down")
	})
	resp, err := GetSkillProductSrv().SkillProduct(asUser(1), req)
	if err != nil {
		t.Fatal(err)
	}
	env.drain(t)
	st, err := order.GetOrderSrv().OrderStatus(context.Background(), resp.Ticket)
	if err != nil || st.Status != order.OrderTicketStatusFailed {
		t.Fatalf("建单失败 ticket 应为 failed: %+v %v", st, err)
	}
	env.assertQuotaReturned(t, sp)
	if n := env.countOrders(t, sp.Id); n != 0 {
		t.Fatalf("不应有订单, got %d", n)
	}
}

// 超时关单：订单关闭、DB sold / 成单记录归还、Redis 名额与商品预占归还，同一用户可以再抢。
func TestSeckillTimeoutCloseReturnsQuota(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 1, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 2)
	env.createXcashTable(t)

	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: sp.Id, AddressId: 1}); err != nil {
		t.Fatal(err)
	}
	env.drain(t)
	if _, err := GetSkillProductSrv().SkillProduct(asUser(2), &SkillProductReq{SkillProductId: sp.Id, AddressId: 2}); e.CodeOf(err) != e.ErrSkillSoldOut {
		t.Fatalf("抢完后应提示抢完, got %v", err)
	}
	var o order.Order
	if err := env.db.Where("seckill_id = ?", sp.Id).First(&o).Error; err != nil {
		t.Fatal(err)
	}

	if err := order.CancelUnpaidOrder(o.OrderNum); err != nil {
		t.Fatal(err)
	}
	if err := order.CancelUnpaidOrder(o.OrderNum); err != nil { // 重复关单幂等
		t.Fatal(err)
	}
	var closed order.Order
	env.db.First(&closed, o.ID)
	if closed.Type != consts.OrderClosed {
		t.Fatalf("订单应关闭, type=%d", closed.Type)
	}
	if got := env.reload(t, sp.Id); got.Sold != 0 {
		t.Fatalf("sold 应归还为 0, got %d", got.Sold)
	}
	var su SkillUser
	env.db.Where("skill_product_id = ? AND user_id = ?", sp.Id, 1).First(&su)
	if su.Bought != 0 {
		t.Fatalf("成单记录应归还, bought=%d", su.Bought)
	}
	env.assertQuotaReturned(t, sp)

	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: sp.Id, AddressId: 1}); err != nil {
		t.Fatalf("归还后可以再抢: %v", err)
	}
	env.drain(t)
	if n := env.countOrders(t, sp.Id); n != 2 {
		t.Fatalf("应有关闭单 + 新单共 2 张, got %d", n)
	}
}

// 客户端在请求里带价格 / 卖家 / 商品都没用：订单按活动价、活动卖家、活动商品建。
func TestSeckillIgnoresClientPrice(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 3, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := fmt.Sprintf(`{"skill_product_id":%d,"address_id":1,"price_cents":1,"money":1,"boss_id":1,"product_id":12345}`, sp.Id)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/skill_product/skill", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	c.Request = r.WithContext(ctl.NewContext(r.Context(), &ctl.UserInfo{Id: 1}))
	SkillProductHandler()(c)
	var resp struct {
		Status int `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Status != 200 {
		t.Fatalf("下单失败: %s", w.Body.String())
	}
	env.drain(t)

	var o order.Order
	if err := env.db.Where("seckill_id = ?", sp.Id).First(&o).Error; err != nil {
		t.Fatal(err)
	}
	if o.Money != sp.PriceCents || o.BossID != testBossID || o.ProductID != env.productID {
		t.Fatalf("订单应按活动定价: money=%d boss=%d product=%d", o.Money, o.BossID, o.ProductID)
	}
}

// 崩溃在抢名额与投递之间：在途 ticket 没有状态，过了宽限期对账按 ticket 归还。
func TestSeckillReconcileRecoversLeakedQuota(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 3, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err := grab(context.Background(), sp, 9, order.NewTicket()); err != nil {
		t.Fatal(err)
	}

	RunSeckillReconcile() // 宽限期内：可能只是还没写 pending，不动
	if got := env.redisInt(t, cache.SkillStockKey(sp.Id)); got != 2 {
		t.Fatalf("宽限期内不应归还, got %d", got)
	}
	orphanTicketGrace = 0
	t.Cleanup(func() { orphanTicketGrace = time.Minute })
	RunSeckillReconcile()

	if got := env.redisInt(t, cache.SkillStockKey(sp.Id)); got != 3 {
		t.Fatalf("泄漏的库存应补回, got %d", got)
	}
	if ok, _ := env.rdb.HExists(context.Background(), cache.SkillUsersKey(sp.Id), "9").Result(); ok {
		t.Fatal("泄漏的用户名额应清掉")
	}
	env.assertNoInflight(t, sp.Id)
}

// MQ 积压：排队中的 ticket 不论多久都算合法占用，对账不补库存、不删用户名额；落库后 Redis 与 DB 一致。
// Redis 两个方向的漂移在静止时按快照精确纠正。
func TestSeckillReconcileKeepsQueuedTickets(t *testing.T) {
	env := newSkillEnv(t)
	orphanTicketGrace = 0
	t.Cleanup(func() { orphanTicketGrace = time.Minute })
	sp := env.activity(t, 3, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 2)
	for uid := uint(1); uid <= 2; uid++ {
		if _, err := GetSkillProductSrv().SkillProduct(asUser(uid), &SkillProductReq{SkillProductId: sp.Id, AddressId: uid}); err != nil {
			t.Fatal(err)
		}
	}
	// orphanTicketGrace=0：只剩 ticket 状态 pending 这一条在保护排队中的单
	RunSeckillReconcile()
	if got := env.redisInt(t, cache.SkillStockKey(sp.Id)); got != 1 {
		t.Fatalf("排队中的单不能被当泄漏补回, stock=%d", got)
	}
	if n, _ := env.rdb.HLen(context.Background(), cache.SkillUsersKey(sp.Id)).Result(); n != 2 {
		t.Fatalf("排队中用户的名额不能删, got %d", n)
	}

	// 商品两桶的预占对账同理：排队中的预占不是泄漏
	new(order.OrderTaskService).RunStockReservationReconcile()
	if n := env.redisInt(t, cache.StockReservedKey(sp.ProductId)); n != 2 {
		t.Fatalf("排队中的商品预占不能被回收, reserved=%d", n)
	}

	env.drain(t)
	if n := env.countOrders(t, sp.Id); n != 2 {
		t.Fatalf("积压消化后应成交 2 单, got %d", n)
	}
	if n, _ := env.rdb.ZCard(context.Background(), cache.OrderAsyncInflightKey(sp.ProductId)).Result(); n != 0 {
		t.Fatalf("落库后商品排队标记应清掉, got %d", n)
	}
	env.assertNoInflight(t, sp.Id)
	if got := env.redisInt(t, cache.SkillStockKey(sp.Id)); got != 1 {
		t.Fatalf("stock=%d want 1", got)
	}

	env.mr.Set(cache.SkillStockKey(sp.Id), "3") // Redis 多了
	env.rdb.HDel(context.Background(), cache.SkillUsersKey(sp.Id), "2")
	RunSeckillReconcile()
	if got := env.redisInt(t, cache.SkillStockKey(sp.Id)); got != 1 {
		t.Fatalf("多出的库存应扣回, stock=%d", got)
	}
	if v, _ := env.rdb.HGet(context.Background(), cache.SkillUsersKey(sp.Id), "2").Result(); v != "1" {
		t.Fatalf("丢掉的用户名额应补回, got %q", v)
	}
	env.mr.Set(cache.SkillStockKey(sp.Id), "0") // Redis 少了
	RunSeckillReconcile()
	if got := env.redisInt(t, cache.SkillStockKey(sp.Id)); got != 1 {
		t.Fatalf("少掉的库存应补回, stock=%d", got)
	}
}

// 排队超过 AsyncTaskMaxAge 的秒杀任务不再建单，按 ticket 归还名额与预占（之后对账回收在途标记时不会两边都处理）。
func TestSeckillStaleTaskAborts(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 1, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)
	ticket := strconv.FormatInt((time.Now().Add(-order.AsyncTaskMaxAge-time.Minute).UnixMilli()-bwsnowflake.Epoch)<<22, 10)
	if err := grab(context.Background(), sp, 1, ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := order.GetOrderSrv().SeckillEnqueue(asUser(1), &order.OrderCreateReq{ProductID: sp.ProductId, Num: 1, AddressID: 1}, sp.Id, ticket); err != nil {
		t.Fatal(err)
	}
	env.drain(t)
	if n := env.countOrders(t, sp.Id); n != 0 {
		t.Fatalf("过期任务不应建单, got %d", n)
	}
	env.assertQuotaReturned(t, sp)
}

// 关单提交后写 Redis 失败：对账按已关单订单补归还，且只还一次。
func TestSeckillReconcileReturnsMissedCancel(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 1, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)
	env.createXcashTable(t)
	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: sp.Id, AddressId: 1}); err != nil {
		t.Fatal(err)
	}
	env.drain(t)
	var o order.Order
	env.db.Where("seckill_id = ?", sp.Id).First(&o)

	// 模拟关单的 Redis 归还没执行：先让 Redis 名额保持占用，DB 照常关单
	keys := cache.SkillKeys(sp.Id)
	ctx := context.Background()
	if err := order.CancelUnpaidOrder(o.OrderNum); err != nil {
		t.Fatal(err)
	}
	env.rdb.SRem(ctx, keys[3], o.ID)
	env.rdb.Decr(ctx, keys[0])
	env.rdb.HIncrBy(ctx, keys[1], "1", 1)

	RunSeckillReconcile()
	RunSeckillReconcile()
	if got := env.redisInt(t, keys[0]); got != 1 {
		t.Fatalf("关单名额应补归还且只还一次, stock=%d", got)
	}
	if n, _ := env.rdb.HLen(ctx, keys[1]).Result(); n != 0 {
		t.Fatalf("用户名额应归还, got %d", n)
	}
}

// 商品下架后秒杀单不再成交，名额与预占归还。
func TestSeckillRejectsOffSaleProduct(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 2, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)
	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: sp.Id, AddressId: 1}); err != nil {
		t.Fatal(err)
	}
	env.db.Model(&product.Product{}).Where("id = ?", env.productID).Update("on_sale", false)
	env.drain(t)
	if n := env.countOrders(t, sp.Id); n != 0 {
		t.Fatalf("下架商品不应成交, got %d", n)
	}
	env.assertQuotaReturned(t, sp)

	start := time.Now().Add(time.Hour)
	if _, err := GetSkillProductSrv().InitSkillGoods(context.Background(), &SkillProductInitReq{
		ProductId: env.productID, PriceCents: 1, Num: 1, StartAt: start, EndAt: start.Add(time.Hour)}); err == nil {
		t.Fatal("下架商品不应能创建活动")
	}
}

// 每人限购 2：同一用户的两张单并发落库都应成交，不会撞唯一索引被误拒。
func TestSeckillPerUserLimitConcurrentFirstOrders(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 5, 2, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)
	for i := 0; i < 3; i++ {
		_, _ = GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: sp.Id, AddressId: 1})
	}
	env.drainConcurrent(t, 2)
	if n := env.countOrders(t, sp.Id); n != 2 {
		t.Fatalf("限购 2 应成交 2 单, got %d", n)
	}
	var su SkillUser
	env.db.Where("skill_product_id = ? AND user_id = ?", sp.Id, 1).First(&su)
	if su.Bought != 2 {
		t.Fatalf("bought=%d want 2", su.Bought)
	}
}

// 旧调用方式：只给 product_id、不给 address_id，按商品进行中的场次、用户第一个地址下单。
func TestSeckillLegacyRequestByProduct(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 2, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	env.seedAddresses(t, 1)
	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{ProductId: env.productID}); err != nil {
		t.Fatal(err)
	}
	env.drain(t)
	var o order.Order
	if err := env.db.Where("seckill_id = ?", sp.Id).First(&o).Error; err != nil || o.AddressID != 1 {
		t.Fatalf("应按活动 %d、地址 1 成交: %+v %v", sp.Id, o, err)
	}
	d, err := GetSkillProductSrv().GetSkillGoods(context.Background(), &GetSkillProductReq{ProductId: env.productID})
	if err != nil || d.Id != sp.Id || d.Remaining != 1 {
		t.Fatalf("按商品查详情: %+v %v", d, err)
	}
}

// 活动 id 在创建前被查过（缓存了 "null"）：创建后立刻能查到、能抢。用户名额等 key 都有 TTL。
func TestSeckillInitOverridesNullCacheAndKeysExpire(t *testing.T) {
	env := newSkillEnv(t)
	env.seedAddresses(t, 1)
	ctx := context.Background()
	if _, err := GetSkillProductSrv().GetSkillGoods(ctx, &GetSkillProductReq{SkillProductId: 1}); e.CodeOf(err) != e.ErrSkillNotExist {
		t.Fatalf("创建前应不存在, got %v", err)
	}
	sp, err := GetSkillProductSrv().InitSkillGoods(ctx, &SkillProductInitReq{ProductId: env.productID, PriceCents: 990, Num: 2,
		StartAt: time.Now().Add(-time.Second), EndAt: time.Now().Add(time.Hour)})
	if err != nil || sp.Id != 1 {
		t.Fatalf("init: %+v %v", sp, err)
	}
	if _, err := GetSkillProductSrv().SkillProduct(asUser(1), &SkillProductReq{SkillProductId: sp.Id, AddressId: 1}); err != nil {
		t.Fatalf("创建后应立刻能抢: %v", err)
	}
	for _, k := range cache.SkillKeys(sp.Id)[:3] {
		if ttl := env.mr.TTL(k); ttl <= 0 {
			t.Fatalf("%s 应有 TTL, got %v", k, ttl)
		}
	}
}

// 读路径：不存在的 id 缓存空值、并发回源只写一份列表、列表与详情返回同一结构。
func TestSkillReadPathCache(t *testing.T) {
	env := newSkillEnv(t)
	sp := env.activity(t, 3, 1, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	ctx := context.Background()

	if _, err := GetSkillProductSrv().GetSkillGoods(ctx, &GetSkillProductReq{SkillProductId: 404}); e.CodeOf(err) != e.ErrSkillNotExist {
		t.Fatalf("不存在应报 ErrSkillNotExist, got %v", err)
	}
	if v, _ := env.mr.Get(fmt.Sprintf(cache.SkillProductKey, 404)); v != "null" {
		t.Fatalf("应缓存空值, got %q", v)
	}
	if ttl := env.mr.TTL(fmt.Sprintf(cache.SkillProductKey, 404)); ttl <= 0 || ttl > cache.SkillNullTTL {
		t.Fatalf("空值缓存应有短 TTL, got %v", ttl)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := GetSkillProductSrv().ListSkillGoods(ctx)
			if err != nil || len(list) != 1 || list[0].Id != sp.Id {
				t.Errorf("list=%v err=%v", list, err)
			}
		}()
	}
	wg.Wait()
	if ttl := env.mr.TTL(cache.SkillProductListKey); ttl <= 0 {
		t.Fatalf("列表缓存应有 TTL, got %v", ttl)
	}

	detail, err := GetSkillProductSrv().GetSkillGoods(ctx, &GetSkillProductReq{SkillProductId: sp.Id})
	if err != nil || detail.Id != sp.Id || detail.PriceCents != sp.PriceCents || detail.Remaining != 3 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

// ---- 测试环境 ----

type skillEnv struct {
	db         *gorm.DB
	mr         *miniredis.Miniredis
	rdb        *redis.Client
	productID  uint
	mu         sync.Mutex
	queue      [][]byte
	publishErr error
}

func (s *skillEnv) Publish(_ context.Context, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.publishErr != nil {
		return s.publishErr
	}
	s.queue = append(s.queue, payload)
	return nil
}

// drainConcurrent 模拟 n 个 consumer 并发消费
func (s *skillEnv) drainConcurrent(t *testing.T, n int) {
	t.Helper()
	s.mu.Lock()
	q := s.queue
	s.queue = nil
	s.mu.Unlock()
	ch := make(chan []byte)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for body := range ch {
				_ = order.HandleAsyncOrderTask(context.Background(), body)
			}
		}()
	}
	for _, body := range q {
		ch <- body
	}
	close(ch)
	wg.Wait()
}

func (s *skillEnv) createXcashTable(t *testing.T) {
	t.Helper()
	if err := s.db.Exec("CREATE TABLE xcash_payment_intent (id INTEGER PRIMARY KEY, order_id INTEGER, status TEXT, expires_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
}

func (s *skillEnv) assertNoInflight(t *testing.T, id uint) {
	t.Helper()
	if n, _ := s.rdb.ZCard(context.Background(), cache.SkillKeys(id)[2]).Result(); n != 0 {
		t.Fatalf("在途集合应为空, got %d", n)
	}
}

// drain 模拟 MQ consumer 依次消费
func (s *skillEnv) drain(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	q := s.queue
	s.queue = nil
	s.mu.Unlock()
	for _, body := range q {
		_ = order.HandleAsyncOrderTask(context.Background(), body)
	}
}

func newSkillEnv(t *testing.T) *skillEnv {
	t.Helper()
	prevLog := util.LogrusObj
	util.LogrusObj = logrus.New()
	util.LogrusObj.Out = io.Discard
	snowflake.InitSnowflake(1)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 64})
	prevRedis := cache.RedisClient
	cache.RedisClient = rdb

	// 设了 SKILL_TEST_MYSQL_DSN 就跑真 MySQL（行锁 / 唯一索引 / 并发事务按 InnoDB 语义），否则用内存 SQLite。
	cfg := &gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}}
	var db *gorm.DB
	var err error
	dsn := os.Getenv("SKILL_TEST_MYSQL_DSN")
	if dsn != "" {
		db, err = gorm.Open(mysql.Open(dsn), cfg)
	} else {
		db, err = gorm.Open(sqlite.Open(fmt.Sprintf("file:skill-%s?mode=memory&cache=shared", t.Name())), cfg)
	}
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.Logger = db.Logger.LogMode(gormlogger.Silent)
	sqlDB, _ := db.DB()
	if dsn == "" {
		sqlDB.SetMaxOpenConns(1)
	} else if err := db.Migrator().DropTable(&product.Product{}, &address.Address{}, &order.Order{}, &outbox.OutboxEvent{},
		&SkillProduct{}, &SkillUser{}, "xcash_payment_intent"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	prevDB := dao.SetTestDB(db)

	env := &skillEnv{db: db, mr: mr, rdb: rdb}
	order.SetAsyncOrderProducer(env)
	t.Cleanup(func() {
		order.SetAsyncOrderWriter(nil)
		dao.SetTestDB(prevDB)
		cache.RedisClient = prevRedis
		util.LogrusObj = prevLog
		_ = rdb.Close()
		_ = sqlDB.Close()
	})
	order.SetAsyncOrderWriter(nil)
	if err := db.AutoMigrate(&product.Product{}, &address.Address{}, &order.Order{}, &outbox.OutboxEvent{},
		&SkillProduct{}, &SkillUser{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	p := &product.Product{Name: "秒杀测试", Title: "秒杀测试", Price: "100.00", OnSale: true, Num: 1000, BossID: testBossID}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	env.productID = p.ID
	return env
}

// activity 直接落一条活动并预热
func (s *skillEnv) activity(t *testing.T, num, limit int, start, end time.Time) *SkillProduct {
	t.Helper()
	sp := &SkillProduct{ProductId: s.productID, BossId: testBossID, Title: "秒杀测试", PriceCents: 990, OriginCents: 10000,
		Num: num, PerUserLimit: limit, StartAt: start, EndAt: end}
	if err := s.db.Create(sp).Error; err != nil {
		t.Fatal(err)
	}
	if err := preheat(context.Background(), sp); err != nil {
		t.Fatal(err)
	}
	return sp
}

func (s *skillEnv) seedAddresses(t *testing.T, n int) {
	t.Helper()
	for uid := 1; uid <= n; uid++ {
		a := &address.Address{UserID: uint(uid), Name: "n", Phone: "1", Address: "a"}
		a.ID = uint(uid)
		if err := s.db.Create(a).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func (s *skillEnv) reload(t *testing.T, id uint) *SkillProduct {
	t.Helper()
	var sp SkillProduct
	if err := s.db.First(&sp, id).Error; err != nil {
		t.Fatal(err)
	}
	return &sp
}

func (s *skillEnv) countOrders(t *testing.T, skillID uint) int64 {
	t.Helper()
	var n int64
	if err := s.db.Model(&order.Order{}).Where("seckill_id = ?", skillID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func (s *skillEnv) redisInt(t *testing.T, key string) int64 {
	t.Helper()
	n, err := s.rdb.Get(context.Background(), key).Int64()
	if err != nil && !errors.Is(err, redis.Nil) {
		t.Fatal(err)
	}
	return n
}

// assertQuotaReturned 秒杀库存回到 num、用户名额清空、商品预占全部释放
func (s *skillEnv) assertQuotaReturned(t *testing.T, sp *SkillProduct) {
	t.Helper()
	if got := s.redisInt(t, cache.SkillStockKey(sp.Id)); got != int64(sp.Num) {
		t.Fatalf("秒杀库存应归还到 %d, got %d", sp.Num, got)
	}
	if n, _ := s.rdb.HLen(context.Background(), cache.SkillUsersKey(sp.Id)).Result(); n != 0 {
		t.Fatalf("用户名额应清空, got %d", n)
	}
	if got := s.redisInt(t, cache.StockReservedKey(sp.ProductId)); got != 0 {
		t.Fatalf("商品预占应释放, reserved=%d", got)
	}
	s.assertNoInflight(t, sp.Id)
}

func asUser(id uint) context.Context {
	return ctl.NewContext(context.Background(), &ctl.UserInfo{Id: id})
}
