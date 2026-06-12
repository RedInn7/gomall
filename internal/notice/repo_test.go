package notice

import (
	"context"
	"errors"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/repository/db/dao"
)

// 公告领域只有 model + repo，按 dao 行为做闭环验证。
// sqlite 不可用（CGO 关闭）时整组 skip。

func setupSQLiteForNotice(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:notice-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&Notice{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

// TestNoticeDao_CreateAndGetById 覆盖创建 + 按 id 读取的闭环，
// 同时验证 NewNoticeDao(ctx) 经全局连接拿到的是同一份数据。
func TestNoticeDao_CreateAndGetById(t *testing.T) {
	db, cleanup := setupSQLiteForNotice(t)
	defer cleanup()

	d := NewNoticeDaoByDB(db)
	n := &Notice{Text: "系统将于今晚 23:00-24:00 升级，期间支付功能暂停"}
	if err := d.CreateNotice(n); err != nil {
		t.Fatalf("CreateNotice: %v", err)
	}
	if n.ID == 0 {
		t.Fatal("CreateNotice 后主键应回填")
	}

	got, err := d.GetNoticeById(n.ID)
	if err != nil {
		t.Fatalf("GetNoticeById: %v", err)
	}
	if got.Text != n.Text {
		t.Fatalf("text = %q, want %q", got.Text, n.Text)
	}

	// ctx 路径：NewNoticeDao 经 dao.NewDBClient 走 SetTestDB 注入的连接
	got2, err := NewNoticeDao(context.Background()).GetNoticeById(n.ID)
	if err != nil {
		t.Fatalf("GetNoticeById via ctx dao: %v", err)
	}
	if got2.ID != n.ID || got2.Text != n.Text {
		t.Fatalf("ctx dao 读取不一致: %+v", got2)
	}
}

// TestNoticeDao_GetMissingIdReturnsNotFound 验证不存在的 id 返回 gorm.ErrRecordNotFound。
func TestNoticeDao_GetMissingIdReturnsNotFound(t *testing.T) {
	db, cleanup := setupSQLiteForNotice(t)
	defer cleanup()

	_, err := NewNoticeDaoByDB(db).GetNoticeById(404)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expect ErrRecordNotFound, got %v", err)
	}
}
