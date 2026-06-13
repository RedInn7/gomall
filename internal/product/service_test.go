package product

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
	"github.com/RedInn7/gomall/types"
)

// 服务层只覆盖 DB 主路径：loadProductFromDB 的 DTO 组装、ProductList 分页、
// ProductShow 的 Cache Aside 回源 + 缓存命中。
// Product.View() 读 Redis 点击数，因此整组依赖 Redis（DB 15），不可用时 skip。
// ProductCreate（multipart 上传）/ ProductUpdate / ProductDelete 的服务层入口
// 牵涉文件系统与延迟双删后台任务，DB 行为已在 repo_test.go 覆盖。

var productTestConfigOnce sync.Once

// initProductTestConfig 保证 conf.Config.System 可用，并固定 UploadModel 为 oss 口径：
// 本地口径会在 DTO 上拼接图片/头像 host 前缀，测试聚焦 DB→DTO 字段映射本身。
func initProductTestConfig() {
	productTestConfigOnce.Do(func() {
		re := conf.ConfigReader{FileName: "../../config/locales/config.yaml"}
		defer func() {
			if r := recover(); r != nil {
				conf.Config = &conf.Conf{}
			}
			if conf.Config.System == nil {
				conf.Config.System = &conf.System{}
			}
			conf.Config.System.UploadModel = consts.UploadModelOss
		}()
		conf.InitConfigForTest(&re)
	})
}

