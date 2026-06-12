package category

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/types"
)

// 商品分类领域的白盒测试：sqlite in-memory 直连 dao 层。
// sqlite 不可用（CGO 关闭）时整组 skip。

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func setupSQLiteForCategory(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:category-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&Category{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

// TestCategory_ListMapsDTO 覆盖列表闭环：落库 -> 接口返回 DataListResp，
// ID / CategoryName / CreatedAt(Unix 秒) 全字段映射。
func TestCategory_ListMapsDTO(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCategory(t)
	defer cleanup()

	base := time.Date(2026, 3, 1, 9, 30, 0, 0, time.Local)
	seeds := []*Category{
		{CategoryName: "数码家电"},
		{CategoryName: "美妆个护"},
	}
	for i, s := range seeds {
		s.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		if err := db.Create(s).Error; err != nil {
			t.Fatalf("seed category: %v", err)
		}
	}

	resp, err := GetCategorySrv().CategoryList(context.Background(), &ListCategoryReq{})
	if err != nil {
		t.Fatalf("CategoryList: %v", err)
	}
	lr, ok := resp.(*types.DataListResp)
	if !ok {
		t.Fatalf("resp type = %T", resp)
	}
	if lr.Total != 2 {
		t.Fatalf("total = %d, want 2", lr.Total)
	}
	items, ok := lr.Item.([]*ListCategoryResp)
	if !ok {
		t.Fatalf("item type = %T", lr.Item)
	}
	byID := map[uint]*ListCategoryResp{}
	for _, it := range items {
		byID[it.ID] = it
	}
	for _, s := range seeds {
		got, ok := byID[s.ID]
		if !ok {
			t.Fatalf("分类 %d 缺失", s.ID)
		}
		if got.CategoryName != s.CategoryName {
			t.Fatalf("name = %q, want %q", got.CategoryName, s.CategoryName)
		}
		if got.CreatedAt != s.CreatedAt.Unix() {
			t.Fatalf("created_at = %d, want %d", got.CreatedAt, s.CreatedAt.Unix())
		}
	}
}

// TestCategory_ListEmptyTable 空表时返回空列表，Total 为 0。
func TestCategory_ListEmptyTable(t *testing.T) {
	initLogForTest()
	_, cleanup := setupSQLiteForCategory(t)
	defer cleanup()

	resp, err := GetCategorySrv().CategoryList(context.Background(), &ListCategoryReq{})
	if err != nil {
		t.Fatalf("CategoryList: %v", err)
	}
	lr := resp.(*types.DataListResp)
	if lr.Total != 0 {
		t.Fatalf("total = %d, want 0", lr.Total)
	}
	if items := lr.Item.([]*ListCategoryResp); len(items) != 0 {
		t.Fatalf("空表应返回空切片，len = %d", len(items))
	}
}
