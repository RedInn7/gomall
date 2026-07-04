package admin

import (
	"context"
	"errors"
	"sync"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
)

var (
	adminSrvIns  *AdminSrv
	adminSrvOnce sync.Once
)

type AdminSrv struct{}

func GetAdminSrv() *AdminSrv {
	adminSrvOnce.Do(func() { adminSrvIns = &AdminSrv{} })
	return adminSrvIns
}

// ListAllUsers admin 接口：列出所有用户（不含密码摘要）
func (s *AdminSrv) ListAllUsers(ctx context.Context, page, pageSize int) (interface{}, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	return user.NewUserDao(ctx).ListPaged(page, pageSize)
}

// PromoteUser 把指定用户的角色设为 role（user / merchant / admin，白名单校验）。
// 设回 user 即降权，升降共用同一入口。仅 admin 可调用（路由层已限）。
func (s *AdminSrv) PromoteUser(ctx context.Context, targetUserId uint, role string) error {
	switch role {
	case user.RoleUser, user.RoleMerchant, user.RoleAdmin:
	default:
		return errors.New("非法角色: " + role)
	}
	target, err := user.NewUserDao(ctx).GetUserById(targetUserId)
	if err != nil {
		return err
	}
	if target == nil || target.ID == 0 {
		return errors.New("目标用户不存在")
	}
	target.Role = role
	if err := user.NewUserDao(ctx).UpdateUserById(targetUserId, target); err != nil {
		return err
	}
	middleware.InvalidateRoleCache(targetUserId)
	log.LogrusObj.Infof("user %d role set to %s", targetUserId, role)
	return nil
}

// BootstrapPromoteSelf 当系统中尚无 admin 时，允许任意已登录用户把自己提升为 admin，方便初始化。
// 一旦存在 admin，本接口立即不可用。
func (s *AdminSrv) BootstrapPromoteSelf(ctx context.Context) error {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return err
	}
	count, err := user.NewUserDao(ctx).CountByRole(user.RoleAdmin)
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("系统已存在 admin，禁止使用 bootstrap 接口")
	}
	return s.PromoteUser(ctx, u.Id, user.RoleAdmin)
}