func setupRedisForProduct(t *testing.T) func() {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379", DB: 15})
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

func TestProductService_LoadProductFromDBMapsDTO(t *testing.T) {
	initLogForTest()
	initProductTestConfig()
	rcleanup := setupRedisForProduct(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForProduct(t)
	defer dcleanup()

	d := NewProductDaoByDB(db)
	p := mustCreateProduct(t, d, &Product{
		Name:          "macbook-air",
		CategoryID:    4,
		Title:         "M3 16G",
		Info:          "95 新",
		ImgPath:       "mba.png",
		Price:         "7500",
		DiscountPrice: "7200",
		OnSale:        true,
		Num:           2,
		BossID:        11,
		BossName:      "seller-b",
		BossAvatar:    "avatar.png",
	})

	resp, err := GetProductSrv().loadProductFromDB(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("loadProductFromDB: %v", err)
	}
	if resp.ID != p.ID || resp.Name != "macbook-air" || resp.CategoryID != 4 ||
		resp.Title != "M3 16G" || resp.Info != "95 新" || resp.ImgPath != "mba.png" ||
		resp.Price != "7500" || resp.DiscountPrice != "7200" ||
		resp.Num != 2 || !resp.OnSale ||
		resp.BossID != 11 || resp.BossName != "seller-b" || resp.BossAvatar != "avatar.png" {
		t.Fatalf("DTO 映射不一致: %+v", resp)
	}
	if resp.CreatedAt != p.CreatedAt.Unix() {
		t.Fatalf("CreatedAt = %d, want %d", resp.CreatedAt, p.CreatedAt.Unix())
	}
	// 点击数走 Redis，新商品无计数应回 0
	if resp.View != 0 {
		t.Fatalf("view = %d, want 0", resp.View)
	}

	if _, err := GetProductSrv().loadProductFromDB(context.Background(), p.ID+100); err == nil {
		t.Fatal("不存在的商品应回源失败")
	}
}

func TestProductService_ProductListPagination(t *testing.T) {
	initLogForTest()
	initProductTestConfig()
	rcleanup := setupRedisForProduct(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForProduct(t)
	defer dcleanup()

	d := NewProductDaoByDB(db)
	for i := 0; i < 3; i++ {
		mustCreateProduct(t, d, &Product{Name: "list-item", CategoryID: 7, Num: 1, BossID: 1})
	}
	mustCreateProduct(t, d, &Product{Name: "other-item", CategoryID: 8, Num: 1, BossID: 1})

	// 第一页：2 条，total 给类目命中全量
	resp, err := GetProductSrv().ProductList(context.Background(), &ProductListReq{
		CategoryID: 7,
		BasePage:   types.BasePage{PageNum: 1, PageSize: 2},
	})
	if err != nil {
		t.Fatalf("ProductList: %v", err)
	}
	list := resp
	if list.Total != 3 {
		t.Fatalf("total = %d, want 3", list.Total)
	}
	items, ok := list.Item.([]*ProductResp)
	if !ok {
		t.Fatalf("item type %T", list.Item)
	}
	if len(items) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(items))
	}
	for _, it := range items {
		if it.CategoryID != 7 || it.Name != "list-item" {
			t.Fatalf("类目过滤失效: %+v", it)
		}
	}

	// 第二页收尾 1 条
	resp, err = GetProductSrv().ProductList(context.Background(), &ProductListReq{
		CategoryID: 7,
		BasePage:   types.BasePage{PageNum: 2, PageSize: 2},
	})
	if err != nil {
		t.Fatalf("ProductList page2: %v", err)
	}
	items = resp.Item.([]*ProductResp)
	if len(items) != 1 {
		t.Fatalf("page2 len = %d, want 1", len(items))
	}

	// 不带类目条件：全量 4 条
	resp, err = GetProductSrv().ProductList(context.Background(), &ProductListReq{
		BasePage: types.BasePage{PageNum: 1, PageSize: 10},
	})
	if err != nil {
		t.Fatalf("ProductList all: %v", err)
	}
	if total := resp.Total; total != 4 {
		t.Fatalf("total = %d, want 4", total)
	}
}

func TestProductService_ProductShowFillsAndServesCache(t *testing.T) {
	initLogForTest()
	initProductTestConfig()
	rcleanup := setupRedisForProduct(t)
	defer rcleanup()
	db, dcleanup := setupSQLiteForProduct(t)
	defer dcleanup()

	d := NewProductDaoByDB(db)
	p := mustCreateProduct(t, d, &Product{
		Name: "show-item", CategoryID: 6, Price: "300", Num: 9, BossID: 13, OnSale: true,
	})

	ctx := context.Background()
	// 首次：缓存 miss，回源 DB 并写缓存
	resp, err := GetProductSrv().ProductShow(ctx, &ProductShowReq{ID: p.ID})
	if err != nil {
		t.Fatalf("ProductShow: %v", err)
	}
	first := resp
	if first.ID != p.ID || first.Name != "show-item" || first.Num != 9 {
		t.Fatalf("回源结果不一致: %+v", first)
	}

	cached := &ProductResp{}
	if err := cache.GetProductDetail(ctx, p.ID, cached); err != nil {
		t.Fatalf("回源后缓存应已写入: %v", err)
	}
	if cached.Name != "show-item" {
		t.Fatalf("缓存内容不一致: %+v", cached)
	}

	// 改库不删缓存，二次读仍出旧值 → 证明确实命中缓存而非每次回源
	if err := db.Model(&Product{}).Where("id=?", p.ID).Update("name", "renamed").Error; err != nil {
		t.Fatalf("update name: %v", err)
	}
	resp, err = GetProductSrv().ProductShow(ctx, &ProductShowReq{ID: p.ID})
	if err != nil {
		t.Fatalf("ProductShow second: %v", err)
	}
	if got := resp.Name; got != "show-item" {
		t.Fatalf("第二次读应命中缓存旧值 show-item, got %q", got)
	}

	// 删缓存后再读：回源拿到新值
	if err := cache.DelProductDetail(ctx, p.ID); err != nil {
		t.Fatalf("del cache: %v", err)
	}
	resp, err = GetProductSrv().ProductShow(ctx, &ProductShowReq{ID: p.ID})
	if err != nil {
		t.Fatalf("ProductShow third: %v", err)
	}
	if got := resp.Name; got != "renamed" {
		t.Fatalf("删缓存后应回源新值 renamed, got %q", got)
	}
}

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}
