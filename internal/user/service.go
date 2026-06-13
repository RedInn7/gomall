package user

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"sync"

	"golang.org/x/crypto/bcrypt"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/email"
	"github.com/RedInn7/gomall/pkg/utils/jwt"
	"github.com/RedInn7/gomall/pkg/utils/log"
	util "github.com/RedInn7/gomall/pkg/utils/upload"
)

var UserSrvIns *UserSrv
var UserSrvOnce sync.Once

type UserSrv struct {
}

func GetUserSrv() *UserSrv {
	UserSrvOnce.Do(func() {
		UserSrvIns = &UserSrv{}
	})
	return UserSrvIns
}

// UserRegister 用户注册
func (s *UserSrv) UserRegister(ctx context.Context, req *UserRegisterReq) (*UserInfoResp, error) {
	userDao := NewUserDao(ctx)
	_, exist, err := userDao.ExistOrNotByUserName(req.UserName)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if exist {
		return nil, errors.New("用户已经存在了")
	}
	user := &User{
		NickName: req.NickName,
		UserName: req.UserName,
		Status:   Active,
		Money:    consts.UserInitMoney,
	}
	// 加密密码
	if err = user.SetPassword(req.Password); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 加密money
	money, err := user.EncryptMoney(req.Key)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	user.Money = money
	// 默认头像走的local
	user.Avatar = consts.UserDefaultAvatarLocal
	if conf.Config.System.UploadModel == consts.UploadModelOss {
		// 如果配置是走oss，则用url作为默认头像
		user.Avatar = consts.UserDefaultAvatarOss
	}

	// 创建用户
	err = userDao.CreateUser(user)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// UserLogin 用户登陆函数
func (s *UserSrv) UserLogin(ctx context.Context, req *UserServiceReq) (*UserTokenData, error) {
	var user *User
	userDao := NewUserDao(ctx)
	user, exist, err := userDao.ExistOrNotByUserName(req.UserName)
	if !exist { // 如果查询不到，返回相应的错误
		log.LogrusObj.Error(err)
		return nil, errors.New("用户不存在")
	}

	if !user.CheckPassword(req.Password) {
		return nil, errors.New("账号/密码不正确")
	}

	accessToken, refreshToken, err := jwt.GenerateToken(user.ID, req.UserName)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	userResp := &UserInfoResp{
		ID:       user.ID,
		UserName: user.UserName,
		NickName: user.NickName,
		Email:    user.Email,
		Status:   user.Status,
		Avatar:   user.AvatarURL(),
		CreateAt: user.CreatedAt.Unix(),
	}

	return &UserTokenData{
		User:         userResp,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// UserInfoUpdate 用户修改信息
func (s *UserSrv) UserInfoUpdate(ctx context.Context, req *UserInfoUpdateReq) (*UserInfoResp, error) {
	// 找到用户
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	userDao := NewUserDao(ctx)
	user, err := userDao.GetUserById(u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	if req.NickName != "" {
		user.NickName = req.NickName
	}

	err = userDao.UpdateUserById(u.Id, user)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// UserAvatarUpload 更新头像
func (s *UserSrv) UserAvatarUpload(ctx context.Context, file multipart.File, fileSize int64, req *UserServiceReq) (*UserInfoResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	uId := u.Id
	userDao := NewUserDao(ctx)
	user, err := userDao.GetUserById(uId)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	var path string
	if conf.Config.System.UploadModel == consts.UploadModelLocal { // 兼容两种存储方式
		path, err = util.AvatarUploadToLocalStatic(file, uId, user.UserName)
	} else {
		path, err = util.UploadToQiNiu(file, fileSize)
	}
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	user.Avatar = path
	err = userDao.UpdateUserById(uId, user)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// SendEmail 发送邮件
func (s *UserSrv) SendEmail(ctx context.Context, req *SendEmailServiceReq) (*UserInfoResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	var passwordDigest string
	if req.OperationType == consts.EmailOperationUpdatePassword {
		if req.Password == "" {
			err = errors.New("修改密码场景必须提供新密码")
			log.LogrusObj.Error(err)
			return nil, err
		}
		digest, hashErr := bcrypt.GenerateFromPassword([]byte(req.Password), PassWordCost)
		if hashErr != nil {
			log.LogrusObj.Error(hashErr)
			return nil, hashErr
		}
		passwordDigest = string(digest)
	}

	var address string
	token, err := jwt.GenerateEmailToken(u.Id, req.OperationType, req.Email, passwordDigest)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	sender := email.NewEmailSender()
	address = conf.Config.Email.ValidEmail + token
	mailText := fmt.Sprintf(consts.EmailOperationMap[req.OperationType], address)
	if err = sender.Send(mailText, req.Email, "FanOneMall"); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// Valid 验证内容
func (s *UserSrv) Valid(ctx context.Context, req *ValidEmailServiceReq) (*UserInfoResp, error) {
	var userId uint
	var email string
	var passwordDigest string
	var operationType uint
	// 验证token
	if req.Token == "" {
		err := errors.New("token输入值为空")
		log.LogrusObj.Error(err)
		return nil, err
	}
	claims, err := jwt.ParseEmailToken(req.Token)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	} else {
		userId = claims.UserID
		email = claims.Email
		passwordDigest = claims.PasswordDigest
		operationType = claims.OperationType
	}

	// 获取该用户信息
	userDao := NewUserDao(ctx)
	user, err := userDao.GetUserById(userId)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	switch operationType {
	case consts.EmailOperationBinding:
		user.Email = email
	case consts.EmailOperationNoBinding:
		user.Email = ""
	case consts.EmailOperationUpdatePassword:
		if passwordDigest == "" {
			err = errors.New("token 缺少密码摘要")
			log.LogrusObj.Error(err)
			return nil, err
		}
		user.PasswordDigest = passwordDigest
	default:
		return nil, errors.New("操作不符合")
	}

	err = userDao.UpdateUserById(userId, user)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return &UserInfoResp{
		ID:       user.ID,
		UserName: user.UserName,
		NickName: user.NickName,
		Email:    user.Email,
		Status:   user.Status,
		Avatar:   user.AvatarURL(),
		CreateAt: user.CreatedAt.Unix(),
	}, nil
}

// UserInfoShow 用户信息展示
func (s *UserSrv) UserInfoShow(ctx context.Context) (*UserInfoResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	user, err := NewUserDao(ctx).GetUserById(u.Id)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return &UserInfoResp{
		ID:       user.ID,
		UserName: user.UserName,
		NickName: user.NickName,
		Email:    user.Email,
		Status:   user.Status,
		Avatar:   user.AvatarURL(),
		CreateAt: user.CreatedAt.Unix(),
	}, nil
}

func (s *UserSrv) UserFollow(ctx context.Context, req *UserFollowingReq) (*UserInfoResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	err = NewUserDao(ctx).FollowUser(u.Id, req.Id)

	return nil, err
}

func (s *UserSrv) UserUnFollow(ctx context.Context, req *UserUnFollowingReq) (*UserInfoResp, error) {
	u, err := ctl.GetUserInfo(ctx)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	err = NewUserDao(ctx).UnFollowUser(u.Id, req.Id)

	return nil, err
}
