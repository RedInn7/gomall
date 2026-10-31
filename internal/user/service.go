package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	conf "github.com/RedInn7/gomall/config"
	"github.com/RedInn7/gomall/consts"
	"github.com/RedInn7/gomall/middleware"
	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/email"
	"github.com/RedInn7/gomall/pkg/utils/jwt"
	"github.com/RedInn7/gomall/pkg/utils/log"
	util "github.com/RedInn7/gomall/pkg/utils/upload"
	"github.com/RedInn7/gomall/repository/cache"
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
	// 设置支付密码摘要（与余额加密分离：摘要用于支付前校验，余额用服务端密钥加密）
	if err = user.SetMoneyPassword(req.Key); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 用服务端密钥加密初始余额
	money, err := user.EncryptMoney()
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
	if err != nil { // 真实 DB / 基础设施错误，不能伪装成"用户不存在"，也避免后续 nil 解引用
		log.LogrusObj.Error(err)
		return nil, err
	}
	if !exist { // 如果查询不到，返回相应的错误
		return nil, errors.New("用户不存在")
	}

	if !user.CheckPassword(req.Password) {
		return nil, errors.New("账号/密码不正确")
	}

	accessToken, refreshToken, err := jwt.GenerateToken(user.ID, req.UserName, user.TokenVersion)
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

	if req.NickName != "" {
		// 只写改动的列：整行回写会把并发改掉的余额 / 密码 / 邮箱覆盖回本次读到的旧值。
		err = NewUserDao(ctx).UpdateUserColumns(u.Id, map[string]interface{}{"nick_name": req.NickName})
		if err != nil {
			log.LogrusObj.Error(err)
			return nil, err
		}
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

	err = userDao.UpdateUserColumns(uId, map[string]interface{}{"avatar": path})
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

var (
	// ErrCurrentPasswordIncorrect 改密码 / 绑定邮箱前校验当前登录密码失败。
	ErrCurrentPasswordIncorrect = errors.New("当前密码不正确")
	// ErrEmailTokenUsed 邮件链接只能用一次。
	ErrEmailTokenUsed = errors.New("链接已使用，请重新获取邮件")
)

// SendEmail 发送邮件。收件地址以服务端为准：
//   - 绑定邮箱：发到请求里的新邮箱，用来确认邮箱归属；要求当前密码
//   - 解绑邮箱：发到已绑定的邮箱
//   - 改密码：发到已绑定的邮箱，要求当前密码
//
// 不再发到请求里任填的地址：否则偷到登录态就能把改密链接发给自己，或拿接口给任意地址发信。
// 每用户每分钟 1 封的限频挂在路由上（routes.go）。
func (s *UserSrv) SendEmail(ctx context.Context, req *SendEmailServiceReq) (*UserInfoResp, error) {
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

	var target, passwordDigest string
	switch req.OperationType {
	case consts.EmailOperationBinding:
		if !user.CheckPassword(req.CurrentPassword) {
			return nil, ErrCurrentPasswordIncorrect
		}
		if req.Email == "" {
			return nil, errors.New("请填写要绑定的邮箱")
		}
		target = req.Email
	case consts.EmailOperationNoBinding:
		if user.Email == "" {
			return nil, errors.New("尚未绑定邮箱")
		}
		target = user.Email
	case consts.EmailOperationUpdatePassword:
		if user.Email == "" {
			return nil, errors.New("请先绑定邮箱再修改密码")
		}
		if !user.CheckPassword(req.CurrentPassword) {
			return nil, ErrCurrentPasswordIncorrect
		}
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
		target = user.Email
	default:
		return nil, errors.New("操作不符合")
	}

	token, err := jwt.GenerateEmailToken(u.Id, req.OperationType, target, passwordDigest)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	sender := email.NewEmailSender()
	address := conf.Config.Email.ValidEmail + token
	mailText := fmt.Sprintf(consts.EmailOperationMap[req.OperationType], address)
	if err = sender.Send(mailText, target, "FanOneMall"); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	return nil, nil
}

// emailTokenClaim 以 SETNX 把邮件 token 标记为已使用，返回是否首次；emailTokenRelease 撤销标记。
// 与 preorder.nowFn 一样留成函数变量，单测替换为内存实现。
var (
	emailTokenClaim = func(ctx context.Context, key string, ttl time.Duration) (bool, error) {
		return cache.RedisClient.SetNX(ctx, key, "1", ttl).Result()
	}
	emailTokenRelease = func(ctx context.Context, key string) error {
		return cache.RedisClient.Del(ctx, key).Err()
	}
)

// emailTokenUsedKey 已用邮件 token 的占位 key，存 sha256 不存原文。
func emailTokenUsedKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "email:token:used:" + hex.EncodeToString(sum[:])
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

	// token 一次性：先占位再改库，并发重放只有一个能进来；改库失败释放占位，同一封邮件可以重试。
	// 占位 TTL 取 token 剩余有效期，过期后 token 本身也失效，占位自然回收。
	var ttl time.Duration
	if claims.ExpiresAt != nil {
		ttl = time.Until(claims.ExpiresAt.Time)
	}
	if ttl <= 0 {
		return nil, errors.New("链接已过期，请重新获取邮件")
	}
	usedKey := emailTokenUsedKey(req.Token)
	first, err := emailTokenClaim(ctx, usedKey, ttl)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if !first {
		return nil, ErrEmailTokenUsed
	}
	applied := false
	defer func() {
		if applied {
			return
		}
		if rbErr := emailTokenRelease(ctx, usedKey); rbErr != nil {
			log.LogrusObj.Errorf("release email token failed user=%d err=%v", userId, rbErr)
		}
	}()

	// 获取该用户信息
	userDao := NewUserDao(ctx)
	user, err := userDao.GetUserById(userId)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	var columns map[string]interface{}
	switch operationType {
	case consts.EmailOperationBinding:
		user.Email = email
		columns = map[string]interface{}{"email": email}
	case consts.EmailOperationNoBinding:
		// 解绑：把 email 置空。GORM 的 struct Updates 会跳过零值，必须按列写空字符串。
		user.Email = ""
		columns = map[string]interface{}{"email": ""}
	case consts.EmailOperationUpdatePassword:
		if passwordDigest == "" {
			err = errors.New("token 缺少密码摘要")
			log.LogrusObj.Error(err)
			return nil, err
		}
		user.PasswordDigest = passwordDigest
		columns = map[string]interface{}{"password_digest": passwordDigest}
	default:
		return nil, errors.New("操作不符合")
	}

	// 只写本次操作改动的列：整行回写会把并发改掉的余额 / 密码 / 邮箱覆盖回本次读到的旧值。
	if err = userDao.UpdateUserColumns(userId, columns); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}

	if operationType == consts.EmailOperationUpdatePassword {
		// 改密码 = 撤销全部旧会话：版本号 +1 并清中间件缓存，
		// 所有已签发 token（包括被盗的）下一个请求即被拒。这就是"改密码立即生效"。
		if err = userDao.BumpTokenVersion(userId); err != nil {
			log.LogrusObj.Error(err)
			return nil, err
		}
		middleware.InvalidateTokenVersionCache(userId)
	}
	applied = true

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
