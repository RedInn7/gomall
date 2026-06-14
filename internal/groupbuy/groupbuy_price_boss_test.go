package groupbuy

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/address"
	orderpkg "github.com/RedInn7/gomall/internal/order"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/snowflake"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 这组用例锁住拼团域的信任边界：卖家以商品表为准、拼团价被服务端封顶 + 兜底，
// 客户端传入的 boss_id / price_cents 不再能决定打款方与成交价。
// 依赖 sqlite in-memory + Redis DB15（与 order 测试同套路），环境缺失则整组 skip。

func setupGroupbuyDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:groupbuy-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(
		&user.User{}, &orderpkg.Order{}, &product.Product{}, &address.Address{},
		&GroupbuyGroup{}, &GroupbuyMember{}, &outbox.OutboxEvent{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

// seedGroupbuyAddress 给 userID 建一条收货地址并返回 id：建团/参团现在校验地址归属。
func seedGroupbuyAddress(t *testing.T, db *gorm.DB, userID uint) uint {
	t.Helper()
	a := &address.Address{UserID: userID, Name: "收货人", Phone: "13800000000", Address: "测试地址"}
	if err := db.Create(a).Error; err != nil {
		t.Fatalf("create address: %v", err)
	}
	return a.ID
}

func setupGroupbuyRedis(t *testing.T) func() {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 10})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis 127.0.0.1:6379 不可用：%v", err)
	}
	prev := cache.RedisClient
	cache.RedisClient = c
	return func() {
		c.FlushDB(context.Background())
		c.Close()
		cache.RedisClient = prev
	}
}

func ensureSnowflakeGroupbuy() {
	defer func() { _ = recover() }()
	snowflake.InitSnowflake(11)
}

// seedGroupbuyProduct 建商品（单价 priceYuan 元、卖家 bossID、库存 stock）并初始化 Redis 库存桶。
func seedGroupbuyProduct(t *testing.T, db *gorm.DB, priceYuan string, bossID uint, stock int) *product.Product {
	t.Helper()
	p := &product.Product{
		Name:          "gb-product",
		CategoryID:    10,
		Num:           stock,
		BossID:        bossID,
		Price:         priceYuan,
		DiscountPrice: priceYuan,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}
	if err := cache.InitStock(context.Background(), p.ID, int64(stock)); err != nil {
		t.Fatalf("init stock: %v", err)
	}
	return p
}

// TestCreateGroup_AuthoritativeBossAndPriceClamp 团长篡改 boss_id/价格不再生效：
// 卖家锁定为商品真实卖家；拼团价落在 [5折, 原价] 内才放行。
func TestCreateGroup_AuthoritativeBossAndPriceClamp(t *testing.T) {
	initLogForTest()
	rcleanup := setupGroupbuyRedis(t)
	defer rcleanup()
	db, dcleanup := setupGroupbuyDB(t)
	defer dcleanup()
	ensureSnowflakeGroupbuy()

	const realBoss = uint(7)
	p := seedGroupbuyProduct(t, db, "100.00", realBoss, 10) // 原价 10000 分，floor=5000 分

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	addr := seedGroupbuyAddress(t, db, 42)

	// 合法拼团价 8000 分（在 [5000,10000] 内）；boss_id 篡改成攻击者 999
	resp, err := GetGroupbuySrv().CreateGroup(ctx, 42, p.ID, 3, 8000, 0, 999, addr)
	if err != nil {
		t.Fatalf("CreateGroup（合法价）应成功: %v", err)
	}
	var leaderOrder orderpkg.Order
	if e := db.First(&leaderOrder, resp.OrderID).Error; e != nil {
		t.Fatalf("reload leader order: %v", e)
	}
	if leaderOrder.BossID != realBoss {
		t.Fatalf("leader order BossID=%d, want %d（商品真实卖家，拒绝客户端 999）", leaderOrder.BossID, realBoss)
	}
	if leaderOrder.Money != 8000 {
		t.Fatalf("leader order Money=%d, want 8000（已服务端校验的拼团价）", leaderOrder.Money)
	}

	// 越界拼团价：1 分（低于 floor 5000）必须拒单
	if _, e := GetGroupbuySrv().CreateGroup(ctx, 43, p.ID, 3, 1, 0, realBoss, 1); e == nil {
		t.Fatal("CreateGroup（1 分薅羊毛价）应被拒，却成功了")
	}
	// 越界拼团价：高于原价（20000 > 10000）必须拒单
	if _, e := GetGroupbuySrv().CreateGroup(ctx, 44, p.ID, 3, 20000, 0, realBoss, 1); e == nil {
		t.Fatal("CreateGroup（高于原价）应被拒，却成功了")
	}
}

// TestJoinGroup_AuthoritativeBoss 参团下单同样锁定商品真实卖家，忽略客户端 boss_id。
func TestJoinGroup_AuthoritativeBoss(t *testing.T) {
	initLogForTest()
	rcleanup := setupGroupbuyRedis(t)
	defer rcleanup()
	db, dcleanup := setupGroupbuyDB(t)
	defer dcleanup()
	ensureSnowflakeGroupbuy()

	const realBoss = uint(7)
	p := seedGroupbuyProduct(t, db, "100.00", realBoss, 10)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 42})
	leaderAddr := seedGroupbuyAddress(t, db, 42)
	joinerAddr := seedGroupbuyAddress(t, db, 50)
	created, err := GetGroupbuySrv().CreateGroup(ctx, 42, p.ID, 3, 8000, 0, realBoss, leaderAddr)
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}

	// 第二名用户参团，boss_id 篡改成 999；用属于自己的地址
	joinCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 50})
	joined, err := GetGroupbuySrv().JoinGroup(joinCtx, 50, created.GroupID, 999, joinerAddr)
	if err != nil {
		t.Fatalf("JoinGroup: %v", err)
	}
	var joinOrder orderpkg.Order
	if e := db.First(&joinOrder, joined.OrderID).Error; e != nil {
		t.Fatalf("reload join order: %v", e)
	}
	if joinOrder.BossID != realBoss {
		t.Fatalf("join order BossID=%d, want %d（商品真实卖家，拒绝客户端 999）", joinOrder.BossID, realBoss)
	}
}
