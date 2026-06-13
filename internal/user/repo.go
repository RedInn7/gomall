package user

import (
	"context"

	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/db/dao"
)

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
	u, f := new(User), new(User)
	d.DB.Model(&User{}).Where(`id = ?`, uId).First(&u)
	d.DB.Model(&User{}).Where(`id = ?`, followerId).First(&f)
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
	u, f := new(User), new(User)
	d.DB.Model(&User{}).Where(`id = ?`, uId).First(&u)
	d.DB.Model(&User{}).Where(`id = ?`, followerId).First(&f)
	err = d.DB.Model(&u).Association(`Relations`).Delete(f)
	if err != nil {
		log.LogrusObj.Error(err)
		return
	}
	return
}

// GetUserById 根据 id 获取用户
func (d *UserDao) GetUserById(uId uint) (user *User, err error) {
	err = d.DB.Model(&User{}).Where("id=?", uId).
		First(&user).Error
	return
}

// UpdateUserById 根据 id 更新用户信息
func (d *UserDao) UpdateUserById(uId uint, user *User) (err error) {
	return d.DB.Model(&User{}).Where("id=?", uId).
		Updates(&user).Error
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
