package product

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/types"
)

// ProductDao 的 sqlite 闭环：建 / 查 / 搜（LIKE + 分页）/ 改 / 删（boss 维度隔离）。
// 不触达 Redis 与 outbox，纯 DB 行为。

func setupSQLiteForProduct(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:product-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&Product{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func mustCreateProduct(t *testing.T, d *ProductDao, p *Product) *Product {
	t.Helper()
	if err := d.CreateProduct(p); err != nil {
		t.Fatalf("create product %q: %v", p.Name, err)
	}
	return p
}

func TestProductDao_CreateAndGetById(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForProduct(t)
	defer cleanup()

	d := NewProductDaoByDB(db)
	created := mustCreateProduct(t, d, &Product{
		Name:          "iphone-15",
		CategoryID:    2,
		Title:         "全新未拆封",
		Info:          "国行 256G",
		ImgPath:       "iphone.png",
		Price:         "6999",
		DiscountPrice: "6599",
		OnSale:        true,
		Num:           5,
		BossID:        7,
		BossName:      "seller-a",
	})
	if created.ID == 0 {
		t.Fatal("create 后应回填主键")
	}

	got, err := d.GetProductById(created.ID)
	if err != nil {
		t.Fatalf("GetProductById: %v", err)
	}
	if got.Name != "iphone-15" || got.CategoryID != 2 || got.Price != "6999" ||
		got.DiscountPrice != "6599" || !got.OnSale || got.Num != 5 || got.BossID != 7 {
		t.Fatalf("字段回读不一致: %+v", got)
	}

	if _, err := d.GetProductById(created.ID + 100); err == nil {
		t.Fatal("查不存在的商品应返回 record not found")
	}
}

func TestProductDao_SearchProductLikeAndPagination(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForProduct(t)
	defer cleanup()

	d := NewProductDaoByDB(db)
	// 3 条命中（2 条名字含关键词 + 1 条 info 含关键词），1 条不命中
	mustCreateProduct(t, d, &Product{Name: "switch 主机", CategoryID: 1, Info: "港版", BossID: 1})
	mustCreateProduct(t, d, &Product{Name: "switch pro 手柄", CategoryID: 1, Info: "原装", BossID: 1})
	mustCreateProduct(t, d, &Product{Name: "游戏卡带", CategoryID: 1, Info: "switch 塞尔达", BossID: 1})
	mustCreateProduct(t, d, &Product{Name: "ps5 手柄", CategoryID: 1, Info: "白色", BossID: 1})

	// 第一页 2 条，count 给全量命中数
	page1, count, err := d.SearchProduct("switch", types.BasePage{PageNum: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("SearchProduct: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}

	// 第二页收尾 1 条，且与第一页无重叠
	page2, count2, err := d.SearchProduct("switch", types.BasePage{PageNum: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("SearchProduct page2: %v", err)
	}
	if count2 != 3 || len(page2) != 1 {
		t.Fatalf("page2 len = %d count = %d, want 1/3", len(page2), count2)
	}
	seen := map[uint]bool{}
	for _, p := range append(page1, page2...) {
		if seen[p.ID] {
			t.Fatalf("分页结果重复 id=%d", p.ID)
		}
		seen[p.ID] = true
	}

	// 无命中关键词
	none, count3, err := d.SearchProduct("不存在的商品", types.BasePage{PageNum: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("SearchProduct miss: %v", err)
	}
	if len(none) != 0 || count3 != 0 {
		t.Fatalf("miss 查询应为空, got len=%d count=%d", len(none), count3)
	}
}

func TestProductDao_ListAndCountByCondition(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForProduct(t)
	defer cleanup()

	d := NewProductDaoByDB(db)
	for i := 0; i < 3; i++ {
		mustCreateProduct(t, d, &Product{Name: "cat7-item", CategoryID: 7, BossID: 1})
	}
	mustCreateProduct(t, d, &Product{Name: "cat8-item", CategoryID: 8, BossID: 1})

	cond := map[string]interface{}{"category_id": 7}
	items, err := d.ListProductByCondition(cond, types.BasePage{PageNum: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("ListProductByCondition: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("page len = %d, want 2", len(items))
	}
	total, err := d.CountProductByCondition(cond)
	if err != nil {
		t.Fatalf("CountProductByCondition: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
}

func TestProductDao_UpdateProduct(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForProduct(t)
	defer cleanup()

	d := NewProductDaoByDB(db)
	p := mustCreateProduct(t, d, &Product{
		Name: "old-name", CategoryID: 2, Title: "old-title",
		Price: "100", DiscountPrice: "90", Num: 3, BossID: 5, OnSale: true,
	})

	err := d.UpdateProduct(p.ID, &Product{
		Name: "new-name", Title: "new-title", Price: "120", DiscountPrice: "110",
	})
	if err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}

	got, err := d.GetProductById(p.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Name != "new-name" || got.Title != "new-title" ||
		got.Price != "120" || got.DiscountPrice != "110" {
		t.Fatalf("更新字段未生效: %+v", got)
	}
	// Updates 走 struct 非零值语义：未传字段（Num/BossID/CategoryID）保持原值
	if got.Num != 3 || got.BossID != 5 || got.CategoryID != 2 {
		t.Fatalf("未更新字段被改动: %+v", got)
	}
}

func TestProductDao_DeleteProductScopedToBoss(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForProduct(t)
	defer cleanup()

	d := NewProductDaoByDB(db)
	p := mustCreateProduct(t, d, &Product{Name: "to-delete", CategoryID: 1, BossID: 9})

	// 非本人（boss_id 不匹配）删除：行保留
	if err := d.DeleteProduct(p.ID, 8); err != nil {
		t.Fatalf("DeleteProduct (wrong boss): %v", err)
	}
	if _, err := d.GetProductById(p.ID); err != nil {
		t.Fatalf("boss 不匹配不应删除: %v", err)
	}

	// 本人删除：行不可再读
	if err := d.DeleteProduct(p.ID, 9); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}
	if _, err := d.GetProductById(p.ID); err == nil {
		t.Fatal("删除后仍可读取")
	}
}

func TestProductDao_RollbackStock(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForProduct(t)
	defer cleanup()

	d := NewProductDaoByDB(db)
	p := mustCreateProduct(t, d, &Product{Name: "stock-item", CategoryID: 1, Num: 4, BossID: 1})

	ok, err := d.RollbackStock(p.ID, 2)
	if err != nil || !ok {
		t.Fatalf("RollbackStock: ok=%v err=%v", ok, err)
	}
	got, err := d.GetProductById(p.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Num != 6 {
		t.Fatalf("num = %d, want 6", got.Num)
	}

	// 商品不存在：RowsAffected=0 必须显式报错，避免静默丢回滚
	if ok, err := d.RollbackStock(p.ID+100, 1); err == nil || ok {
		t.Fatalf("不存在的商品回滚应报错, got ok=%v err=%v", ok, err)
	}
}
