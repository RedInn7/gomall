package product

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/RedInn7/gomall/internal/shared/outbox"
	"github.com/RedInn7/gomall/service/events"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCreateProductAndEventCommitsProductImagesAndEvent(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := testProduct()

	if err := createProductAndEvent(db, p, "cover.jpg", "detail.jpg"); err != nil {
		t.Fatalf("createProductAndEvent() error = %v", err)
	}

	var imageCount int64
	if err := db.Model(&ProductImg{}).Where("product_id = ?", p.ID).Count(&imageCount).Error; err != nil {
		t.Fatalf("count product images: %v", err)
	}
	if imageCount != 2 {
		t.Fatalf("image count = %d, want 2", imageCount)
	}
	assertProductChangedEvent(t, db, p.ID, "create")
}

func TestCreateProductAndEventRollsBackWhenOutboxInsertFails(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	injectProductOutboxFailure(t, db)
	p := testProduct()

	err := createProductAndEvent(db, p, "cover.jpg")
	if err == nil {
		t.Fatal("createProductAndEvent() error = nil, want injected outbox error")
	}

	assertTableCount(t, db.Unscoped().Model(&Product{}), 0)
	assertTableCount(t, db.Unscoped().Model(&ProductImg{}), 0)
}

func TestCreateProductAndEventRollsBackWhenImageInsertFails(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	if err := db.Exec(`CREATE TRIGGER fail_product_image BEFORE INSERT ON product_imgs BEGIN SELECT RAISE(ABORT, 'injected image failure'); END;`).Error; err != nil {
		t.Fatalf("create image failure trigger: %v", err)
	}

	err := createProductAndEvent(db, testProduct(), "cover.jpg")
	if err == nil {
		t.Fatal("createProductAndEvent() error = nil, want injected image error")
	}
	assertTableCount(t, db.Unscoped().Model(&Product{}), 0)
	assertTableCount(t, db.Model(&outbox.OutboxEvent{}), 0)

}

func TestUpdateProductAndEventCommitsTogether(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := seedProduct(t, db)
	updated := testProduct()
	updated.Name = "升级款咖啡壶"
	updated.OnSale = false

	if err := updateProductAndEvent(db, p.ID, p.BossID, updated); err != nil {
		t.Fatalf("updateProductAndEvent() error = %v", err)
	}
	got := loadProduct(t, db, p.ID)
	if got.Name != "升级款咖啡壶" || got.OnSale {
		t.Fatalf("updated product = %+v", got)
	}
	assertProductChangedEvent(t, db, p.ID, "update")
}

func TestUpdateProductAndEventRollsBackWhenOutboxInsertFails(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := seedProduct(t, db)
	injectProductOutboxFailure(t, db)
	updated := testProduct()
	updated.Name = "不应提交的新名称"

	err := updateProductAndEvent(db, p.ID, p.BossID, updated)
	if err == nil {
		t.Fatal("updateProductAndEvent() error = nil, want injected outbox error")
	}
	if got := loadProduct(t, db, p.ID); got.Name != p.Name {
		t.Fatalf("product name = %q, want rollback to %q", got.Name, p.Name)
	}
}

func TestUpdateProductAndEventRejectsWrongSellerWithoutEvent(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := seedProduct(t, db)

	err := updateProductAndEvent(db, p.ID, p.BossID+1, testProduct())
	if !errors.Is(err, ErrProductNotFoundOrForbidden) {
		t.Fatalf("error = %v, want %v", err, ErrProductNotFoundOrForbidden)
	}
	assertTableCount(t, db.Model(&outbox.OutboxEvent{}), 0)
}

func TestDeleteProductAndEventCommitsTogether(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := seedProduct(t, db)

	if err := deleteProductAndEvent(db, p.ID, p.BossID); err != nil {
		t.Fatalf("deleteProductAndEvent() error = %v", err)
	}
	var got Product
	if err := db.First(&got, p.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("load deleted product error = %v, want record not found", err)
	}
	assertProductChangedEvent(t, db, p.ID, "delete")
}

func TestDeleteProductAndEventRollsBackWhenOutboxInsertFails(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := seedProduct(t, db)
	injectProductOutboxFailure(t, db)

	err := deleteProductAndEvent(db, p.ID, p.BossID)
	if err == nil {
		t.Fatal("deleteProductAndEvent() error = nil, want injected outbox error")
	}
	_ = loadProduct(t, db, p.ID)
}

func TestDeleteProductAndEventRejectsWrongSellerWithoutEvent(t *testing.T) {
	db := openProductOutboxTestDB(t, true)
	p := seedProduct(t, db)

	err := deleteProductAndEvent(db, p.ID, p.BossID+1)
	if !errors.Is(err, ErrProductNotFoundOrForbidden) {
		t.Fatalf("error = %v, want %v", err, ErrProductNotFoundOrForbidden)
	}
	_ = loadProduct(t, db, p.ID)
	assertTableCount(t, db.Model(&outbox.OutboxEvent{}), 0)
}

func openProductOutboxTestDB(t *testing.T, migrateOutbox bool) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:product-outbox-%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	models := []any{&Product{}, &ProductImg{}}
	if migrateOutbox {
		models = append(models, &outbox.OutboxEvent{})
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate product outbox schema: %v", err)
	}
	return db
}

func injectProductOutboxFailure(t *testing.T, db *gorm.DB) {
	t.Helper()
	// 用数据库触发器模拟真实 INSERT 失败，不给生产代码增加测试专用开关。
	statement := `CREATE TRIGGER fail_product_outbox BEFORE INSERT ON outbox_event
		WHEN NEW.routing_key = 'product.changed'
		BEGIN SELECT RAISE(ABORT, 'injected outbox failure'); END;`
	if err := db.Exec(statement).Error; err != nil {
		t.Fatalf("create outbox failure trigger: %v", err)
	}
}

func seedProduct(t *testing.T, db *gorm.DB) *Product {
	t.Helper()
	p := testProduct()
	if err := db.Create(p).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	return p
}

func loadProduct(t *testing.T, db *gorm.DB, id uint) *Product {
	t.Helper()
	var p Product
	if err := db.First(&p, id).Error; err != nil {
		t.Fatalf("load product %d: %v", id, err)
	}
	return &p
}

func assertProductChangedEvent(t *testing.T, db *gorm.DB, productID uint, op string) {
	t.Helper()
	var row outbox.OutboxEvent
	if err := db.Where("aggregate_type = ? AND aggregate_id = ?", "product", productID).First(&row).Error; err != nil {
		t.Fatalf("load product.changed event: %v", err)
	}
	if row.EventType != "ProductChanged" || row.RoutingKey != "product.changed" || row.Status != outbox.OutboxStatusPending {
		t.Fatalf("outbox event = %+v", row)
	}
	var payload events.ProductChanged
	if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.ProductID != productID || payload.Op != op {
		t.Fatalf("payload = %+v, want product=%d op=%s", payload, productID, op)
	}
}

func assertTableCount(t *testing.T, query *gorm.DB, want int64) {
	t.Helper()
	var count int64
	if err := query.Count(&count).Error; err != nil {
		t.Fatalf("count table: %v", err)
	}
	if count != want {
		t.Fatalf("count = %d, want %d", count, want)
	}
}

func testProduct() *Product {
	return &Product{
		Name:       "露营咖啡壶",
		CategoryID: 7,
		Title:      "户外手冲",
		Info:       "适合露营",
		Price:      "12900",
		Num:        10,
		OnSale:     true,
		BossID:     22,
	}
}
