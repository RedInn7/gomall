package cart

import (
	"context"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/pkg/e"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 购物车域的 DB 闭环测试：sqlite in-memory，覆盖加购（首次/重复/超上限）、
// 改数量、删除，以及商品不存在时的拒绝路径。
//
// 注意：CartList 依赖 ListCartByUserId 的手写 SELECT，其中用到 MySQL 方言
// （UNIX_TIMESTAMP、限定名引用保留字 check：`c.check` 在 sqlite 是语法错误，
// MySQL 允许保留字跟在限定符后），sqlite 下无法执行，因此列表 DTO 组装
// 不在本组覆盖范围内。

func setupSQLiteForCart(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:cart-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
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
