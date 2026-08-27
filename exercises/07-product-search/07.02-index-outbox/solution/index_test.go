//go:build exercise

package indexoutbox

import (
	"errors"
	"reflect"
	"testing"
)

func TestSaveCommitsProductAndEventTogether(t *testing.T) {
	db := NewDB()
	product := Product{ID: 7, Name: "咖啡壶", Version: 1}
	if err := SaveProductAndEvent(db, product, "evt-7-v1"); err != nil {
		t.Fatal(err)
	}
	if db.Products[7] != product {
		t.Fatalf("product=%+v", db.Products[7])
	}
	want := Event{ID: "evt-7-v1", Topic: "product.changed", ProductID: 7, Version: 1}
	if db.Events["evt-7-v1"] != want {
		t.Fatalf("event=%+v want=%+v", db.Events["evt-7-v1"], want)
	}
}

func TestEventIDIsTrimmed(t *testing.T) {
	db := NewDB()
	if err := SaveProductAndEvent(db, Product{ID: 1, Name: "A", Version: 1}, "  evt-1  "); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.Events["evt-1"]; !ok {
		t.Fatalf("events=%+v", db.Events)
	}
}

func TestInvalidProductDoesNotMutateDB(t *testing.T) {
	db := NewDB()
	beforeProducts, beforeEvents := cloneProducts(db.Products), cloneEvents(db.Events)
	err := SaveProductAndEvent(db, Product{Name: "A", Version: 1}, "evt")
	if !errors.Is(err, ErrInvalidProductID) || !reflect.DeepEqual(db.Products, beforeProducts) || !reflect.DeepEqual(db.Events, beforeEvents) {
		t.Fatalf("err=%v products=%+v events=%+v", err, db.Products, db.Events)
	}
}

func TestBlankEventIDDoesNotMutateDB(t *testing.T) {
	db := NewDB()
	err := SaveProductAndEvent(db, Product{ID: 1, Name: "A", Version: 1}, " \t ")
	if !errors.Is(err, ErrEmptyEventID) || len(db.Products) != 0 || len(db.Events) != 0 {
		t.Fatalf("err=%v products=%+v events=%+v", err, db.Products, db.Events)
	}
}

func TestOutboxFailureRollsBackNewProduct(t *testing.T) {
	db := NewDB()
	db.FailOutbox = true
	err := SaveProductAndEvent(db, Product{ID: 2, Name: "B", Version: 1}, "evt-2")
	if !errors.Is(err, ErrOutboxUnavailable) || len(db.Products) != 0 || len(db.Events) != 0 {
		t.Fatalf("err=%v products=%+v events=%+v", err, db.Products, db.Events)
	}
}

func TestOutboxFailurePreservesPreviousVersion(t *testing.T) {
	db := NewDB()
	old := Product{ID: 3, Name: "旧名称", Version: 1}
	db.Products[3] = old
	db.FailOutbox = true
	err := SaveProductAndEvent(db, Product{ID: 3, Name: "新名称", Version: 2}, "evt-3-v2")
	if !errors.Is(err, ErrOutboxUnavailable) || db.Products[3] != old || len(db.Events) != 0 {
		t.Fatalf("err=%v product=%+v events=%+v", err, db.Products[3], db.Events)
	}
}

func TestExactEventReplayIsIdempotent(t *testing.T) {
	db := NewDB()
	product := Product{ID: 4, Name: "商品", Version: 1}
	if err := SaveProductAndEvent(db, product, "evt-4"); err != nil {
		t.Fatal(err)
	}
	db.FailOutbox = true
	if err := SaveProductAndEvent(db, product, "evt-4"); err != nil {
		t.Fatalf("replay err=%v", err)
	}
	if len(db.Products) != 1 || len(db.Events) != 1 {
		t.Fatalf("products=%+v events=%+v", db.Products, db.Events)
	}
}

func TestEventIDCannotReferToAnotherProduct(t *testing.T) {
	db := NewDB()
	if err := SaveProductAndEvent(db, Product{ID: 5, Name: "A", Version: 1}, "shared"); err != nil {
		t.Fatal(err)
	}
	err := SaveProductAndEvent(db, Product{ID: 6, Name: "B", Version: 1}, "shared")
	if !errors.Is(err, ErrEventConflict) || len(db.Products) != 1 {
		t.Fatalf("err=%v products=%+v", err, db.Products)
	}
}

func TestEventIDCannotReferToAnotherVersion(t *testing.T) {
	db := NewDB()
	if err := SaveProductAndEvent(db, Product{ID: 7, Name: "A", Version: 1}, "shared"); err != nil {
		t.Fatal(err)
	}
	err := SaveProductAndEvent(db, Product{ID: 7, Name: "B", Version: 2}, "shared")
	if !errors.Is(err, ErrEventConflict) || db.Products[7].Version != 1 {
		t.Fatalf("err=%v product=%+v", err, db.Products[7])
	}
}

func TestStaleVersionIsRejected(t *testing.T) {
	db := NewDB()
	db.Products[8] = Product{ID: 8, Name: "新", Version: 3}
	err := SaveProductAndEvent(db, Product{ID: 8, Name: "旧", Version: 2}, "evt-stale")
	if !errors.Is(err, ErrStaleVersion) || db.Products[8].Version != 3 || len(db.Events) != 0 {
		t.Fatalf("err=%v product=%+v events=%+v", err, db.Products[8], db.Events)
	}
}

func TestSameVersionWithDifferentContentIsRejected(t *testing.T) {
	db := NewDB()
	db.Products[9] = Product{ID: 9, Name: "原名称", Version: 4}
	err := SaveProductAndEvent(db, Product{ID: 9, Name: "冲突名称", Version: 4}, "evt-conflict")
	if !errors.Is(err, ErrVersionConflict) || db.Products[9].Name != "原名称" || len(db.Events) != 0 {
		t.Fatalf("err=%v product=%+v events=%+v", err, db.Products[9], db.Events)
	}
}

func TestSameProductVersionCanRecordAnotherEvent(t *testing.T) {
	db := NewDB()
	product := Product{ID: 10, Name: "商品", Version: 5}
	db.Products[10] = product
	if err := SaveProductAndEvent(db, product, "evt-repair"); err != nil {
		t.Fatal(err)
	}
	if db.Events["evt-repair"].Version != 5 || db.Products[10] != product {
		t.Fatalf("product=%+v events=%+v", db.Products[10], db.Events)
	}
}

func TestHigherVersionReplacesProductAndRecordsEvent(t *testing.T) {
	db := NewDB()
	db.Products[11] = Product{ID: 11, Name: "旧", Version: 1}
	newProduct := Product{ID: 11, Name: "新", Version: 2}
	if err := SaveProductAndEvent(db, newProduct, "evt-11-v2"); err != nil {
		t.Fatal(err)
	}
	if db.Products[11] != newProduct || db.Events["evt-11-v2"].Version != 2 {
		t.Fatalf("product=%+v events=%+v", db.Products[11], db.Events)
	}
}
