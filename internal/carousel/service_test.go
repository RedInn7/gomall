package carousel

import (
	"context"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/types"
)

// 轮播图领域的白盒测试：sqlite in-memory 直连 dao 层。
// sqlite 不可用（CGO 关闭）时整组 skip。

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func setupSQLiteForCarousel(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:carousel-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(newTestDialector(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&Carousel{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

// TestCarousel_ListReturnsSeededRows 覆盖列表闭环：落库两条，
// 接口返回 DataListResp 且逐字段映射正确。CreatedAt 依赖 SELECT 列
// 别名映射，当前查询表达式未带 AS 别名，该字段不参与断言（详见缺陷记录）。
func TestCarousel_ListReturnsSeededRows(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCarousel(t)
	defer cleanup()

	seeds := []*Carousel{
		{ImgPath: "https://cdn.example.com/banner/618-main.png", ProductID: 11},
		{ImgPath: "https://cdn.example.com/banner/new-arrival.png", ProductID: 22},
	}
	for _, s := range seeds {
		if err := db.Create(s).Error; err != nil {
			t.Fatalf("seed carousel: %v", err)
		}
	}

	resp, err := GetCarouselSrv().ListCarousel(context.Background(), &ListCarouselReq{})
	if err != nil {
		t.Fatalf("ListCarousel: %v", err)
	}
	lr, ok := resp.(*types.DataListResp)
	if !ok {
		t.Fatalf("resp type = %T", resp)
	}
	if lr.Total != 2 {
		t.Fatalf("total = %d, want 2", lr.Total)
	}
	items, ok := lr.Item.([]*ListCarouselResp)
	if !ok {
		t.Fatalf("item type = %T", lr.Item)
	}
	byProduct := map[uint]*ListCarouselResp{}
	for _, it := range items {
		byProduct[it.ProductID] = it
	}
	for _, s := range seeds {
		got, ok := byProduct[s.ProductID]
		if !ok {
			t.Fatalf("product %d 的轮播图缺失", s.ProductID)
		}
		if got.ID != s.ID || got.ImgPath != s.ImgPath {
			t.Fatalf("DTO 映射不一致: got %+v, seed %+v", got, s)
		}
	}
	t.Logf("CreatedAt 映射观测值（别名缺失时为 0）：%d / %d", items[0].CreatedAt, items[1].CreatedAt)
}

// TestCarousel_ListEmptyTable 空表时 Total 为 0，不报错。
func TestCarousel_ListEmptyTable(t *testing.T) {
	initLogForTest()
	_, cleanup := setupSQLiteForCarousel(t)
	defer cleanup()

	resp, err := GetCarouselSrv().ListCarousel(context.Background(), &ListCarouselReq{})
	if err != nil {
		t.Fatalf("ListCarousel: %v", err)
	}
	if lr := resp.(*types.DataListResp); lr.Total != 0 {
		t.Fatalf("total = %d, want 0", lr.Total)
	}
}
