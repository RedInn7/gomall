package favorite

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

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/internal/category"
	"github.com/RedInn7/gomall/internal/product"
	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 收藏夹域的 DB 闭环测试：sqlite in-memory，覆盖收藏创建（含重复收藏拒绝）、
// 列表的跨表 DTO 组装（user/product/category 三表 join + 图片地址拼接）、删除。
//
// ListFavoriteByUserId 的手写 SELECT 用到 MySQL 的 UNIX_TIMESTAMP，
// sqlite 没有该函数，这里通过自定义 driver 注册同名函数补齐方言差异。

var favoriteSQLiteDriverOnce sync.Once

const favoriteSQLiteDriverName = "sqlite3_favorite_unixts"

func registerFavoriteSQLiteDriver() {
	favoriteSQLiteDriverOnce.Do(func() {
		sql.Register(favoriteSQLiteDriverName, &sqlite3.SQLiteDriver{
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

func setupSQLiteForFavorite(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	registerFavoriteSQLiteDriver()
	dsn := "file:favorite-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(&sqlite.Dialector{DriverName: favoriteSQLiteDriverName, DSN: dsn},
		&gorm.Config{NamingStrategy: schema.NamingStrategy{SingularTable: true}})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(
		&user.User{}, &product.Product{}, &category.Category{}, &Favorite{},
	); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() {
		dao.SetTestDB(prev)
	}
}

// initFavoriteTestConfig 提供列表组装所需的最小配置：
// local 上传模式 + 图片域名拼接参数。返回恢复函数。
func initFavoriteTestConfig() func() {
	prev := conf.Config
	conf.Config = &conf.Conf{
		System: &conf.System{
			UploadModel: consts.UploadModelLocal,
			HttpPort:    ":5001",
		},
		PhotoPath: &conf.LocalPhotoPath{
			PhotoHost:   "http://127.0.0.1",
			ProductPath: "/product/",
		},
	}
	return func() { conf.Config = prev }
}

type favoriteFixture struct {
	Buyer    *user.User
	Boss     *user.User
	Category *category.Category
	Product  *product.Product
}

func seedFavoriteFixture(t *testing.T, db *gorm.DB) favoriteFixture {
	t.Helper()
	buyer := &user.User{UserName: "u-" + t.Name(), NickName: "买家"}
	if err := db.Create(buyer).Error; err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	boss := &user.User{UserName: "boss-" + t.Name(), NickName: "店主", Avatar: "boss.jpg"}
	if err := db.Create(boss).Error; err != nil {
		t.Fatalf("create boss: %v", err)
	}
	cat := &category.Category{CategoryName: "数码"}
	if err := db.Create(cat).Error; err != nil {
		t.Fatalf("create category: %v", err)
	}
	p := &product.Product{
		Name: "fav-item", CategoryID: cat.ID, Title: "限量款", Info: "详情",
		ImgPath: "item.jpg", Price: "100", DiscountPrice: "90",
		OnSale: true, Num: 5, BossID: boss.ID, BossName: boss.UserName,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}
	return favoriteFixture{Buyer: buyer, Boss: boss, Category: cat, Product: p}
}

func TestFavoriteCreate_AndDuplicateRejected(t *testing.T) {
	initLogForTest()
	restore := initFavoriteTestConfig()
	defer restore()
	db, cleanup := setupSQLiteForFavorite(t)
	defer cleanup()

	fx := seedFavoriteFixture(t, db)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.Buyer.ID})
	req := &FavoriteCreateReq{ProductId: fx.Product.ID, BossId: fx.Boss.ID}

	if _, err := GetFavoriteSrv().FavoriteCreate(ctx, req); err != nil {
		t.Fatalf("FavoriteCreate: %v", err)
	}

	var row Favorite
	if err := db.Where("user_id=? AND product_id=?", fx.Buyer.ID, fx.Product.ID).
		First(&row).Error; err != nil {
		t.Fatalf("load favorite: %v", err)
	}
	if row.BossID != fx.Boss.ID {
		t.Fatalf("boss_id = %d, want %d", row.BossID, fx.Boss.ID)
	}

	// 重复收藏被拒，且不产生第二行
	if _, err := GetFavoriteSrv().FavoriteCreate(ctx, req); err == nil {
		t.Fatal("重复收藏应当报错")
	}
	var cnt int64
	db.Model(&Favorite{}).Where("user_id=?", fx.Buyer.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("favorite rows = %d, want 1", cnt)
	}
}

func TestFavoriteList_AssemblesCrossDomainDTO(t *testing.T) {
	initLogForTest()
	restore := initFavoriteTestConfig()
	defer restore()
	db, cleanup := setupSQLiteForFavorite(t)
	defer cleanup()

	fx := seedFavoriteFixture(t, db)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.Buyer.ID})
	if _, err := GetFavoriteSrv().FavoriteCreate(ctx,
		&FavoriteCreateReq{ProductId: fx.Product.ID, BossId: fx.Boss.ID}); err != nil {
		t.Fatalf("FavoriteCreate: %v", err)
	}

	resp, err := GetFavoriteSrv().FavoriteList(ctx,
		&FavoritesServiceReq{PageNum: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("FavoriteList: %v", err)
	}
	dl := resp
	if dl == nil {
		t.Fatalf("resp is nil")
	}
	if dl.Total != 1 {
		t.Fatalf("total = %d, want 1", dl.Total)
	}
	items, ok := dl.Item.([]*FavoriteListResp)
	if !ok || len(items) != 1 {
		t.Fatalf("item type/len 异常：%T", dl.Item)
	}
	got := items[0]
	if got.ProductID != fx.Product.ID || got.UserID != fx.Buyer.ID {
		t.Fatalf("product/user = %d/%d", got.ProductID, got.UserID)
	}
	if got.Name != "fav-item" || got.Title != "限量款" {
		t.Fatalf("name/title = %q/%q", got.Name, got.Title)
	}
	if got.CategoryID != fx.Category.ID || got.CategoryName != "数码" {
		t.Fatalf("category = %d/%q", got.CategoryID, got.CategoryName)
	}
	if got.BossID != fx.Boss.ID || got.BossName != fx.Boss.UserName || got.BossAvatar != "boss.jpg" {
		t.Fatalf("boss = %d/%q/%q", got.BossID, got.BossName, got.BossAvatar)
	}
	if got.Price != "100" || got.DiscountPrice != "90" || got.Num != 5 || !got.OnSale {
		t.Fatalf("price/discount/num/on_sale = %q/%q/%d/%v",
			got.Price, got.DiscountPrice, got.Num, got.OnSale)
	}
	// local 上传模式下图片地址要拼上 host + port + 目录前缀
	wantImg := "http://127.0.0.1:5001/product/item.jpg"
	if got.ImgPath != wantImg {
		t.Fatalf("img_path = %q, want %q", got.ImgPath, wantImg)
	}
	if got.CreatedAt <= 0 {
		t.Fatalf("created_at 应为正的 unix 秒，got %d", got.CreatedAt)
	}
}

func TestFavoriteDelete_RemovesAndMissingRejected(t *testing.T) {
	initLogForTest()
	restore := initFavoriteTestConfig()
	defer restore()
	db, cleanup := setupSQLiteForFavorite(t)
	defer cleanup()

	fx := seedFavoriteFixture(t, db)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: fx.Buyer.ID})
	if _, err := GetFavoriteSrv().FavoriteCreate(ctx,
		&FavoriteCreateReq{ProductId: fx.Product.ID, BossId: fx.Boss.ID}); err != nil {
		t.Fatalf("FavoriteCreate: %v", err)
	}

	// 越权删除：用户 B 即使在请求体里塞 A 的 user_id，也删不掉 A 的收藏。
	// 删除条件取自登录态 uid，B 名下没有该收藏，直接报错且 A 的行保留。
	intruder := &user.User{UserName: "intruder-" + t.Name(), NickName: "路人"}
	if err := db.Create(intruder).Error; err != nil {
		t.Fatalf("create intruder: %v", err)
	}
	intruderCtx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: intruder.ID})
	if _, err := GetFavoriteSrv().FavoriteDelete(intruderCtx,
		&FavoriteDeleteReq{Id: fx.Buyer.ID, ProductId: fx.Product.ID}); err == nil {
		t.Fatal("跨用户删除应当报错")
	}
	var cnt int64
	db.Model(&Favorite{}).Where("user_id=?", fx.Buyer.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("跨用户删除不应生效，favorite rows = %d, want 1", cnt)
	}

	// 本人删除：按登录态 uid + product_id 定位，请求体里的 user_id 不被采信
	if _, err := GetFavoriteSrv().FavoriteDelete(ctx,
		&FavoriteDeleteReq{ProductId: fx.Product.ID}); err != nil {
		t.Fatalf("FavoriteDelete: %v", err)
	}
	db.Model(&Favorite{}).Where("user_id=?", fx.Buyer.ID).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("favorite rows = %d, want 0", cnt)
	}

	// 再删一次：记录已不存在，应当报错
	if _, err := GetFavoriteSrv().FavoriteDelete(ctx,
		&FavoriteDeleteReq{ProductId: fx.Product.ID}); err == nil {
		t.Fatal("删除不存在的收藏应当报错")
	}
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
