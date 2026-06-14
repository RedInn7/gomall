package cart

import (
	"testing"

	"github.com/RedInn7/gomall/pkg/e"
)

// TestCreateCart_NonRecordNotFoundDBError 覆盖瞬时 DB 错误（非 RecordNotFound）下的
// 加购路径：GetCartById 未填充 cart 仍为 nil，CreateCart 必须在访问其字段前返回错误，
// 而不是对 nil 指针解引用 panic。
func TestCreateCart_NonRecordNotFoundDBError(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForCart(t)
	defer cleanup()

	// 关闭底层连接，制造一次非 ErrRecordNotFound 的查询错误（database is closed）
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}

	dao := &CartDao{db}
	cart, status, err := dao.CreateCart(1, 1, 1)
	if err == nil {
		t.Fatal("底层 DB 错误时应回传 err")
	}
	if status != e.ERROR {
		t.Fatalf("status = %d, want %d", status, e.ERROR)
	}
	if cart != nil {
		t.Fatalf("出错时不应回传 cart，got %+v", cart)
	}
}
