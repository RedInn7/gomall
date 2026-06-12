package cart

import (
	"context"
	"database/sql"
	"io"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/types"
)

// 购物车域的 DB 闭环测试：sqlite in-memory，覆盖加购（首次/重复/超上限）、
// 改数量、删除、列表 DTO 组装，以及商品不存在时的拒绝路径。
//
// ListCartByUserId 的手写 SELECT 用到 MySQL 的 UNIX_TIMESTAMP，
// sqlite 没有该函数，这里通过自定义 driver 注册同名函数补齐方言差异；
// 保留字 check 以 `check` 反引号限定名引用，MySQL/sqlite 均可解析。

var cartSQLiteDriverOnce sync.Once

const cartSQLiteDriverName = "sqlite3_cart_unixts"

func registerCartSQLiteDriver() {
	cartSQLiteDriverOnce.Do(func() {
		sql.Register(cartSQLiteDriverName, &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				return conn.RegisterFunc("UNIX_TIMESTAMP", func(ts string) int64 {
					layouts := []string{
						"2006-01-02 15:04:05.999999999-07:00",
						"2006-01-02 15:04:05.999999999Z07:00",
						"2006-01-02 15:04:05",
						time.RFC3339Nano,
					}
					for _, layout := range layouts {
						if t, err := time.Parse(layout, ts); err == nil {
							return t.Unix()
						}
					}
					return 0
				}, true)
			},
		})
	})
}

func setupSQLiteForCart(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	registerCartSQLiteDriver()
	dsn := "file:cart-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(&sqlite.Dialector{DriverName: cartSQLiteDriverName, DSN: dsn}, &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&Cart{}, &product.Product{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() {
		dao.SetTestDB(prev)
	}
}

func seedCartProduct(t *testing.T, db *gorm.DB, bossID uint) *product.Product {
	t.Helper()
	p := &product.Product{Name: "cart-item", Num: 100, BossID: bossID}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}
	return p
}

func TestCartCreate_FirstAddCreatesRow(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const userID, bossID = uint(101), uint(201)
	p := seedCartProduct(t, db, bossID)

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID})
	if _, err := GetCartSrv().CartCreate(ctx, &CartCreateReq{
		ProductId: p.ID, BossID: bossID,
	}); err != nil {
		t.Fatalf("CartCreate: %v", err)
	}

	var row Cart
	if err := db.Where("user_id=? AND product_id=?", userID, p.ID).First(&row).Error; err != nil {
		t.Fatalf("load cart: %v", err)
	}
	if row.Num != 1 || row.MaxNum != 10 || row.Check {
		t.Fatalf("cart row = num %d / max %d / check %v, want 1/10/false",
			row.Num, row.MaxNum, row.Check)
	}
	if row.BossID != bossID {
		t.Fatalf("boss_id = %d, want %d", row.BossID, bossID)
	}
}

func TestCartCreate_RepeatAddIncrementsNum(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const userID, bossID = uint(102), uint(202)
	p := seedCartProduct(t, db, bossID)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID})

	req := &CartCreateReq{ProductId: p.ID, BossID: bossID}
	if _, err := GetCartSrv().CartCreate(ctx, req); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// 重复加购不报错，行数不变、数量 +1
	if _, err := GetCartSrv().CartCreate(ctx, req); err != nil {
		t.Fatalf("second add: %v", err)
	}

	var rows []Cart
	if err := db.Where("user_id=? AND product_id=?", userID, p.ID).Find(&rows).Error; err != nil {
		t.Fatalf("load carts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expect 1 cart row, got %d", len(rows))
	}
	if rows[0].Num != 2 {
		t.Fatalf("num = %d, want 2", rows[0].Num)
	}
}

func TestCartCreate_OverMaxNumRejected(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const userID, bossID = uint(103), uint(203)
	p := seedCartProduct(t, db, bossID)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID})

	req := &CartCreateReq{ProductId: p.ID, BossID: bossID}
	if _, err := GetCartSrv().CartCreate(ctx, req); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// 把上限压到当前数量，下一次加购应当被拒
	if err := db.Model(&Cart{}).
		Where("user_id=? AND product_id=?", userID, p.ID).
		Update("max_num", 1).Error; err != nil {
		t.Fatalf("update max_num: %v", err)
	}

	_, err := GetCartSrv().CartCreate(ctx, req)
	if err == nil {
		t.Fatal("超过 max_num 应当报错")
	}
	if err.Error() != e.GetMsg(e.ErrorProductMoreCart) {
		t.Fatalf("err = %q, want %q", err.Error(), e.GetMsg(e.ErrorProductMoreCart))
	}

	// 数量不应越过上限
	var row Cart
	if err := db.Where("user_id=? AND product_id=?", userID, p.ID).First(&row).Error; err != nil {
		t.Fatalf("load cart: %v", err)
	}
	if row.Num != 1 {
		t.Fatalf("num = %d, want 1", row.Num)
	}
}

func TestCartCreate_ProductNotFound(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 104})
	_, err := GetCartSrv().CartCreate(ctx, &CartCreateReq{ProductId: 99999, BossID: 1})
	if err == nil {
		t.Fatal("商品不存在应当报错")
	}

	var cnt int64
	db.Model(&Cart{}).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("不应落任何购物车行，got %d", cnt)
	}
}

