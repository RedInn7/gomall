package user

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/RedInn7/gomall/pkg/utils/ctl"
)

func TestMoneyPasswordLocksAfterRepeatedFailures(t *testing.T) {
	stubMoneyPasswordLimiter(t)
	digest, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{MoneyPasswordDigest: string(digest)}
	u.ID = 1
	ctx := context.Background()

	if err := VerifyMoneyPassword(ctx, &User{}, "123456"); !errors.Is(err, ErrMoneyPasswordNotSet) {
		t.Fatalf("empty digest must be rejected, got %v", err)
	}

	// 中途输对一次会清零，之后要重新连续输错满 5 次才锁。
	for i := 0; i < moneyPasswordMaxAttempts-1; i++ {
		if err := VerifyMoneyPassword(ctx, u, "000000"); !errors.Is(err, ErrMoneyKeyIncorrect) {
			t.Fatalf("attempt %d: want ErrMoneyKeyIncorrect, got %v", i+1, err)
		}
	}
	if err := VerifyMoneyPassword(ctx, u, "123456"); err != nil {
		t.Fatalf("correct password before the limit: %v", err)
	}
	for i := 0; i < moneyPasswordMaxAttempts; i++ {
		if err := VerifyMoneyPassword(ctx, u, "000000"); !errors.Is(err, ErrMoneyKeyIncorrect) {
			t.Fatalf("attempt %d: want ErrMoneyKeyIncorrect, got %v", i+1, err)
		}
	}
	if err := VerifyMoneyPassword(ctx, u, "123456"); !errors.Is(err, ErrMoneyPasswordLocked) {
		t.Fatalf("correct password after %d failures must be locked, got %v", moneyPasswordMaxAttempts, err)
	}
}

// 补设支付密码只给摘要为空的存量用户：凭登录密码设一次，之后不能再用这个接口覆盖。
func TestSetMoneyPasswordOnlyWhenUnset(t *testing.T) {
	db := openUserTestDB(t)
	u := seedUserWithBalance(t, db, 1, 0)
	digest, err := bcrypt.GenerateFromPassword([]byte("login-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	// 迁移加列时存量行的支付密码摘要是 NULL，不是空串。
	if err := db.Model(u).Updates(map[string]interface{}{
		"password_digest": string(digest), "money_password_digest": gorm.Expr("NULL"),
	}).Error; err != nil {
		t.Fatal(err)
	}
	ctx := ctl.NewContext(context.Background(), &ctl.UserInfo{Id: 1})

	if _, err := GetUserSrv().SetMoneyPassword(ctx, &SetMoneyPasswordReq{Password: "wrong-pass", Key: "123456"}); !errors.Is(err, ErrCurrentPasswordIncorrect) {
		t.Fatalf("wrong login password: want ErrCurrentPasswordIncorrect, got %v", err)
	}
	if _, err := GetUserSrv().SetMoneyPassword(ctx, &SetMoneyPasswordReq{Password: "login-pass", Key: "123456"}); err != nil {
		t.Fatalf("set money password: %v", err)
	}
	if _, err := GetUserSrv().SetMoneyPassword(ctx, &SetMoneyPasswordReq{Password: "login-pass", Key: "654321"}); !errors.Is(err, ErrMoneyPasswordAlreadySet) {
		t.Fatalf("overwrite: want ErrMoneyPasswordAlreadySet, got %v", err)
	}
	// 并发兜底：绕过服务层的前置判断，条件写入同样不覆盖已有摘要。
	if set, err := NewUserDaoByDB(db).SetMoneyPasswordIfUnset(1, "overwritten"); err != nil || set {
		t.Fatalf("conditional write must not overwrite: set=%v err=%v", set, err)
	}

	var got User
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !got.CheckMoneyPassword("123456") {
		t.Fatal("money password must stay the first one set")
	}
}

// stubMoneyPasswordLimiter 用内存计数替换 Redis 滑动窗口，语义同 SlidingWindowAllow：满额前放行并 +1。
func stubMoneyPasswordLimiter(t *testing.T) {
	t.Helper()
	attempts := map[uint]int{}
	prevAttempt, prevReset := moneyPasswordAttempt, moneyPasswordReset
	moneyPasswordAttempt = func(_ context.Context, userID uint) (bool, error) {
		if attempts[userID] >= moneyPasswordMaxAttempts {
			return false, nil
		}
		attempts[userID]++
		return true, nil
	}
	moneyPasswordReset = func(_ context.Context, userID uint) error {
		delete(attempts, userID)
		return nil
	}
	t.Cleanup(func() { moneyPasswordAttempt, moneyPasswordReset = prevAttempt, prevReset })
}
