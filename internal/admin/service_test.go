package admin

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	logpkg "github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// 管理域白盒测试：sqlite in-memory，覆盖 bootstrap 提权、定向提权、用户列表分页。
// sqlite 不可用（CGO 关闭）时整组 skip。

func initLogForTest() {
	if logpkg.LogrusObj == nil {
		l := logrus.New()
		l.Out = io.Discard
		logpkg.LogrusObj = l
	}
}

func setupSQLiteForAdmin(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	dsn := "file:admin-" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		t.Skipf("sqlite 不可用（CGO 关闭？）：%v", err)
	}
	if err := db.AutoMigrate(&user.User{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	prev := dao.SetTestDB(db)
	return db, func() { dao.SetTestDB(prev) }
}

func mustCreateUser(t *testing.T, db *gorm.DB, name, role string) *user.User {
	t.Helper()
	u := &user.User{UserName: name, Role: role, Status: user.Active}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return u
}

// TestAdmin_BootstrapPromoteSelf 验证初始化提权的两段语义：
// 系统无 admin 时首个用户可自提；一旦存在 admin，接口立即关闭。
func TestAdmin_BootstrapPromoteSelf(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForAdmin(t)
	defer cleanup()
	srv := GetAdminSrv()

	u1 := mustCreateUser(t, db, "first-operator", user.RoleUser)
	u2 := mustCreateUser(t, db, "second-operator", user.RoleUser)

	// 未登录上下文直接拒绝
	if err := srv.BootstrapPromoteSelf(context.Background()); err == nil {
		t.Fatal("无用户信息的 ctx 应报错")
	}

	// 无 admin -> 首个用户自提成功
	ctx1 := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: u1.ID})
	if err := srv.BootstrapPromoteSelf(ctx1); err != nil {
		t.Fatalf("BootstrapPromoteSelf: %v", err)
	}
	var got user.User
	if err := db.First(&got, u1.ID).Error; err != nil {
		t.Fatalf("reload u1: %v", err)
	}
	if got.Role != user.RoleAdmin {
		t.Fatalf("u1 role = %q, want %q", got.Role, user.RoleAdmin)
	}

	// 已有 admin -> 第二个用户被拒，角色不变
	ctx2 := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: u2.ID})
	if err := srv.BootstrapPromoteSelf(ctx2); err == nil {
		t.Fatal("已存在 admin 时 bootstrap 应被拒绝")
	}
	var got2 user.User
	if err := db.First(&got2, u2.ID).Error; err != nil {
		t.Fatalf("reload u2: %v", err)
	}
	if got2.Role != user.RoleUser {
		t.Fatalf("u2 role = %q, 应保持 %q", got2.Role, user.RoleUser)
	}
}

// TestAdmin_PromoteToAdmin 验证定向提权：普通用户 -> admin；目标不存在则报错。
func TestAdmin_PromoteToAdmin(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForAdmin(t)
	defer cleanup()
	srv := GetAdminSrv()

	target := mustCreateUser(t, db, "promo-target", user.RoleUser)
	if err := srv.PromoteToAdmin(context.Background(), target.ID); err != nil {
		t.Fatalf("PromoteToAdmin: %v", err)
	}
	var got user.User
	if err := db.First(&got, target.ID).Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if got.Role != user.RoleAdmin {
		t.Fatalf("role = %q, want %q", got.Role, user.RoleAdmin)
	}

	// 目标不存在
	err := srv.PromoteToAdmin(context.Background(), 99999)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("提权不存在的用户应返回 not found，got %v", err)
	}
}

// TestAdmin_ListAllUsers 验证分页、id 倒序，以及敏感字段不出库（密码摘要不在 SELECT 列）。
func TestAdmin_ListAllUsers(t *testing.T) {
	initLogForTest()
	db, cleanup := setupSQLiteForAdmin(t)
	defer cleanup()
	srv := GetAdminSrv()

	names := []string{"alice", "bob", "carol"}
	for _, n := range names {
		u := mustCreateUser(t, db, n, user.RoleUser)
		u.PasswordDigest = "bcrypt-digest-placeholder"
		if err := db.Save(u).Error; err != nil {
			t.Fatalf("save digest: %v", err)
		}
	}

	// 第一页：2 条，id 倒序
	resp, err := srv.ListAllUsers(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("ListAllUsers: %v", err)
	}
	page1 := resp.([]*user.User)
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if page1[0].ID <= page1[1].ID {
		t.Fatalf("应按 id 倒序：%d, %d", page1[0].ID, page1[1].ID)
	}
	for _, u := range page1 {
		if u.PasswordDigest != "" {
			t.Fatalf("密码摘要不应出现在列表结果中: %q", u.PasswordDigest)
		}
		if u.UserName == "" || u.Role == "" {
			t.Fatalf("基础字段缺失: %+v", u)
		}
	}

	// 第二页：剩 1 条
	resp, err = srv.ListAllUsers(context.Background(), 2, 2)
	if err != nil {
		t.Fatalf("ListAllUsers page2: %v", err)
	}
	if page2 := resp.([]*user.User); len(page2) != 1 {
		t.Fatalf("page2 len = %d, want 1", len(page2))
	}

	// 非法分页参数回退默认值（page=1, pageSize=50）
	resp, err = srv.ListAllUsers(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("ListAllUsers default: %v", err)
	}
	if all := resp.([]*user.User); len(all) != 3 {
		t.Fatalf("默认分页应取全量 3 条，got %d", len(all))
	}
}
