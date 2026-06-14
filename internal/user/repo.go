package user

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

// ErrUserNotExist 关注/取关的目标用户不存在
var ErrUserNotExist = errors.New("用户不存在")

type UserDao struct {
	*gorm.DB
}

func NewUserDao(ctx context.Context) *UserDao {
	return &UserDao{dao.NewDBClient(ctx)}
}

func NewUserDaoByDB(db *gorm.DB) *UserDao {
	return &UserDao{db}
}

// FollowUser userId 关注了 followerId
func (d *UserDao) FollowUser(uId, followerId uint) (err error) {
	if uId == followerId {
		return errors.New("不能关注自己")
	}
	u, f, err := d.loadRelationUsers(uId, followerId)
	if err != nil {
		return err
	}
	err = d.DB.Model(&f).Association(`Relations`).
		Append([]User{*u})
	if err != nil {
		log.LogrusObj.Error(err)
		return err
	}

	return
}

// UnFollowUser 不再关注
func (d *UserDao) UnFollowUser(uId, followerId uint) (err error) {
	if uId == followerId {
		return errors.New("不能取关自己")
	}
	u, f, err := d.loadRelationUsers(uId, followerId)
	if err != nil {
		return err
	}
	err = d.DB.Model(&u).Association(`Relations`).Delete(f)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	return
}

// loadRelationUsers 加载关注关系两端的用户，任一不存在返回 ErrUserNotExist，
// 避免对主键为 0 的零值 User 执行关联写入而污染关系表。
func (d *UserDao) loadRelationUsers(uId, followerId uint) (u, f *User, err error) {
	u, f = new(User), new(User)
	if err = d.DB.Model(&User{}).Where(`id = ?`, uId).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrUserNotExist
		}
		log.LogrusObj.Error(err)
		return nil, nil, err
	}
	if err = d.DB.Model(&User{}).Where(`id = ?`, followerId).First(&f).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrUserNotExist
		}
		log.LogrusObj.Error(err)
		return nil, nil, err
	}
	return u, f, nil
}

// GetUserById 根据 id 获取用户
func (d *UserDao) GetUserById(uId uint) (user *User, err error) {
	err = d.DB.Model(&User{}).Where("id=?", uId).
		First(&user).Error
	return
}

// GetUserByIdForUpdate 在事务内对用户行加行锁（SELECT ... FOR UPDATE）后读取。
// 余额的"读-改-写"必须走这条路径：行锁把并发支付/退款串行化，杜绝丢失更新
// （两个事务读到同一余额各自扣减，后写覆盖先写）。
func (d *UserDao) GetUserByIdForUpdate(uId uint) (user *User, err error) {
	err = d.DB.Clauses(clause.Locking{Strength: "UPDATE"}).
		Model(&User{}).Where("id=?", uId).
		First(&user).Error
	return
}

// UpdateUserById 根据 id 更新用户信息
func (d *UserDao) UpdateUserById(uId uint, user *User) (err error) {
	return d.DB.Model(&User{}).Where("id=?", uId).
		Updates(&user).Error
}

// UpdateUserColumns 按列名 map 更新，支持把字段写成零值（如解绑邮箱写空串），
// 规避 struct Updates 跳过零值的坑。
func (d *UserDao) UpdateUserColumns(uId uint, columns map[string]interface{}) error {
	return d.DB.Model(&User{}).Where("id=?", uId).
		Updates(columns).Error
}

// ExistOrNotByUserName 根据username判断是否存在该名字
func (d *UserDao) ExistOrNotByUserName(userName string) (user *User, exist bool, err error) {
	var count int64
	err = d.DB.Model(&User{}).Where("user_name = ?", userName).Count(&count).Error
	if count == 0 {
		return user, false, err
	}
	err = d.DB.Model(&User{}).Where("user_name = ?", userName).First(&user).Error
	if err != nil {
		return user, false, err
	}
	return user, true, nil
}

// CreateUser 创建用户
func (d *UserDao) CreateUser(user *User) error {
	return d.DB.Model(&User{}).Create(&user).Error
}
