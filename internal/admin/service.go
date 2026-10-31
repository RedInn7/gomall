package admin

import (
	"context"
	"crypto/subtle"
	"errors"
	"os"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/internal/user"
	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

var (
	adminSrvIns  *AdminSrv
	adminSrvOnce sync.Once
)

var (
	// ErrBootstrapDisabled 服务端未配置 ADMIN_BOOTSTRAP_TOKEN，bootstrap 接口一律拒绝。
	ErrBootstrapDisabled = errors.New("bootstrap 未开放：服务端未配置 ADMIN_BOOTSTRAP_TOKEN")
	// ErrBootstrapToken 请求携带的 bootstrap token 与服务端配置不一致。
	ErrBootstrapToken = errors.New("bootstrap token 不正确")
	// ErrLastAdmin 系统至少保留一个 admin：降级最后一个 admin 会让 bootstrap 重新开放。
	ErrLastAdmin = errors.New("不能降级最后一个 admin")
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
// 最后一个 admin 不允许降级。
func (s *AdminSrv) PromoteUser(ctx context.Context, targetUserId uint, role string) error {
	switch role {
	case user.RoleUser, user.RoleMerchant, user.RoleAdmin:
	default:
		return errors.New("非法角色: " + role)
	}
	return changeRole(ctx, targetUserId, role, func(admins int64, current string) error {
		if current == user.RoleAdmin && role != user.RoleAdmin && admins <= 1 {
			return ErrLastAdmin
		}
		return nil
	})
}

// BootstrapPromoteSelf 当系统中尚无 admin 时，把当前登录用户提升为 admin，方便初始化。
// 必须在服务端配置 ADMIN_BOOTSTRAP_TOKEN 且请求带上相同 token；一旦存在 admin 立即不可用。
func (s *AdminSrv) BootstrapPromoteSelf(ctx context.Context, token string) error {
	expected := strings.TrimSpace(os.Getenv("ADMIN_BOOTSTRAP_TOKEN"))
	if expected == "" {
		return ErrBootstrapDisabled
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {
		return ErrBootstrapToken
	}
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		return err
	}
	return changeRole(ctx, u.Id, user.RoleAdmin, func(admins int64, _ string) error {
		if admins > 0 {
			return errors.New("系统已存在 admin，禁止使用 bootstrap 接口")
		}
		return nil
	})
}

// changeRole 在一个事务里先锁 admin 集合、再锁目标行，check 用锁内读到的 admin 数量与目标
// 当前角色做业务校验，通过后只写 role 一列（整行回写会把并发修改的其它列覆盖回旧值）。
// "统计 admin + 改角色"在同一把锁下完成，并发 bootstrap 不会产生多个 admin，并发降级也
// 降不掉最后一个 admin；所有改角色事务加锁顺序一致，彼此串行而不互相死锁。
func changeRole(ctx context.Context, userId uint, role string, check func(admins int64, current string) error) error {
	err := dao.NewDBClient(ctx).Transaction(func(tx *gorm.DB) error {
		// ponytail: 依赖 InnoDB 默认 REPEATABLE READ 的 next-key 锁。还没有 admin 时锁住的是
		// role 索引上的间隙，并发 bootstrap 会有一方死锁回滚、只成功一个；若改成 READ COMMITTED
		// （间隙锁失效）需换成专用锁行。
		admins, err := user.NewUserDaoByDB(tx.Clauses(clause.Locking{Strength: "UPDATE"})).CountByRole(user.RoleAdmin)
		if err != nil {
			return err
		}
		userDao := user.NewUserDaoByDB(tx)
		target, err := userDao.GetUserByIdForUpdate(userId)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("目标用户不存在")
		}
		if err != nil {
			return err
		}
		if err = check(admins, target.Role); err != nil {
			return err
		}
		return userDao.UpdateUserColumns(userId, map[string]interface{}{"role": role})
	})
	if err != nil {
		return err
	}
	middleware.InvalidateRoleCache(userId)
	log.LogrusObj.Infof("user %d role set to %s", userId, role)
	return nil
}
