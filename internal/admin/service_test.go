package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

func TestBootstrapRequiresConfiguredToken(t *testing.T) {
	db := openAdminTestDB(t, user.RoleUser, user.RoleUser)
	ctx1 := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})
	ctx2 := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 2})

	t.Setenv("ADMIN_BOOTSTRAP_TOKEN", "")
	if err := GetAdminSrv().BootstrapPromoteSelf(ctx1, "anything"); !errors.Is(err, ErrBootstrapDisabled) {
		t.Fatalf("未配置 token 时应直接拒绝, got %v", err)
	}

	t.Setenv("ADMIN_BOOTSTRAP_TOKEN", "s3cret")
	for _, token := range []string{"", "wrong", "s3cret "} {
		if err := GetAdminSrv().BootstrapPromoteSelf(ctx1, token); !errors.Is(err, ErrBootstrapToken) {
			t.Fatalf("token %q 应被拒绝, got %v", token, err)
		}
	}
	if role := roleOf(t, db, 1); role != user.RoleUser {
		t.Fatalf("token 校验失败不能提权, role=%s", role)
	}

	if err := GetAdminSrv().BootstrapPromoteSelf(ctx1, "s3cret"); err != nil {
		t.Fatalf("正确 token 应能 bootstrap: %v", err)
	}
	if err := GetAdminSrv().BootstrapPromoteSelf(ctx2, "s3cret"); err == nil {
		t.Fatal("已有 admin 后 bootstrap 必须关闭")
	}
	if roleOf(t, db, 1) != user.RoleAdmin || roleOf(t, db, 2) != user.RoleUser {
		t.Fatalf("只能产生一个 admin: user1=%s user2=%s", roleOf(t, db, 1), roleOf(t, db, 2))
	}
}

func TestLastAdminCannotBeDemoted(t *testing.T) {
	db := openAdminTestDB(t, user.RoleAdmin, user.RoleUser)
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})
	srv := GetAdminSrv()

	if err := srv.PromoteUser(ctx, 1, user.RoleUser); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("最后一个 admin 不能降级, got %v", err)
	}
	if err := srv.PromoteUser(ctx, 2, user.RoleAdmin); err != nil {
		t.Fatalf("promote user 2: %v", err)
	}
	if err := srv.PromoteUser(ctx, 1, user.RoleMerchant); err != nil {
		t.Fatalf("还有其它 admin 时可以降级: %v", err)
	}
	if err := srv.PromoteUser(ctx, 2, user.RoleUser); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("降级后剩下的 admin 同样不能被降级, got %v", err)
	}
	if roleOf(t, db, 1) != user.RoleMerchant || roleOf(t, db, 2) != user.RoleAdmin {
		t.Fatalf("unexpected roles: user1=%s user2=%s", roleOf(t, db, 1), roleOf(t, db, 2))
	}
}

// openAdminTestDB 建 sqlite 内存库，按顺序种下 id=1..n 的用户及其角色。
func openAdminTestDB(t *testing.T, roles ...string) *gorm.DB {
	t.Helper()
	prevLog := log.LogrusObj
	log.LogrusObj = logrus.New()
	log.LogrusObj.Out = io.Discard
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:admin-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	prevDB := dao.SetTestDB(db)
	t.Cleanup(func() {
		dao.SetTestDB(prevDB)
		log.LogrusObj = prevLog
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&user.User{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for i, role := range roles {
		u := &user.User{UserName: fmt.Sprintf("admin-test-%d", i+1), Role: role}
		u.ID = uint(i + 1)
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}
	return db
}

func roleOf(t *testing.T, db *gorm.DB, id uint) string {
	t.Helper()
	var u user.User
	if err := db.First(&u, id).Error; err != nil {
		t.Fatalf("load user %d: %v", id, err)
	}
	return u.Role
}
