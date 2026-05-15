package service

import (
	"context"
	"errors"
	"sync"

	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
	"github.com/RedInn7/gomall/repository/db/model"
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
	var users []*model.User
	db := dao.NewUserDao(ctx).DB
	err := db.Model(&model.User{}).
		Select("id, user_name, nick_name, email, status, role, created_at").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Order("id DESC").
		Find(&users).Error
	return users, err
}

// PromoteToAdmin 把指定用户提升为 admin。仅 admin 可调用（路由层已限）。
func (s *AdminSrv) PromoteToAdmin(ctx context.Context, targetUserId uint) error {
	target, err := dao.NewUserDao(ctx).GetUserById(targetUserId)
	if err != nil {
		return err
	}
	if target == nil || target.ID == 0 {
		return errors.New("目标用户不存在")
	}
	target.Role = model.RoleAdmin
	if err := dao.NewUserDao(ctx).UpdateUserById(targetUserId, target); err != nil {
		return err
	}
	middleware.InvalidateRoleCache(targetUserId)
	log.LogrusObj.Infof("user %d promoted to admin", targetUserId)
	return nil
}

// BootstrapPromoteSelf 当系统中尚无 admin 时，允许任意已登录用户把自己提升为 admin，方便初始化。
// 一旦存在 admin，本接口立即不可用。
func (s *AdminSrv) BootstrapPromoteSelf(ctx context.Context) error {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return err
	}
	db := dao.NewUserDao(ctx).DB
	var count int64
	if err := db.Model(&model.User{}).Where("role = ?", model.RoleAdmin).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("系统已存在 admin，禁止使用 bootstrap 接口")
	}
	return s.PromoteToAdmin(ctx, u.Id)
}
