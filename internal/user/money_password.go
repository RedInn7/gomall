package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

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
)

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
