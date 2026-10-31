package user

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
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