// TestCartList_AssemblesDTO 覆盖列表闭环：加购后 CartList 返回
// cart × product 两表 join 出来的 DTO，逐字段断言（含保留字 check 列
// 与 UNIX_TIMESTAMP 出来的 created_at）。
func TestCartList_AssemblesDTO(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const userID, bossID = uint(109), uint(209)
	p := &product.Product{
		Name: "cart-item", Info: "商品详情", ImgPath: "item.jpg",
		DiscountPrice: "88", Num: 100,
		BossID: bossID, BossName: "店主",
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID})
	if _, err := GetCartSrv().CartCreate(ctx, &CartCreateReq{
		ProductId: p.ID, BossID: bossID,
	}); err != nil {
		t.Fatalf("CartCreate: %v", err)
	}
	var row Cart
	if err := db.Where("user_id=?", userID).First(&row).Error; err != nil {
		t.Fatalf("load cart: %v", err)
	}

	resp, err := GetCartSrv().CartList(ctx, &CartListReq{})
	if err != nil {
		t.Fatalf("CartList: %v", err)
	}
	dl, ok := resp.(*types.DataListResp)
	if !ok {
		t.Fatalf("resp type = %T", resp)
	}
	if dl.Total != 1 {
		t.Fatalf("total = %d, want 1", dl.Total)
	}
	items, ok := dl.Item.([]*CartResp)
	if !ok || len(items) != 1 {
		t.Fatalf("item type/len 异常：%T", dl.Item)
	}
	got := items[0]
	if got.ID != row.ID || got.UserID != userID || got.ProductID != p.ID {
		t.Fatalf("id/user/product = %d/%d/%d", got.ID, got.UserID, got.ProductID)
	}
	if got.Num != 1 || got.MaxNum != 10 || got.Check_ {
		t.Fatalf("num/max/check = %d/%d/%v, want 1/10/false", got.Num, got.MaxNum, got.Check_)
	}
	if got.BossId != bossID || got.BossName != "店主" {
		t.Fatalf("boss = %d/%q", got.BossId, got.BossName)
	}
	if got.ImgPath != "item.jpg" || got.Info != "商品详情" || got.DiscountPrice != "88" {
		t.Fatalf("img/info/discount = %q/%q/%q", got.ImgPath, got.Info, got.DiscountPrice)
	}
	if got.CreatedAt <= 0 {
		t.Fatalf("created_at 应为正的 unix 秒，got %d", got.CreatedAt)
	}
	if diff := got.CreatedAt - row.CreatedAt.Unix(); diff < -1 || diff > 1 {
		t.Fatalf("created_at = %d, want ≈ %d", got.CreatedAt, row.CreatedAt.Unix())
	}

	// 他人列表为空
	otherCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: userID + 1})
	otherResp, err := GetCartSrv().CartList(otherCtx, &CartListReq{})
	if err != nil {
		t.Fatalf("CartList(other): %v", err)
	}
	if lr := otherResp.(*types.DataListResp); lr.Total != 0 {
		t.Fatalf("他人列表 total = %d, want 0", lr.Total)
	}
}

func TestCartUpdate_ScopedToOwner(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const ownerID, otherID, bossID = uint(105), uint(106), uint(205)
	p := seedCartProduct(t, db, bossID)
	ownerCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: ownerID})
	if _, err := GetCartSrv().CartCreate(ownerCtx, &CartCreateReq{ProductId: p.ID, BossID: bossID}); err != nil {
		t.Fatalf("CartCreate: %v", err)
	}
	var row Cart
	if err := db.Where("user_id=?", ownerID).First(&row).Error; err != nil {
		t.Fatalf("load cart: %v", err)
	}

	// 本人改数量生效
	if _, err := GetCartSrv().CartUpdate(ownerCtx, &UpdateCartServiceReq{Id: row.ID, Num: 5}); err != nil {
		t.Fatalf("CartUpdate: %v", err)
	}
	if err := db.First(&row, row.ID).Error; err != nil {
		t.Fatalf("reload cart: %v", err)
	}
	if row.Num != 5 {
		t.Fatalf("num = %d, want 5", row.Num)
	}

	// 他人拿到 cart id 也改不动：where 条件带 user_id，0 行命中且不报错
	otherCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: otherID})
	if _, err := GetCartSrv().CartUpdate(otherCtx, &UpdateCartServiceReq{Id: row.ID, Num: 99}); err != nil {
		t.Fatalf("跨用户更新不应报错（静默 0 行）: %v", err)
	}
	if err := db.First(&row, row.ID).Error; err != nil {
		t.Fatalf("reload cart: %v", err)
	}
	if row.Num != 5 {
		t.Fatalf("跨用户更新不应生效，num = %d", row.Num)
	}
}

func TestCartDelete_RemovesOwnRowOnly(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	const ownerID, otherID, bossID = uint(107), uint(108), uint(207)
	p := seedCartProduct(t, db, bossID)
	ownerCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: ownerID})
	if _, err := GetCartSrv().CartCreate(ownerCtx, &CartCreateReq{ProductId: p.ID, BossID: bossID}); err != nil {
		t.Fatalf("CartCreate: %v", err)
	}
	var row Cart
	if err := db.Where("user_id=?", ownerID).First(&row).Error; err != nil {
		t.Fatalf("load cart: %v", err)
	}

	// 他人删不掉
	otherCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: otherID})
	if _, err := GetCartSrv().CartDelete(otherCtx, &CartDeleteReq{Id: row.ID}); err != nil {
		t.Fatalf("跨用户删除不应报错（静默 0 行）: %v", err)
	}
	var cnt int64
	db.Model(&Cart{}).Where("id=?", row.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("跨用户删除不应生效，count = %d", cnt)
	}

	// 本人删除生效（软删，常规查询不可见）
	if _, err := GetCartSrv().CartDelete(ownerCtx, &CartDeleteReq{Id: row.ID}); err != nil {
		t.Fatalf("CartDelete: %v", err)
	}
	err := db.First(&Cart{}, row.ID).Error
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("expect ErrRecordNotFound after delete, got %v", err)
	}
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
