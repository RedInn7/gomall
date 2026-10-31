package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
	"github.com/RedInn7/gomall/pkg/utils/log"
	"github.com/RedInn7/gomall/repository/cache"
)

// 支付密码限次：同一用户 30 分钟滑动窗口内最多尝试 5 次，校验通过即清零，
// 所以只有连续输错才会累计；满 5 次后直接拒绝，不再做 bcrypt 比对。
const (
	moneyPasswordMaxAttempts = 5
	moneyPasswordLockWindow  = 30 * time.Minute
)

var (
	// ErrMoneyPasswordNotSet 支付密码摘要为空。注册时必设支付密码，只有迁移前的种子用户没有。
	ErrMoneyPasswordNotSet = errors.New("尚未设置支付密码，请先设置支付密码")
	// ErrMoneyPasswordLocked 连续输错次数达到上限。
	ErrMoneyPasswordLocked = errors.New("支付密码错误次数过多，请 30 分钟后再试")
	// ErrMoneyPasswordAlreadySet 补设接口只给没有支付密码的存量用户用，已设置过的不能拿它覆盖。
	ErrMoneyPasswordAlreadySet = errors.New("支付密码已设置")
)

// SetMoneyPassword 给没有支付密码的存量用户（迁移前的种子用户）补设支付密码，要求当前登录密码。
// 只允许摘要为空时设置一次，这不是"修改支付密码"：偷到登录态和登录密码也改不掉已有的支付密码。
func (s *UserSrv) SetMoneyPassword(ctx context.Context, req *SetMoneyPasswordReq) (*UserInfoResp, error) {
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
	// 先判是否已设置：已有支付密码的用户拿这个接口连登录密码都试不了
	if user.MoneyPasswordDigest != "" {
		return nil, ErrMoneyPasswordAlreadySet
	}
	if !user.CheckPassword(req.Password) {
		return nil, ErrCurrentPasswordIncorrect
	}
	if err = user.SetMoneyPassword(req.Key); err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	// 条件写入兜底并发：两个请求同时补设，只有先到的生效
	set, err := userDao.SetMoneyPasswordIfUnset(u.Id, user.MoneyPasswordDigest)
	if err != nil {
		log.LogrusObj.Error(err)
		return nil, err
	}
	if !set {
		return nil, ErrMoneyPasswordAlreadySet
	}
	return nil, nil
}

// moneyPasswordAttempt 记一次尝试并返回是否放行；moneyPasswordReset 校验通过后清零。
// 复用 cache.SlidingWindowAllow 的 Redis 滑动窗口；与 preorder.nowFn 一样留成函数变量，单测替换为内存实现。
var (
	moneyPasswordAttempt = func(ctx context.Context, userID uint) (bool, error) {
		nowMS := time.Now().UnixMilli()
		buf := make([]byte, 6)
		_, _ = rand.Read(buf)
		allowed, _, err := cache.SlidingWindowAllow(ctx, moneyPasswordKey(userID),
			moneyPasswordLockWindow.Milliseconds(), moneyPasswordMaxAttempts, nowMS,
			strconv.FormatInt(nowMS, 10)+"-"+hex.EncodeToString(buf))
		return allowed, err
	}
	moneyPasswordReset = func(ctx context.Context, userID uint) error {
		return cache.RedisClient.Del(ctx, moneyPasswordKey(userID)).Err()
	}
)

func moneyPasswordKey(userID uint) string {
	return cache.RateLimitKey("money_password", strconv.FormatUint(uint64(userID), 10))
}

// VerifyMoneyPassword 扣款 / 查余额前统一的支付密码校验，带失败限次。
//   - 摘要为空直接拒绝，不再"没设密码就放行"
//   - 窗口内尝试次数已满返回 ErrMoneyPasswordLocked
//   - Redis 不可用时拒绝：限次失效等于放开对 6 位数字的暴力枚举
func VerifyMoneyPassword(ctx context.Context, u *User, pin string) error {
	if u.MoneyPasswordDigest == "" {
		return ErrMoneyPasswordNotSet
	}
	allowed, err := moneyPasswordAttempt(ctx, u.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrMoneyPasswordLocked
	}
	if !u.CheckMoneyPassword(pin) {
		return ErrMoneyKeyIncorrect
	}
	if err := moneyPasswordReset(ctx, u.ID); err != nil {
		// 清零失败只会让之后的限次更严，不影响本次已通过的校验。
		log.LogrusObj.Warnf("reset money password attempts failed user=%d err=%v", u.ID, err)
	}
	return nil
}
